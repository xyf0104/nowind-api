package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func newTestHelperServer(manager *ConfigManager, site, state string) (*helperServer, error) {
	helper, err := newHelperServer(manager, site, state)
	if err == nil {
		helper.prepare = func() error { return nil }
	}
	return helper, err
}

func TestHelperServerHistoryBackupsAlwaysReturnsJSONArray(t *testing.T) {
	helper, err := newTestHelperServer(NewConfigManager(t.TempDir()), defaultXIASSAPIURL, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNO1")
	if err != nil {
		t.Fatal(err)
	}
	helper.listHistoryBackups = func() ([]HistoryBackupInfo, error) { return nil, nil }

	request := httptest.NewRequest(http.MethodGet, "/api/history-backups", nil)
	request.Host = "127.0.0.1:43123"
	request.Header.Set("X-XIASS-Helper-State", helper.state)
	response := httptest.NewRecorder()
	helper.routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("history backups status = %d, body = %s", response.Code, response.Body.String())
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if string(payload["items"]) != "[]" {
		t.Fatalf("history backups items = %s, want []", payload["items"])
	}
}

func TestHelperServerDeletesConfigurationBackupWithoutRestartingCodex(t *testing.T) {
	manager := NewConfigManager(t.TempDir())
	backup, err := manager.Apply(ApplyConfig{BaseURL: "https://gateway.example.com", APIKey: "sk-test-1234567890"})
	if err != nil {
		t.Fatal(err)
	}
	helper, err := newTestHelperServer(manager, defaultXIASSAPIURL, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNO1")
	if err != nil {
		t.Fatal(err)
	}
	var stops, starts atomic.Int32
	helper.stop = func(CodexInstallation) error { stops.Add(1); return nil }
	helper.start = func(CodexInstallation) error { starts.Add(1); return nil }

	body, err := json.Marshal(map[string]string{"kind": "config", "backup_id": backup.BackupID})
	if err != nil {
		t.Fatal(err)
	}
	postHelperJSON(t, helper.routes(), "/api/delete-backup", helper.state, body, http.StatusOK)
	if _, err := os.Stat(filepath.Join(manager.BackupRoot, backup.BackupID)); !os.IsNotExist(err) {
		t.Fatalf("deleted backup remains on disk: %v", err)
	}
	if stops.Load() != 0 || starts.Load() != 0 {
		t.Fatalf("backup deletion unexpectedly restarted Codex: stop=%d start=%d", stops.Load(), starts.Load())
	}
}

func TestHelperServerListsCompatibleModelsFromLocalHelperOnly(t *testing.T) {
	helper, err := newTestHelperServer(NewConfigManager(t.TempDir()), defaultXIASSAPIURL, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNO1")
	if err != nil {
		t.Fatal(err)
	}
	helper.listModels = func(baseURL, apiKey string) ([]string, error) {
		if baseURL != "http://127.0.0.1:54843/v1" || apiKey != "local-key" {
			t.Fatalf("model discovery input = %q / %q", baseURL, apiKey)
		}
		return []string{"gpt-5.6-luna", "gpt-5.6-sol"}, nil
	}
	body := []byte(`{"base_url":"127.0.0.1:54843","api_key":"local-key"}`)
	response := postHelperJSON(t, helper.routes(), "/api/models", helper.state, body, http.StatusOK)
	models, ok := response["models"].([]any)
	if !ok || len(models) != 2 || models[0] != "gpt-5.6-luna" || models[1] != "gpt-5.6-sol" {
		t.Fatalf("model response = %+v", response)
	}
	postHelperJSON(t, helper.routes(), "/api/models", "", body, http.StatusForbidden)
}

func TestDiscoverCompatibleModelsUsesStandardModelsEndpoint(t *testing.T) {
	var authorization string
	var clientVersion string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		clientVersion = r.URL.Query().Get("client_version")
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-5.6-sol"},{"id":"gpt-5.6-luna"},{"id":"gpt-5.6-sol"},{"id":""}]}`))
	}))
	defer server.Close()

	models, err := discoverCompatibleModels(server.URL+"/v1", "local-key")
	if err != nil {
		t.Fatal(err)
	}
	if authorization != "Bearer local-key" {
		t.Fatalf("authorization header = %q", authorization)
	}
	if clientVersion != "0.146.0" {
		t.Fatalf("client_version = %q, want 0.146.0", clientVersion)
	}
	if got := strings.Join(models, ","); got != "gpt-5.6-luna,gpt-5.6-sol" {
		t.Fatalf("models = %q", got)
	}
}

func TestDiscoverCompatibleModelsAcceptsCodexManifestModels(t *testing.T) {
	var clientVersion string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clientVersion = r.URL.Query().Get("client_version")
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"models":[{"slug":"gpt-6-astra"},{"slug":"gpt-5.6-sol"},{"slug":"gpt-6-astra"}]}`))
	}))
	defer server.Close()

	models, err := discoverCompatibleModels(server.URL+"/v1", "local-key")
	if err != nil {
		t.Fatal(err)
	}
	if clientVersion != "0.146.0" {
		t.Fatalf("client_version = %q, want 0.146.0", clientVersion)
	}
	if got := strings.Join(models, ","); got != "gpt-5.6-sol,gpt-6-astra" {
		t.Fatalf("models = %q", got)
	}
}

func TestHelperServerBrowserCloseRequiresLocalStateAndRequestsShutdown(t *testing.T) {
	helper, err := newTestHelperServer(NewConfigManager(t.TempDir()), defaultXIASSAPIURL, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNO1")
	if err != nil {
		t.Fatal(err)
	}
	handler := helper.routes()

	invalid := httptest.NewRequest(http.MethodPost, "/api/browser-closed", nil)
	invalid.Host = "127.0.0.1:43123"
	invalidResponse := httptest.NewRecorder()
	handler.ServeHTTP(invalidResponse, invalid)
	if invalidResponse.Code != http.StatusForbidden {
		t.Fatalf("invalid browser-close status = %d, want %d", invalidResponse.Code, http.StatusForbidden)
	}
	select {
	case <-helper.shutdown:
		t.Fatal("invalid browser-close request shut down helper")
	default:
	}

	valid := httptest.NewRequest(http.MethodPost, "/api/browser-closed?state="+helper.state, nil)
	valid.Host = "127.0.0.1:43123"
	validResponse := httptest.NewRecorder()
	handler.ServeHTTP(validResponse, valid)
	if validResponse.Code != http.StatusNoContent {
		t.Fatalf("valid browser-close status = %d, want %d", validResponse.Code, http.StatusNoContent)
	}
	select {
	case <-helper.shutdown:
	default:
		t.Fatal("valid browser-close request did not shut down helper")
	}
}

func TestHelperServerApplyAndRestoreFlow(t *testing.T) {
	manager := NewConfigManager(t.TempDir())
	if err := os.WriteFile(manager.ConfigPath, []byte(testOriginalConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	helper, err := newTestHelperServer(manager, "https://gateway.example.com", "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNO1")
	if err != nil {
		t.Fatal(err)
	}
	helper.detect = func() CodexInstallation {
		return CodexInstallation{Found: true, Running: true, AppPath: "/test/Codex.app"}
	}
	var stopCount atomic.Int32
	var startCount atomic.Int32
	helper.stop = func(CodexInstallation) error {
		stopCount.Add(1)
		return nil
	}
	helper.start = func(CodexInstallation) error {
		startCount.Add(1)
		return nil
	}

	handler := helper.routes()

	statusResponse := getJSON(t, handler, "/api/status")
	connectURL, _ := statusResponse["connect_url"].(string)
	if !strings.HasPrefix(connectURL, "https://gateway.example.com/codex-helper/connect?") {
		t.Fatalf("connect_url = %q", connectURL)
	}

	applyBody := []byte(`{"base_url":"https://gateway.example.com","api_key":"sk-test-1234567890","key_name":"Codex"}`)
	apply := postHelperJSON(t, handler, "/api/apply", helper.state, applyBody, http.StatusOK)
	if ok, _ := apply["ok"].(bool); !ok {
		t.Fatalf("apply response = %+v", apply)
	}
	backupID, _ := apply["backup_id"].(string)
	if backupID == "" {
		t.Fatal("apply response has no backup ID")
	}
	if stopCount.Load() != 1 || startCount.Load() != 1 {
		t.Fatalf("lifecycle counts after apply = stop %d, start %d", stopCount.Load(), startCount.Load())
	}

	written, err := os.ReadFile(manager.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(written), `model_provider = "codex_local_access"`) || !strings.Contains(string(written), `[model_providers.codex_local_access]`) {
		t.Fatal("apply endpoint did not force the canonical XIASS provider")
	}

	restoreBody, _ := json.Marshal(map[string]string{"backup_id": backupID})
	restore := postHelperJSON(t, handler, "/api/restore", helper.state, restoreBody, http.StatusOK)
	if ok, _ := restore["ok"].(bool); !ok {
		t.Fatalf("restore response = %+v", restore)
	}
	if stopCount.Load() != 2 || startCount.Load() != 2 {
		t.Fatalf("lifecycle counts after restore = stop %d, start %d", stopCount.Load(), startCount.Load())
	}
	restored, err := os.ReadFile(manager.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(restored) != testOriginalConfig {
		t.Fatal("HTTP restore flow did not restore original config exactly")
	}
}

func TestHelperServerUsesLightweightModelCheckWhenProviderDoesNotChange(t *testing.T) {
	home := t.TempDir()
	writeHistoryConfigWithModel(t, home, providerID, defaultModel)
	helper, err := newTestHelperServer(NewConfigManager(home), "https://gateway.example.com", "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNO1")
	if err != nil {
		t.Fatal(err)
	}
	helper.detect = func() CodexInstallation {
		return CodexInstallation{Found: true, Running: true, AppPath: "/test/Codex.app"}
	}
	helper.stop = func(CodexInstallation) error { return nil }
	helper.start = func(CodexInstallation) error { return nil }
	var repairs atomic.Int32
	var modelSyncs atomic.Int32
	helper.repairHistory = func() (HistoryRepairResult, error) {
		repairs.Add(1)
		return HistoryRepairResult{}, nil
	}
	helper.syncHistoryModel = func() (HistoryRepairResult, error) {
		modelSyncs.Add(1)
		return HistoryRepairResult{TargetProvider: providerID, TargetModel: defaultModel}, nil
	}

	response := postHelperJSON(t, helper.routes(), "/api/apply", helper.state, []byte(`{"base_url":"https://gateway.example.com","api_key":"sk-test-1234567890","key_name":"Codex"}`), http.StatusOK)
	if repairs.Load() != 0 || modelSyncs.Load() != 1 {
		t.Fatalf("same-provider configuration ran full repair %d times and model check %d times", repairs.Load(), modelSyncs.Load())
	}
	history, _ := response["history"].(map[string]any)
	if history["target_model"] != defaultModel || history["scanned_session_files"] != float64(0) {
		t.Fatalf("same-provider history response did not report the lightweight path: %+v", response)
	}
}

func TestHelperServerSynchronizesExistingThreadsWhenOnlyModelChanges(t *testing.T) {
	home := t.TempDir()
	writeHistoryConfigWithModel(t, home, providerID, "gpt-5.6-sol")
	helper, err := newTestHelperServer(NewConfigManager(home), "https://gateway.example.com", "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNO1")
	if err != nil {
		t.Fatal(err)
	}
	helper.detect = func() CodexInstallation {
		return CodexInstallation{Found: true, Running: true, AppPath: "/test/Codex.app"}
	}
	helper.stop = func(CodexInstallation) error { return nil }
	helper.start = func(CodexInstallation) error { return nil }
	var repairs, modelSyncs atomic.Int32
	helper.repairHistory = func() (HistoryRepairResult, error) {
		repairs.Add(1)
		return HistoryRepairResult{}, nil
	}
	helper.syncHistoryModel = func() (HistoryRepairResult, error) {
		modelSyncs.Add(1)
		return HistoryRepairResult{TargetProvider: providerID, TargetModel: "gpt-6-astra", UpdatedModelRows: 3}, nil
	}

	response := postHelperJSON(t, helper.routes(), "/api/apply", helper.state, []byte(`{"base_url":"https://gateway.example.com","api_key":"sk-test-1234567890","key_name":"Codex","model":"gpt-6-astra"}`), http.StatusOK)
	if repairs.Load() != 0 || modelSyncs.Load() != 1 {
		t.Fatalf("same-provider model change ran full repair %d times and model sync %d times", repairs.Load(), modelSyncs.Load())
	}
	history, _ := response["history"].(map[string]any)
	if history["target_model"] != "gpt-6-astra" || history["updated_model_rows"] != float64(3) {
		t.Fatalf("model sync response = %+v", response)
	}
}

func TestHelperServerRunsHistoryRepairWhenProviderChanges(t *testing.T) {
	home := t.TempDir()
	writeHistoryConfig(t, home, legacyProviderID)
	helper, err := newTestHelperServer(NewConfigManager(home), "https://gateway.example.com", "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNO1")
	if err != nil {
		t.Fatal(err)
	}
	helper.detect = func() CodexInstallation {
		return CodexInstallation{Found: true, Running: true, AppPath: "/test/Codex.app"}
	}
	helper.stop = func(CodexInstallation) error { return nil }
	helper.start = func(CodexInstallation) error { return nil }
	var repairs atomic.Int32
	helper.repairHistory = func() (HistoryRepairResult, error) {
		repairs.Add(1)
		return HistoryRepairResult{TargetProvider: providerID}, nil
	}

	postHelperJSON(t, helper.routes(), "/api/apply", helper.state, []byte(`{"base_url":"https://gateway.example.com","api_key":"sk-test-1234567890","key_name":"Codex"}`), http.StatusOK)
	if repairs.Load() != 1 {
		t.Fatalf("provider change repaired history %d times, want 1", repairs.Load())
	}
}

func TestHelperServerRejectsMissingStateAndForeignBaseURL(t *testing.T) {
	helper, err := newTestHelperServer(NewConfigManager(t.TempDir()), "https://gateway.example.com", "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNO1")
	if err != nil {
		t.Fatal(err)
	}
	helper.stop = func(CodexInstallation) error { return nil }
	helper.start = func(CodexInstallation) error { return nil }
	handler := helper.routes()

	body := []byte(`{"base_url":"https://gateway.example.com","api_key":"sk-test-1234567890","key_name":"Codex"}`)
	postHelperJSON(t, handler, "/api/apply", "", body, http.StatusForbidden)
	foreign := []byte(`{"base_url":"https://evil.example","api_key":"sk-test-1234567890","key_name":"Codex"}`)
	postHelperJSON(t, handler, "/api/apply", helper.state, foreign, http.StatusBadRequest)
}

func TestHelperServerManualApplySupportsLoopbackAndRejectsRemoteHTTP(t *testing.T) {
	home := t.TempDir()
	helper, err := newTestHelperServer(NewConfigManager(home), defaultXIASSAPIURL, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNO1")
	if err != nil {
		t.Fatal(err)
	}
	helper.detect = func() CodexInstallation {
		return CodexInstallation{Found: true, Running: true, AppPath: "/test/Codex.app"}
	}
	var stopped, started atomic.Int32
	helper.stop = func(CodexInstallation) error { stopped.Add(1); return nil }
	helper.start = func(CodexInstallation) error { started.Add(1); return nil }
	handler := helper.routes()

	body := []byte(`{"base_url":"127.0.0.1:54843/V1","api_key":"local-key","model":"local-codex-model","review_model":"local-review-model"}`)
	postHelperJSON(t, handler, "/api/apply-manual", "", body, http.StatusForbidden)
	response := postHelperJSON(t, handler, "/api/apply-manual", helper.state, body, http.StatusOK)
	if ok, _ := response["ok"].(bool); !ok {
		t.Fatalf("manual apply response = %+v", response)
	}
	written, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		`model = "local-codex-model"`,
		`review_model = "local-review-model"`,
		`name = "Custom API"`,
		`base_url = "http://127.0.0.1:54843/V1"`,
	} {
		if !strings.Contains(string(written), expected) {
			t.Errorf("manual config is missing %q", expected)
		}
	}
	if stopped.Load() != 1 || started.Load() != 1 {
		t.Fatalf("manual lifecycle counts = stop %d, start %d", stopped.Load(), started.Load())
	}

	unsafeBody := []byte(`{"base_url":"http://gateway.example.com/v1","api_key":"remote-key","model":"remote-model"}`)
	postHelperJSON(t, handler, "/api/apply-manual", helper.state, unsafeBody, http.StatusBadRequest)
	if stopped.Load() != 1 || started.Load() != 1 {
		t.Fatal("invalid remote HTTP input unexpectedly restarted Codex")
	}
}

func TestHelperServerSelectsSiteAtRuntime(t *testing.T) {
	helper, err := newTestHelperServer(NewConfigManager(t.TempDir()), "", "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNO1")
	if err != nil {
		t.Fatal(err)
	}
	handler := helper.routes()

	before := getJSON(t, handler, "/api/status")
	if before["connect_url"] != "" || before["site_url"] != "" {
		t.Fatalf("unconfigured status = %+v", before)
	}

	selected := postHelperJSON(
		t,
		handler,
		"/api/site",
		helper.state,
		[]byte(`{"site_url":"https://gateway.example.com/"}`),
		http.StatusOK,
	)
	connectURL, _ := selected["connect_url"].(string)
	if !strings.HasPrefix(connectURL, "https://gateway.example.com/codex-helper/connect?") {
		t.Fatalf("runtime connect_url = %q", connectURL)
	}
	if selected["site_url"] != "https://gateway.example.com" {
		t.Fatalf("runtime site_url = %v", selected["site_url"])
	}
}

func TestHelperStatusReportsStoredContextSettings(t *testing.T) {
	manager := NewConfigManager(t.TempDir())
	if err := os.WriteFile(manager.ConfigPath, []byte("model_provider = \"codex_local_access\"\nmodel_context_window = 1000000\nmodel_auto_compact_token_limit = 900000\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	helper, err := newTestHelperServer(manager, defaultXIASSAPIURL, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNO1")
	if err != nil {
		t.Fatal(err)
	}
	status := getJSON(t, helper.routes(), "/api/status")
	if status["model_context_window"] != float64(1000000) || status["model_auto_compact_token_limit"] != float64(900000) {
		t.Fatalf("status context settings = %v/%v", status["model_context_window"], status["model_auto_compact_token_limit"])
	}
}

func TestHelperStatusUsesXIASSDefaultsForForeignProvider(t *testing.T) {
	manager := NewConfigManager(t.TempDir())
	foreign := `model_provider = "foreign-relay"
model_context_window = 1000000
model_auto_compact_token_limit = 900000

[model_providers.foreign-relay]
name = "Foreign relay"
base_url = "https://relay.example.com/v1"
`
	if err := os.WriteFile(manager.ConfigPath, []byte(foreign), 0o600); err != nil {
		t.Fatal(err)
	}
	helper, err := newTestHelperServer(manager, defaultXIASSAPIURL, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNO1")
	if err != nil {
		t.Fatal(err)
	}
	status := getJSON(t, helper.routes(), "/api/status")
	if status["model_context_window"] != float64(defaultContextWindow) || status["model_auto_compact_token_limit"] != float64(defaultAutoCompactTokenLimit) {
		t.Fatalf("foreign provider context leaked into XIASS setup: %v/%v", status["model_context_window"], status["model_auto_compact_token_limit"])
	}
}

func TestHelperCarriesContextSelectionThroughSiteCallback(t *testing.T) {
	helper, err := newTestHelperServer(NewConfigManager(t.TempDir()), defaultXIASSAPIURL, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNO1")
	if err != nil {
		t.Fatal(err)
	}
	helper.detect = func() CodexInstallation {
		return CodexInstallation{Found: true, Running: true, AppPath: "/test/Codex.app"}
	}
	helper.stop = func(CodexInstallation) error { return nil }
	helper.start = func(CodexInstallation) error { return nil }
	helper.repairHistory = func() (HistoryRepairResult, error) { return HistoryRepairResult{}, nil }
	var applied ApplyConfig
	helper.applyConfig = func(input ApplyConfig) (ApplyResult, error) {
		applied = input
		return ApplyResult{BackupID: "unused"}, nil
	}

	body := []byte(`{"site_url":"https://gateway.example.com","model_context_window":1000000,"model_auto_compact_token_limit":900000}`)
	postHelperJSON(t, helper.routes(), "/api/site", helper.state, body, http.StatusOK)
	callbackBody := []byte(`{"base_url":"https://gateway.example.com/v1","api_key":"sk-test-1234567890","key_name":"Codex","provider_name":"Foreign relay"}`)
	postHelperJSON(t, helper.routes(), "/api/apply", helper.state, callbackBody, http.StatusOK)
	if applied.ModelContextWindow != 1000000 || applied.ModelAutoCompactTokenLimit != 900000 {
		t.Fatalf("callback applied context = %d/%d, want 1000000/900000", applied.ModelContextWindow, applied.ModelAutoCompactTokenLimit)
	}
	if !applied.ForceCanonicalProvider || applied.ProviderName != providerName {
		t.Fatalf("website-assisted apply did not force XIASS takeover: %+v", applied)
	}
	if helper.pendingContextSettings() != nil {
		t.Fatal("pending context settings were not cleared after a successful callback")
	}
}

func TestHelperManualApplyDoesNotForceCanonicalProvider(t *testing.T) {
	helper, err := newTestHelperServer(NewConfigManager(t.TempDir()), defaultXIASSAPIURL, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNO1")
	if err != nil {
		t.Fatal(err)
	}
	helper.detect = func() CodexInstallation {
		return CodexInstallation{Found: true, Running: true, AppPath: "/test/Codex.app"}
	}
	helper.stop = func(CodexInstallation) error { return nil }
	helper.start = func(CodexInstallation) error { return nil }
	helper.repairHistory = func() (HistoryRepairResult, error) { return HistoryRepairResult{}, nil }
	var applied ApplyConfig
	helper.applyConfig = func(input ApplyConfig) (ApplyResult, error) {
		applied = input
		return ApplyResult{BackupID: "unused"}, nil
	}

	body := []byte(`{"base_url":"https://custom.example.com/v1","api_key":"custom-key","provider_name":"Ignored by manual path"}`)
	postHelperJSON(t, helper.routes(), "/api/apply-manual", helper.state, body, http.StatusOK)
	if applied.ForceCanonicalProvider {
		t.Fatalf("manual configuration unexpectedly forced XIASS takeover: %+v", applied)
	}
	if applied.ProviderName != "Custom API" {
		t.Fatalf("manual provider name = %q, want Custom API", applied.ProviderName)
	}
}

func TestHelperDerivesCompactLimitWhenOnlyContextWindowIsProvided(t *testing.T) {
	helper, err := newTestHelperServer(NewConfigManager(t.TempDir()), defaultXIASSAPIURL, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNO1")
	if err != nil {
		t.Fatal(err)
	}
	settings, err := helper.resolveRequestedContext(ContextSettings{ModelContextWindow: 1000000})
	if err != nil {
		t.Fatal(err)
	}
	if settings.ModelAutoCompactTokenLimit != 900000 {
		t.Fatalf("derived site compact limit = %d, want 900000", settings.ModelAutoCompactTokenLimit)
	}

	helper.detect = func() CodexInstallation {
		return CodexInstallation{Found: true, Running: true, AppPath: "/test/Codex.app"}
	}
	helper.stop = func(CodexInstallation) error { return nil }
	helper.start = func(CodexInstallation) error { return nil }
	helper.repairHistory = func() (HistoryRepairResult, error) { return HistoryRepairResult{}, nil }
	var applied ApplyConfig
	helper.applyConfig = func(input ApplyConfig) (ApplyResult, error) {
		applied = input
		return ApplyResult{BackupID: "unused"}, nil
	}
	body, err := json.Marshal(map[string]any{
		"base_url":             defaultXIASSAPIURL + "/v1",
		"api_key":              "sk-test-1234567890",
		"key_name":             "Codex",
		"model_context_window": 512000,
	})
	if err != nil {
		t.Fatal(err)
	}
	postHelperJSON(t, helper.routes(), "/api/apply", helper.state, body, http.StatusOK)
	if applied.ModelAutoCompactTokenLimit != 460800 {
		t.Fatalf("derived callback compact limit = %d, want 460800", applied.ModelAutoCompactTokenLimit)
	}
}

func TestHelperCallbackContextOverridesPendingSelection(t *testing.T) {
	helper, err := newTestHelperServer(NewConfigManager(t.TempDir()), defaultXIASSAPIURL, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNO1")
	if err != nil {
		t.Fatal(err)
	}
	helper.detect = func() CodexInstallation {
		return CodexInstallation{Found: true, Running: true, AppPath: "/test/Codex.app"}
	}
	helper.stop = func(CodexInstallation) error { return nil }
	helper.start = func(CodexInstallation) error { return nil }
	helper.repairHistory = func() (HistoryRepairResult, error) { return HistoryRepairResult{}, nil }
	var applied ApplyConfig
	helper.applyConfig = func(input ApplyConfig) (ApplyResult, error) {
		applied = input
		return ApplyResult{BackupID: "unused"}, nil
	}
	helper.setPendingContext(ContextSettings{ModelContextWindow: 1000000, ModelAutoCompactTokenLimit: 900000})
	body, err := json.Marshal(map[string]any{
		"base_url":                       defaultXIASSAPIURL + "/v1",
		"api_key":                        "sk-test-1234567890",
		"key_name":                       "Codex",
		"model_context_window":           512000,
		"model_auto_compact_token_limit": 460800,
	})
	if err != nil {
		t.Fatal(err)
	}
	postHelperJSON(t, helper.routes(), "/api/apply", helper.state, body, http.StatusOK)
	if applied.ModelContextWindow != 512000 || applied.ModelAutoCompactTokenLimit != 460800 {
		t.Fatalf("explicit callback context = %d/%d, want 512000/460800", applied.ModelContextWindow, applied.ModelAutoCompactTokenLimit)
	}
}

func TestHelperServerSelectsCodexAppAtRuntime(t *testing.T) {
	helper, err := newTestHelperServer(NewConfigManager(t.TempDir()), defaultXIASSAPIURL, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNO1")
	if err != nil {
		t.Fatal(err)
	}
	helper.detect = func() CodexInstallation { return CodexInstallation{} }
	helper.selectApp = func() (CodexInstallation, error) {
		return CodexInstallation{
			AppPath:    `C:\Program Files\Codex`,
			Executable: `C:\Program Files\Codex\Codex.exe`,
			Found:      true,
		}, nil
	}
	handler := helper.routes()

	selected := postHelperJSON(t, handler, "/api/select-app", helper.state, []byte(`{}`), http.StatusOK)
	if ok, _ := selected["ok"].(bool); !ok {
		t.Fatalf("select app response = %+v", selected)
	}
	status := getJSON(t, handler, "/api/status")
	codex, _ := status["codex"].(map[string]any)
	if found, _ := codex["found"].(bool); !found {
		t.Fatalf("selected Codex app was not retained: %+v", status)
	}
	if codex["executable"] != `C:\Program Files\Codex\Codex.exe` {
		t.Fatalf("selected Codex executable = %v", codex["executable"])
	}
}

func TestHelperServerAcceptsPastedCodexAppPath(t *testing.T) {
	helper, err := newTestHelperServer(NewConfigManager(t.TempDir()), defaultXIASSAPIURL, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNO1")
	if err != nil {
		t.Fatal(err)
	}
	helper.detect = func() CodexInstallation { return CodexInstallation{} }
	var receivedPath string
	helper.selectAppPath = func(path string) (CodexInstallation, error) {
		receivedPath = path
		return CodexInstallation{
			AppPath:      "Microsoft Store / WindowsApps / OpenAI Codex (ChatGPT.exe)",
			LaunchTarget: `OpenAI.Codex_2p2nqsd0c76g0!App`,
			Found:        true,
		}, nil
	}
	handler := helper.routes()

	path := `C:\Program Files\WindowsApps\OpenAI.Codex_26.810.4967.0_x64__2p2nqsd0c76g0\app\ChatGPT.exe`
	body, err := json.Marshal(map[string]string{"path": path})
	if err != nil {
		t.Fatal(err)
	}
	selected := postHelperJSON(t, handler, "/api/select-app", helper.state, body, http.StatusOK)
	if receivedPath != path {
		t.Fatalf("pasted path = %q, want %q", receivedPath, path)
	}
	if ok, _ := selected["ok"].(bool); !ok {
		t.Fatalf("select pasted app response = %+v", selected)
	}
	status := getJSON(t, handler, "/api/status")
	codex, _ := status["codex"].(map[string]any)
	if codex["launch_target"] != `OpenAI.Codex_2p2nqsd0c76g0!App` {
		t.Fatalf("selected launch target = %v", codex["launch_target"])
	}
}

func TestHelperIndexRendersUsableSessionState(t *testing.T) {
	const state = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNO1"
	helper, err := newTestHelperServer(NewConfigManager(t.TempDir()), "", state)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Host = "127.0.0.1:43123"
	response := httptest.NewRecorder()
	helper.routes().ServeHTTP(response, request)
	body := response.Body.String()
	if !strings.Contains(body, `name="xiass-helper-state" content="`+state+`"`) {
		t.Fatal("helper session state was not rendered as a plain meta attribute")
	}
	if strings.Contains(body, `content="&`) || strings.Contains(body, `content="\&quot;`) {
		t.Fatal("helper session state contains an extra escaped quote layer")
	}
	if !strings.Contains(body, `value="`+defaultXIASSAPIURL+`"`) {
		t.Fatal("helper index does not render the default XIASS API URL")
	}
	if !strings.Contains(body, `id="manual-base-url"`) || !strings.Contains(body, `id="manual-api-key"`) {
		t.Fatal("helper index does not expose manual API configuration")
	}
	if !strings.Contains(body, `id="codex-app-path"`) || !strings.Contains(body, `id="use-app-path-button"`) {
		t.Fatal("helper index does not expose a pasteable Codex App path")
	}
	if !strings.Contains(body, `id="repair-history-button"`) || !strings.Contains(body, `id="history-backup-select"`) || !strings.Contains(body, `id="delete-history-backup-button"`) {
		t.Fatal("helper index does not expose history compatibility repair and recovery controls")
	}
	if !strings.Contains(body, `id="delete-backup-button"`) || !strings.Contains(body, `id="load-manual-models-button"`) || !strings.Contains(body, `id="manual-review-model"`) {
		t.Fatal("helper index does not expose backup cleanup and multi-model manual configuration controls")
	}
	if !strings.Contains(body, "334800") || !strings.Contains(body, "defaultCompactLimit") {
		t.Fatal("helper index does not expose the corrected 90% context defaults")
	}
	if strings.Contains(strings.ToLower(body), "codex++") {
		t.Fatal("helper index contains an unrelated product name")
	}
}

func TestHelperManualHistoryRepairStopsRepairsAndStarts(t *testing.T) {
	home := t.TempDir()
	writeHistoryConfig(t, home, "codex_local_access")
	session := writeHistoryRollout(t, home, "sessions/rollout-a.jsonl", "xiass", "thread-a")
	appendHistoryRecords(t, session, map[string]any{"type": "response_item", "payload": map[string]any{
		"type": "reasoning", "encrypted_content": "opaque", "summary": []any{},
	}})
	before, err := os.ReadFile(session)
	if err != nil {
		t.Fatal(err)
	}
	createHistoryDatabase(t, filepath.Join(home, "state_5.sqlite"), map[string]string{"thread-a": "xiass"})
	helper, err := newTestHelperServer(NewConfigManager(home), defaultXIASSAPIURL, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNO1")
	if err != nil {
		t.Fatal(err)
	}
	helper.detect = func() CodexInstallation {
		return CodexInstallation{Found: true, Running: true, AppPath: "/test/Codex.app"}
	}
	var stopped, started atomic.Int32
	helper.stop = func(CodexInstallation) error { stopped.Add(1); return nil }
	helper.start = func(CodexInstallation) error { started.Add(1); return nil }

	response := postHelperJSON(t, helper.routes(), "/api/repair-history", helper.state, []byte(`{}`), http.StatusOK)
	if ok, _ := response["ok"].(bool); !ok {
		t.Fatalf("repair response = %+v", response)
	}
	if stopped.Load() != 1 || started.Load() != 1 {
		t.Fatalf("lifecycle counts = stop %d, start %d", stopped.Load(), started.Load())
	}
	history, _ := response["history"].(map[string]any)
	backupID, _ := history["backup_id"].(string)
	if backupID == "" {
		t.Fatalf("repair did not return a restorable history backup: %+v", response)
	}
	if repaired, err := os.ReadFile(session); err != nil || bytes.Contains(repaired, []byte("encrypted_content")) {
		t.Fatalf("incompatible continuation was not removed: %v", err)
	}
	assertHistoryRolloutProvider(t, session, "codex_local_access")
	assertHistoryDatabase(t, filepath.Join(home, "state_5.sqlite"), 1, "codex_local_access")

	restoreBody, _ := json.Marshal(map[string]string{"backup_id": backupID})
	restore := postHelperJSON(t, helper.routes(), "/api/restore-history", helper.state, restoreBody, http.StatusOK)
	if ok, _ := restore["ok"].(bool); !ok {
		t.Fatalf("history restore response = %+v", restore)
	}
	if stopped.Load() != 2 || started.Load() != 2 {
		t.Fatalf("lifecycle counts after restore = stop %d, start %d", stopped.Load(), started.Load())
	}
	restored, err := os.ReadFile(session)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(restored, before) {
		t.Fatal("HTTP history restore did not restore the original conversation records")
	}
}

func TestHelperApplyRollsBackConfigWhenHistoryValidationFails(t *testing.T) {
	home := t.TempDir()
	manager := NewConfigManager(home)
	originalConfig := strings.Replace(testOriginalConfig, `model_provider = "official"`, `model_provider = "openai"`, 1)
	if err := os.WriteFile(manager.ConfigPath, []byte(originalConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "sqlite"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "sqlite", "state_5.sqlite"), []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	helper, err := newTestHelperServer(manager, "https://gateway.example.com", "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNO1")
	if err != nil {
		t.Fatal(err)
	}
	helper.detect = func() CodexInstallation {
		return CodexInstallation{Found: true, Running: true, AppPath: "/test/Codex.app"}
	}
	var stopped, started atomic.Int32
	helper.stop = func(CodexInstallation) error { stopped.Add(1); return nil }
	helper.start = func(CodexInstallation) error { started.Add(1); return nil }

	body := []byte(`{"base_url":"https://gateway.example.com","api_key":"sk-test-1234567890","key_name":"Codex"}`)
	response := postHelperJSON(t, helper.routes(), "/api/apply", helper.state, body, http.StatusInternalServerError)
	if ok, _ := response["ok"].(bool); ok {
		t.Fatalf("apply unexpectedly succeeded: %+v", response)
	}
	if stopped.Load() != 1 || started.Load() != 1 {
		t.Fatalf("lifecycle counts = stop %d, start %d", stopped.Load(), started.Load())
	}
	restored, err := os.ReadFile(manager.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(restored) != originalConfig {
		t.Fatal("configuration was not rolled back after history validation failed")
	}
}

func TestHelperRestoreMissingOriginalConfigAlignsConversationsWithOfficialProvider(t *testing.T) {
	home := t.TempDir()
	manager := NewConfigManager(home)
	session := writeHistoryRollout(t, home, "sessions/rollout-a.jsonl", "codex_local_access", "thread-a")
	databasePath := filepath.Join(home, "state_5.sqlite")
	createHistoryDatabase(t, databasePath, map[string]string{"thread-a": "codex_local_access"})
	helper, err := newTestHelperServer(manager, "https://gateway.example.com", "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNO1")
	if err != nil {
		t.Fatal(err)
	}
	helper.detect = func() CodexInstallation { return CodexInstallation{Found: true, AppPath: "/test/Codex.app"} }
	helper.stop = func(CodexInstallation) error { return nil }
	helper.start = func(CodexInstallation) error { return nil }
	handler := helper.routes()

	apply := postHelperJSON(t, handler, "/api/apply", helper.state, []byte(`{"base_url":"https://gateway.example.com","api_key":"sk-test-1234567890","key_name":"Codex"}`), http.StatusOK)
	backupID, _ := apply["backup_id"].(string)
	if backupID == "" {
		t.Fatal("apply did not return a backup ID")
	}
	restoreBody, _ := json.Marshal(map[string]string{"backup_id": backupID})
	postHelperJSON(t, handler, "/api/restore", helper.state, restoreBody, http.StatusOK)
	if _, err := os.Stat(manager.ConfigPath); !os.IsNotExist(err) {
		t.Fatalf("config.toml still exists after restoring a missing original: %v", err)
	}
	assertHistoryRolloutProvider(t, session, "openai")
	assertHistoryDatabase(t, databasePath, 1, "openai")
}

func TestHelperRestoreLegacyXIASSBackupUpgradesConfigAndHistoryForward(t *testing.T) {
	home := t.TempDir()
	manager := NewConfigManager(home)
	legacyConfig := `model_provider = "xiass"

[model_providers.xiass]
name = "XIASS API"
base_url = "https://gateway.example.com"
wire_api = "responses"
requires_openai_auth = false
experimental_bearer_token = "sk-test-1234567890"
`
	if err := os.WriteFile(manager.ConfigPath, []byte(legacyConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	session := writeHistoryRollout(t, home, "sessions/rollout-a.jsonl", "xiass", "thread-a")
	databasePath := filepath.Join(home, "state_5.sqlite")
	createHistoryDatabase(t, databasePath, map[string]string{"thread-a": "xiass"})
	apply, err := manager.Apply(ApplyConfig{BaseURL: "https://gateway.example.com", APIKey: "sk-new-1234567890"})
	if err != nil {
		t.Fatal(err)
	}
	helper, err := newTestHelperServer(manager, "https://gateway.example.com", "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNO1")
	if err != nil {
		t.Fatal(err)
	}
	helper.detect = func() CodexInstallation { return CodexInstallation{Found: true, AppPath: "/test/Codex.app"} }
	helper.stop = func(CodexInstallation) error { return nil }
	helper.start = func(CodexInstallation) error { return nil }
	body, _ := json.Marshal(map[string]string{"backup_id": apply.BackupID})
	postHelperJSON(t, helper.routes(), "/api/restore", helper.state, body, http.StatusOK)
	config, err := os.ReadFile(manager.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(config), `model_provider = "xiass"`) || !strings.Contains(string(config), `model_provider = "codex_local_access"`) {
		t.Fatal("restored legacy configuration was not upgraded forward")
	}
	assertHistoryRolloutProvider(t, session, "codex_local_access")
	assertHistoryDatabase(t, databasePath, 1, "codex_local_access")
}

func TestHelperRejectsConcurrentLifecycleOperationBeforeStoppingCodex(t *testing.T) {
	home := t.TempDir()
	writeHistoryConfig(t, home, "codex_local_access")
	helper, err := newTestHelperServer(NewConfigManager(home), defaultXIASSAPIURL, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNO1")
	if err != nil {
		t.Fatal(err)
	}
	helper.detect = func() CodexInstallation {
		return CodexInstallation{Found: true, Running: true, AppPath: "/test/Codex.app"}
	}
	var stopped atomic.Int32
	helper.stop = func(CodexInstallation) error { stopped.Add(1); return nil }
	helper.start = func(CodexInstallation) error { return nil }
	release, err := acquireLifecycleLock(home)
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	postHelperJSON(t, helper.routes(), "/api/repair-history", helper.state, []byte(`{}`), http.StatusConflict)
	if stopped.Load() != 0 {
		t.Fatal("Codex was stopped even though another helper held the lifecycle lock")
	}
}

func TestHelperRejectsDuplicateApplyWithoutQueueingRestart(t *testing.T) {
	helper, err := newTestHelperServer(NewConfigManager(t.TempDir()), defaultXIASSAPIURL, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNO1")
	if err != nil {
		t.Fatal(err)
	}
	helper.detect = func() CodexInstallation {
		return CodexInstallation{Found: true, Running: true, AppPath: "/test/Codex.app"}
	}
	var stopped atomic.Int32
	helper.stop = func(CodexInstallation) error { stopped.Add(1); return nil }
	helper.start = func(CodexInstallation) error { return nil }
	helper.operationMu.Lock()
	defer helper.operationMu.Unlock()

	body, err := json.Marshal(map[string]string{"base_url": defaultXIASSAPIURL, "api_key": "sk-test-1234567890"})
	if err != nil {
		t.Fatal(err)
	}
	postHelperJSON(t, helper.routes(), "/api/apply", helper.state, body, http.StatusConflict)
	if stopped.Load() != 0 {
		t.Fatal("duplicate apply request reached the Codex lifecycle")
	}
}

func TestHelperStopFailureChangesNothing(t *testing.T) {
	home := t.TempDir()
	manager := NewConfigManager(home)
	if err := os.WriteFile(manager.ConfigPath, []byte(testOriginalConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	session := writeHistoryRollout(t, home, "sessions/rollout-a.jsonl", "official", "thread-a")
	beforeSession, err := os.ReadFile(session)
	if err != nil {
		t.Fatal(err)
	}
	helper, err := newTestHelperServer(manager, "https://gateway.example.com", "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNO1")
	if err != nil {
		t.Fatal(err)
	}
	helper.detect = func() CodexInstallation {
		return CodexInstallation{Found: true, Running: true, AppPath: "/test/Codex.app"}
	}
	helper.stop = func(CodexInstallation) error { return errors.New("cannot verify process state") }
	var started atomic.Int32
	helper.start = func(CodexInstallation) error { started.Add(1); return nil }
	body := []byte(`{"base_url":"https://gateway.example.com","api_key":"sk-test-1234567890","key_name":"Codex"}`)
	postHelperJSON(t, helper.routes(), "/api/apply", helper.state, body, http.StatusInternalServerError)

	config, err := os.ReadFile(manager.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(config) != testOriginalConfig {
		t.Fatal("config changed after stop failure")
	}
	afterSession, err := os.ReadFile(session)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(beforeSession, afterSession) {
		t.Fatal("session changed after stop failure")
	}
	backups, err := manager.ListBackups()
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 0 || started.Load() != 0 {
		t.Fatalf("stop failure created backups or started Codex: backups=%d starts=%d", len(backups), started.Load())
	}
}

func TestHelperPrepareFailureChangesNothing(t *testing.T) {
	home := t.TempDir()
	manager := NewConfigManager(home)
	if err := os.WriteFile(manager.ConfigPath, []byte(testOriginalConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	helper, err := newTestHelperServer(manager, "https://gateway.example.com", "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNO1")
	if err != nil {
		t.Fatal(err)
	}
	helper.prepare = func() error { return errors.New("conflicting manager is still running") }
	var stopped, started atomic.Int32
	helper.stop = func(CodexInstallation) error { stopped.Add(1); return nil }
	helper.start = func(CodexInstallation) error { started.Add(1); return nil }

	body := []byte(`{"base_url":"https://gateway.example.com","api_key":"sk-test-1234567890","key_name":"Codex"}`)
	response := postHelperJSON(t, helper.routes(), "/api/apply", helper.state, body, http.StatusConflict)
	if stopped.Load() != 0 || started.Load() != 0 {
		t.Fatalf("prepare failure reached Codex lifecycle: stop=%d start=%d", stopped.Load(), started.Load())
	}
	written, err := os.ReadFile(manager.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(written) != testOriginalConfig {
		t.Fatal("configuration changed after prepare failure")
	}
	if message, _ := response["message"].(string); !strings.Contains(message, "第三方管理工具") {
		t.Fatalf("prepare failure response is unclear: %q", message)
	}
}

func TestHelperRetriesCodexLaunchAfterTransientFailure(t *testing.T) {
	helper, err := newTestHelperServer(NewConfigManager(t.TempDir()), defaultXIASSAPIURL, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNO1")
	if err != nil {
		t.Fatal(err)
	}
	installation := CodexInstallation{Found: true, AppPath: "/test/Codex.app"}
	helper.detect = func() CodexInstallation { return installation }
	var attempts atomic.Int32
	helper.start = func(CodexInstallation) error {
		if attempts.Add(1) == 1 {
			return errors.New("transient launch failure")
		}
		return nil
	}
	if err := helper.startWithRetry(installation); err != nil {
		t.Fatal(err)
	}
	if attempts.Load() != 2 {
		t.Fatalf("launch attempts = %d, want 2", attempts.Load())
	}
}

func TestHelperDoesNotStartCodexAfterHistoryRollbackFailure(t *testing.T) {
	home := t.TempDir()
	writeHistoryConfig(t, home, "codex_local_access")
	helper, err := newTestHelperServer(NewConfigManager(home), defaultXIASSAPIURL, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNO1")
	if err != nil {
		t.Fatal(err)
	}
	helper.detect = func() CodexInstallation {
		return CodexInstallation{Found: true, Running: true, AppPath: "/test/Codex.app"}
	}
	helper.stop = func(CodexInstallation) error { return nil }
	helper.repairCompatibilityHistory = func() (HistoryRepairResult, error) {
		return HistoryRepairResult{}, &HistoryRepairApplyError{
			Cause:       errors.New("forced repair failure"),
			RollbackErr: errors.New("forced rollback failure"),
		}
	}
	var started atomic.Int32
	helper.start = func(CodexInstallation) error { started.Add(1); return nil }
	response := postHelperJSON(t, helper.routes(), "/api/repair-history", helper.state, []byte(`{}`), http.StatusInternalServerError)
	if started.Load() != 0 {
		t.Fatal("Codex was started after history rollback failed")
	}
	message, _ := response["message"].(string)
	if !strings.Contains(message, "保持关闭") {
		t.Fatalf("unsafe rollback response is unclear: %q", message)
	}
}

func TestHelperDoesNotStartCodexAfterApplyConfigRollbackFailure(t *testing.T) {
	helper, err := newTestHelperServer(NewConfigManager(t.TempDir()), defaultXIASSAPIURL, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNO1")
	if err != nil {
		t.Fatal(err)
	}
	helper.detect = func() CodexInstallation {
		return CodexInstallation{Found: true, Running: true, AppPath: "/test/Codex.app"}
	}
	helper.stop = func(CodexInstallation) error { return nil }
	helper.applyConfig = func(ApplyConfig) (ApplyResult, error) {
		return ApplyResult{}, &ConfigMutationError{
			Cause:       errors.New("forced config failure"),
			RollbackErr: errors.New("forced rollback failure"),
		}
	}
	var started atomic.Int32
	helper.start = func(CodexInstallation) error { started.Add(1); return nil }
	body, err := json.Marshal(map[string]string{
		"base_url": defaultXIASSAPIURL,
		"api_key":  "sk-test-1234567890",
	})
	if err != nil {
		t.Fatal(err)
	}
	response := postHelperJSON(t, helper.routes(), "/api/apply", helper.state, body, http.StatusInternalServerError)
	if started.Load() != 0 {
		t.Fatal("Codex was started after apply configuration rollback failed")
	}
	if message, _ := response["message"].(string); !strings.Contains(message, "保持关闭") {
		t.Fatalf("unsafe apply rollback response is unclear: %q", message)
	}
}

func TestHelperDoesNotStartCodexAfterRestoreConfigRollbackFailure(t *testing.T) {
	helper, err := newTestHelperServer(NewConfigManager(t.TempDir()), defaultXIASSAPIURL, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNO1")
	if err != nil {
		t.Fatal(err)
	}
	helper.detect = func() CodexInstallation {
		return CodexInstallation{Found: true, Running: true, AppPath: "/test/Codex.app"}
	}
	helper.stop = func(CodexInstallation) error { return nil }
	helper.restoreConfig = func(string) (RestoreResult, error) {
		return RestoreResult{}, &ConfigMutationError{
			Cause:       errors.New("forced restore failure"),
			RollbackErr: errors.New("forced rollback failure"),
		}
	}
	var started atomic.Int32
	helper.start = func(CodexInstallation) error { started.Add(1); return nil }
	response := postHelperJSON(t, helper.routes(), "/api/restore", helper.state, []byte(`{"backup_id":"test-backup"}`), http.StatusInternalServerError)
	if started.Load() != 0 {
		t.Fatal("Codex was started after restore configuration rollback failed")
	}
	if message, _ := response["message"].(string); !strings.Contains(message, "保持关闭") {
		t.Fatalf("unsafe restore rollback response is unclear: %q", message)
	}
}

func TestLocalHTTPServerAllowsLongLifecycleOperations(t *testing.T) {
	server := newLocalHTTPServer(http.NotFoundHandler())
	if server.WriteTimeout < 2*time.Minute {
		t.Fatalf("WriteTimeout = %v, want at least 2 minutes", server.WriteTimeout)
	}
}

func TestHelperApplyRestoresOriginalStateWhenNewStateCannotStart(t *testing.T) {
	home := t.TempDir()
	manager := NewConfigManager(home)
	if err := os.WriteFile(manager.ConfigPath, []byte(testOriginalConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	helper, err := newTestHelperServer(manager, "https://gateway.example.com", "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNO1")
	if err != nil {
		t.Fatal(err)
	}
	helper.detect = func() CodexInstallation {
		return CodexInstallation{Found: true, Running: true, AppPath: "/test/Codex.app"}
	}
	helper.stop = func(CodexInstallation) error { return nil }
	var starts atomic.Int32
	helper.start = func(CodexInstallation) error {
		if starts.Add(1) <= 2 {
			return errors.New("new state cannot start")
		}
		return nil
	}
	body := []byte(`{"base_url":"https://gateway.example.com","api_key":"sk-test-1234567890","key_name":"Codex"}`)
	response := postHelperJSON(t, helper.routes(), "/api/apply", helper.state, body, http.StatusInternalServerError)
	if restarted, _ := response["restarted"].(bool); !restarted {
		t.Fatalf("original safe state was not restarted: %+v", response)
	}
	config, err := os.ReadFile(manager.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(config) != testOriginalConfig {
		t.Fatal("original config was not restored after two launch failures")
	}
}

func TestHelperRestoreReturnsToPreRestoreStateWhenSelectedStateCannotStart(t *testing.T) {
	home := t.TempDir()
	manager := NewConfigManager(home)
	if err := os.WriteFile(manager.ConfigPath, []byte(testOriginalConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	apply, err := manager.Apply(ApplyConfig{BaseURL: "https://gateway.example.com", APIKey: "sk-current-1234567890"})
	if err != nil {
		t.Fatal(err)
	}
	preRestore, err := os.ReadFile(manager.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	helper, err := newTestHelperServer(manager, "https://gateway.example.com", "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNO1")
	if err != nil {
		t.Fatal(err)
	}
	helper.detect = func() CodexInstallation {
		return CodexInstallation{Found: true, Running: true, AppPath: "/test/Codex.app"}
	}
	helper.stop = func(CodexInstallation) error { return nil }
	var starts atomic.Int32
	helper.start = func(CodexInstallation) error {
		if starts.Add(1) <= 2 {
			return errors.New("selected restore state cannot start")
		}
		return nil
	}
	body, _ := json.Marshal(map[string]string{"backup_id": apply.BackupID})
	response := postHelperJSON(t, helper.routes(), "/api/restore", helper.state, body, http.StatusInternalServerError)
	if restarted, _ := response["restarted"].(bool); !restarted {
		t.Fatalf("pre-restore safe state was not restarted: %+v", response)
	}
	config, err := os.ReadFile(manager.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(config, preRestore) {
		t.Fatal("pre-restore config was not restored after two launch failures")
	}
}

func TestHelperApplyLaunchFailureRestoresLegacyHistorySnapshot(t *testing.T) {
	home := t.TempDir()
	manager := NewConfigManager(home)
	legacyConfig := `model_provider = "xiass"

[model_providers.xiass]
name = "XIASS API"
base_url = "https://gateway.example.com"
wire_api = "responses"
requires_openai_auth = false
experimental_bearer_token = "sk-old-1234567890"
`
	if err := os.WriteFile(manager.ConfigPath, []byte(legacyConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	session := writeHistoryRollout(t, home, "sessions/rollout-a.jsonl", "xiass", "thread-a")
	databasePath := filepath.Join(home, "state_5.sqlite")
	createHistoryDatabase(t, databasePath, map[string]string{"thread-a": "xiass"})
	helper, err := newTestHelperServer(manager, "https://gateway.example.com", "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNO1")
	if err != nil {
		t.Fatal(err)
	}
	helper.detect = func() CodexInstallation {
		return CodexInstallation{Found: true, Running: true, AppPath: "/test/Codex.app"}
	}
	helper.stop = func(CodexInstallation) error { return nil }
	var starts atomic.Int32
	helper.start = func(CodexInstallation) error {
		if starts.Add(1) <= 2 {
			return errors.New("new state cannot start")
		}
		return nil
	}
	body := []byte(`{"base_url":"https://gateway.example.com","api_key":"sk-new-1234567890","key_name":"Codex"}`)
	postHelperJSON(t, helper.routes(), "/api/apply", helper.state, body, http.StatusInternalServerError)
	config, err := os.ReadFile(manager.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(config) != legacyConfig {
		t.Fatal("legacy config was not restored after launch failure")
	}
	assertHistoryRolloutProvider(t, session, "xiass")
	assertHistoryDatabase(t, databasePath, 1, "xiass")
}

func TestHelperNeverRollsBackWhileCodexExitCannotBeConfirmed(t *testing.T) {
	home := t.TempDir()
	manager := NewConfigManager(home)
	legacyConfig := `model_provider = "xiass"

[model_providers.xiass]
name = "XIASS API"
base_url = "https://gateway.example.com"
wire_api = "responses"
requires_openai_auth = false
experimental_bearer_token = "sk-old-1234567890"
`
	if err := os.WriteFile(manager.ConfigPath, []byte(legacyConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	session := writeHistoryRollout(t, home, "sessions/rollout-a.jsonl", "xiass", "thread-a")
	databasePath := filepath.Join(home, "state_5.sqlite")
	createHistoryDatabase(t, databasePath, map[string]string{"thread-a": "xiass"})
	helper, err := newTestHelperServer(manager, "https://gateway.example.com", "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNO1")
	if err != nil {
		t.Fatal(err)
	}
	helper.detect = func() CodexInstallation {
		return CodexInstallation{Found: true, Running: true, AppPath: "/test/Codex.app"}
	}
	var stops atomic.Int32
	helper.stop = func(CodexInstallation) error {
		if stops.Add(1) == 1 {
			return nil
		}
		return errors.New("cannot confirm Codex exited")
	}
	helper.start = func(CodexInstallation) error { return errors.New("launch detection failed") }
	body := []byte(`{"base_url":"https://gateway.example.com","api_key":"sk-new-1234567890","key_name":"Codex"}`)
	response := postHelperJSON(t, helper.routes(), "/api/apply", helper.state, body, http.StatusInternalServerError)
	message, _ := response["message"].(string)
	if !strings.Contains(message, "未执行回滚") {
		t.Fatalf("unsafe rollback refusal is unclear: %q", message)
	}
	config, err := os.ReadFile(manager.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(config), `model_provider = "codex_local_access"`) {
		t.Fatal("new aligned config was unexpectedly rolled back while Codex might be running")
	}
	assertHistoryRolloutProvider(t, session, "codex_local_access")
	assertHistoryDatabase(t, databasePath, 1, "codex_local_access")
}

func getJSON(t *testing.T, handler http.Handler, target string) map[string]any {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, target, nil)
	request.Host = "127.0.0.1:43123"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var payload map[string]any
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	return payload
}

func postHelperJSON(t *testing.T, handler http.Handler, target, state string, body []byte, wantStatus int) map[string]any {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, target, bytes.NewReader(body))
	request.Host = "127.0.0.1:43123"
	request.Header.Set("Content-Type", "application/json")
	if state != "" {
		request.Header.Set("X-XIASS-Helper-State", state)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var payload map[string]any
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if response.Code != wantStatus {
		t.Fatalf("status = %d, want %d, payload = %+v", response.Code, wantStatus, payload)
	}
	return payload
}

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

const testOriginalConfig = `model_provider = "official"
model = "gpt-old"
model_reasoning_effort = "ultra"
web_search = "cached"

[mcp_servers.example]
command = "example-mcp"

[desktop]
appearanceTheme = "system"

[model_providers.official]
name = "Official"
base_url = "https://example.com"
wire_api = "responses"
requires_openai_auth = true
`

func TestApplyAndRestorePreservesOriginalConfig(t *testing.T) {
	home := t.TempDir()
	manager := NewConfigManager(home)
	if err := os.WriteFile(manager.ConfigPath, []byte(testOriginalConfig), 0o600); err != nil {
		t.Fatal(err)
	}

	input := ApplyConfig{
		BaseURL: "https://gateway.example.com/v1/",
		APIKey:  "sk-test-1234567890",
		KeyName: "Codex",
	}
	result, err := manager.Apply(input)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if result.BackupID == "" || result.ConfigSHA == "" {
		t.Fatalf("Apply() result is incomplete: %+v", result)
	}

	written, err := os.ReadFile(manager.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(written)
	for _, preserved := range []string{
		`model_reasoning_effort = "ultra"`,
		`[mcp_servers.example]`,
		`command = "example-mcp"`,
		`[desktop]`,
		`[model_providers.official]`,
	} {
		if !strings.Contains(text, preserved) {
			t.Errorf("updated config did not preserve %q", preserved)
		}
	}
	if count := strings.Count(text, "[model_providers.official]"); count != 1 {
		t.Fatalf("XIASS provider count = %d, want 1", count)
	}
	if !strings.Contains(text, `model_provider = "official"`) {
		t.Fatal("existing custom provider ID was not preserved")
	}
	if !strings.Contains(text, `http_headers = { "x-openai-actor-authorization" = "gateway.example.com" }`) {
		t.Fatal("actor authorization header does not match the working XIASS Codex configuration")
	}
	if strings.Contains(text, `x-openai-actor-authorization" = "https://`) {
		t.Fatal("actor authorization header must contain the XIASS hostname, not a URL")
	}
	if err := verifyManagedConfig(written, ApplyConfig{BaseURL: "https://gateway.example.com/v1", APIKey: input.APIKey}, "official"); err != nil {
		t.Fatalf("written config verification failed: %v", err)
	}

	backupBytes, err := os.ReadFile(manager.originalPath(result.BackupID))
	if err != nil {
		t.Fatal(err)
	}
	if string(backupBytes) != testOriginalConfig {
		t.Fatal("backup is not byte-for-byte identical to the original config")
	}

	restore, err := manager.Restore(result.BackupID)
	if err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	if restore.SafetyBackupID == "" {
		t.Fatal("Restore() did not create a pre-restore safety backup")
	}
	restored, err := os.ReadFile(manager.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(restored) != testOriginalConfig {
		t.Fatal("restored config is not byte-for-byte identical to the original")
	}
}

func TestDeleteBackupRemovesOnlyItsValidatedManagedDirectory(t *testing.T) {
	home := t.TempDir()
	manager := NewConfigManager(home)
	if err := os.WriteFile(manager.ConfigPath, []byte(testOriginalConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := manager.Apply(ApplyConfig{BaseURL: "https://gateway.example.com", APIKey: "sk-test-1234567890"})
	if err != nil {
		t.Fatal(err)
	}
	backupDirectory := filepath.Join(manager.BackupRoot, result.BackupID)
	if _, err := os.Stat(backupDirectory); err != nil {
		t.Fatalf("backup missing before deletion: %v", err)
	}
	if err := manager.DeleteBackup(result.BackupID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(backupDirectory); !os.IsNotExist(err) {
		t.Fatalf("backup still exists after deletion: %v", err)
	}
	backups, err := manager.ListBackups()
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 0 {
		t.Fatalf("deleted backup is still listed: %+v", backups)
	}
	if err := manager.DeleteBackup("../outside"); err == nil {
		t.Fatal("backup deletion accepted a traversal ID")
	}
}

func TestNormalizeApplyConfigSupportsHTTPSAndLoopbackHTTP(t *testing.T) {
	tests := map[string]string{
		"https":                   "https://gateway.example.com/v1",
		"https root":              "https://gateway.example.com",
		"localhost":               "http://localhost:54843/v1",
		"loopback IPv4":           "http://127.0.0.1:54843/V1",
		"loopback IPv4 no scheme": "127.0.0.1:54843/V1",
		"loopback IPv6":           "http://[::1]:54843/v1",
	}
	wants := map[string]string{
		"https":                   "https://gateway.example.com/v1",
		"https root":              "https://gateway.example.com/v1",
		"localhost":               "http://localhost:54843/v1",
		"loopback IPv4":           "http://127.0.0.1:54843/V1",
		"loopback IPv4 no scheme": "http://127.0.0.1:54843/V1",
		"loopback IPv6":           "http://[::1]:54843/v1",
	}
	for name, baseURL := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := normalizeApplyConfig(ApplyConfig{BaseURL: baseURL + "/", APIKey: "local-key"})
			if err != nil {
				t.Fatal(err)
			}
			if got.BaseURL != wants[name] {
				t.Fatalf("base URL = %q, want %q", got.BaseURL, wants[name])
			}
			if got.Model != defaultModel || got.ProviderName != providerName {
				t.Fatalf("defaults = model %q, provider %q", got.Model, got.ProviderName)
			}
		})
	}
}

func TestNormalizeApplyConfigRejectsRemoteHTTP(t *testing.T) {
	for _, baseURL := range []string{
		"http://gateway.example.com/v1",
		"http://localhost.example.com:54843/v1",
		"ftp://127.0.0.1:54843/v1",
	} {
		if _, err := normalizeApplyConfig(ApplyConfig{BaseURL: baseURL, APIKey: "local-key"}); err == nil {
			t.Fatalf("unsafe base URL was accepted: %s", baseURL)
		}
	}
}

func TestNormalizeContextSettingsSupportsPresetsAndRejectsUnsafeValues(t *testing.T) {
	defaults, err := normalizeContextSettings(ContextSettings{})
	if err != nil {
		t.Fatal(err)
	}
	if defaults.ModelContextWindow != 372000 || defaults.ModelAutoCompactTokenLimit != 334800 {
		t.Fatalf("default context settings = %+v, want 372000/334800", defaults)
	}

	large, err := normalizeContextSettings(ContextSettings{
		ModelContextWindow:         1000000,
		ModelAutoCompactTokenLimit: 900000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if large.ModelContextWindow != 1000000 || large.ModelAutoCompactTokenLimit != 900000 {
		t.Fatalf("large context settings = %+v", large)
	}

	derived, err := normalizeContextSettings(ContextSettings{ModelContextWindow: 512000})
	if err != nil {
		t.Fatal(err)
	}
	if derived.ModelAutoCompactTokenLimit != 460800 {
		t.Fatalf("derived compact limit = %d, want 460800", derived.ModelAutoCompactTokenLimit)
	}

	for _, test := range []ContextSettings{
		{ModelContextWindow: 63999, ModelAutoCompactTokenLimit: 50000},
		{ModelContextWindow: 1050001, ModelAutoCompactTokenLimit: 900000},
		{ModelContextWindow: 1000000, ModelAutoCompactTokenLimit: 1000001},
		{ModelContextWindow: 1000000, ModelAutoCompactTokenLimit: 15999},
	} {
		if _, err := normalizeContextSettings(test); err == nil {
			t.Fatalf("unsafe context settings were accepted: %+v", test)
		}
	}
}

func TestApplyWritesAndReadsSelectedContextSettings(t *testing.T) {
	manager := NewConfigManager(t.TempDir())
	input := ApplyConfig{
		BaseURL:                    "https://gateway.example.com",
		APIKey:                     "sk-test-1234567890",
		ModelContextWindow:         1000000,
		ModelAutoCompactTokenLimit: 900000,
	}
	if _, err := manager.Apply(input); err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(manager.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(written)
	for _, expected := range []string{
		"model_context_window = 1000000",
		"model_auto_compact_token_limit = 900000",
	} {
		if !strings.Contains(text, expected) {
			t.Errorf("selected context setting is missing %q", expected)
		}
	}
	settings, err := manager.ReadContextSettings()
	if err != nil {
		t.Fatal(err)
	}
	if settings.ModelContextWindow != input.ModelContextWindow || settings.ModelAutoCompactTokenLimit != input.ModelAutoCompactTokenLimit {
		t.Fatalf("read context settings = %+v, want %+v", settings, ContextSettings{
			ModelContextWindow:         input.ModelContextWindow,
			ModelAutoCompactTokenLimit: input.ModelAutoCompactTokenLimit,
		})
	}
}

func TestApplyWritesLocalModelCatalogAndUsesOfficialReviewDefault(t *testing.T) {
	home := t.TempDir()
	writeSyntheticNativeCache(t, home)
	manager := NewConfigManager(home)
	if err := os.WriteFile(manager.ConfigPath, []byte("review_model = \"old-review\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := manager.Apply(ApplyConfig{
		BaseURL:            "https://gateway.example.com",
		APIKey:             "sk-test-1234567890",
		Model:              "gpt-6-astra",
		ReviewModel:        "gpt-5.6-sol",
		ModelCatalogModels: []string{"gpt-5.6-sol", "gpt-6-astra", "gpt-5.6-sol"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.CatalogSHA == "" {
		t.Fatal("Apply() did not return a model catalog checksum")
	}
	config, err := os.ReadFile(manager.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(config), "review_model") {
		t.Fatal("old review_model was not removed")
	}
	catalogPath := configuredCatalogPath(t, config)
	if !strings.HasPrefix(catalogPath, manager.ModelCatalogRoot+string(os.PathSeparator)+"model-catalog-") {
		t.Fatal("config.toml does not point Codex at the local model catalog")
	}
	catalog, err := os.ReadFile(catalogPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateModelCatalog(catalog); err != nil {
		t.Fatal(err)
	}
	var parsed modelCatalogFile
	if err := json.Unmarshal(catalog, &parsed); err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(parsed.Models))
	for _, model := range parsed.Models {
		ids = append(ids, model.Slug)
	}
	if got := strings.Join(ids, ","); got != "gpt-6-astra,gpt-5.6-sol" {
		t.Fatalf("catalog model IDs = %q", got)
	}
	if strings.Contains(string(catalog), "sk-test-1234567890") || strings.Contains(string(catalog), "gateway.example.com") {
		t.Fatal("model catalog contains provider credentials or URL")
	}
	var configRoot map[string]any
	if err := toml.Unmarshal(config, &configRoot); err != nil {
		t.Fatal(err)
	}
	if configRoot["web_search"] != "live" {
		t.Fatalf("web_search = %v, want live", configRoot["web_search"])
	}
	byID := make(map[string]modelCatalogModel, len(parsed.Models))
	for _, model := range parsed.Models {
		byID[model.Slug] = model
	}
	for _, id := range []string{"gpt-6-astra", "gpt-5.6-sol"} {
		model, ok := byID[id]
		if !ok || !modelCatalogContains(model.InputModalities, "image") || !model.SupportsImageDetailOriginal || !model.SupportsSearchTool {
			t.Fatalf("%s lost image/search capabilities: %+v", id, model)
		}
	}
}

func TestModelCatalogKeepsBuiltInImageModelCapabilities(t *testing.T) {
	data, err := buildModelCatalogJSON(
		"https://gateway.example.com/v1",
		"gpt-5.6-sol",
		[]string{"gpt-image-2", "gpt-5.6-sol"},
		syntheticNativeDescriptors(t),
	)
	if err != nil {
		t.Fatal(err)
	}
	var catalog modelCatalogFile
	if err := json.Unmarshal(data, &catalog); err != nil {
		t.Fatal(err)
	}
	for _, model := range catalog.Models {
		if model.Slug != "gpt-image-2" {
			continue
		}
		if !modelCatalogContains(model.InputModalities, "image") || !model.SupportsImageDetailOriginal {
			t.Fatalf("built-in image model capabilities = %+v", model)
		}
		return
	}
	t.Fatal("built-in image model was missing from the generated catalog")
}

func modelCatalogContains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func TestRestoreRestoresModelCatalogTogetherWithConfig(t *testing.T) {
	manager := NewConfigManager(t.TempDir())
	writeSyntheticNativeCache(t, filepath.Dir(manager.ConfigPath))
	if _, err := manager.Apply(ApplyConfig{
		BaseURL:            "https://gateway.example.com",
		APIKey:             "sk-first-1234567890",
		Model:              "gpt-5.6-sol",
		ModelCatalogModels: []string{"gpt-5.6-sol"},
	}); err != nil {
		t.Fatal(err)
	}
	firstConfig, err := os.ReadFile(manager.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	firstCatalogPath := configuredCatalogPath(t, firstConfig)
	firstCatalog, err := os.ReadFile(firstCatalogPath)
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.Apply(ApplyConfig{
		BaseURL:            "https://gateway.example.com",
		APIKey:             "sk-second-1234567890",
		Model:              "gpt-6-astra",
		ModelCatalogModels: []string{"gpt-6-astra"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Restore(second.BackupID); err != nil {
		t.Fatal(err)
	}
	restoredConfig, err := os.ReadFile(manager.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	restoredCatalogPath := configuredCatalogPath(t, restoredConfig)
	restoredCatalog, err := os.ReadFile(restoredCatalogPath)
	if err != nil {
		t.Fatal(err)
	}
	if restoredCatalogPath != firstCatalogPath || !bytes.Equal(restoredConfig, firstConfig) || !bytes.Equal(restoredCatalog, firstCatalog) {
		t.Fatal("restoring a configuration backup did not restore its model catalog")
	}
}

func configuredCatalogPath(t *testing.T, config []byte) string {
	t.Helper()
	var root map[string]any
	if err := toml.Unmarshal(config, &root); err != nil {
		t.Fatal(err)
	}
	path, _ := root["model_catalog_json"].(string)
	if strings.TrimSpace(path) == "" {
		t.Fatal("model_catalog_json is missing")
	}
	return path
}

func TestReadContextSettingsUsesCompatibilityDefaultsWhenAbsent(t *testing.T) {
	manager := NewConfigManager(t.TempDir())
	settings, err := manager.ReadContextSettings()
	if err != nil {
		t.Fatal(err)
	}
	if settings != (ContextSettings{
		ModelContextWindow:         defaultContextWindow,
		ModelAutoCompactTokenLimit: defaultAutoCompactTokenLimit,
	}) {
		t.Fatalf("default context settings = %+v", settings)
	}
}

func TestApplySupportsCustomProviderAndModel(t *testing.T) {
	manager := NewConfigManager(t.TempDir())
	input := ApplyConfig{
		BaseURL:      "http://127.0.0.1:54843/V1",
		APIKey:       "local-key",
		Model:        "local-codex-model",
		ReviewModel:  "local-review-model",
		ProviderName: "Custom API",
	}
	if _, err := manager.Apply(input); err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(manager.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(written)
	for _, expected := range []string{
		`model = "local-codex-model"`,
		`name = "Custom API"`,
		`base_url = "http://127.0.0.1:54843/V1"`,
		`experimental_bearer_token = "local-key"`,
		`http_headers = { "x-openai-actor-authorization" = "127.0.0.1" }`,
	} {
		if !strings.Contains(text, expected) {
			t.Errorf("custom config is missing %q", expected)
		}
	}
	if strings.Contains(text, "review_model") {
		t.Fatal("custom config persisted review_model instead of using Codex's official default")
	}
}

func TestVerifyManagedConfigAllowsProviderDisplayNameVariation(t *testing.T) {
	input := ApplyConfig{
		BaseURL:      "https://gateway.example.com/v1",
		APIKey:       "sk-test-1234567890",
		Model:        "gpt-5.6-sol",
		ReviewModel:  "gpt-5.6-sol",
		ProviderName: "XIASS API",
	}
	var err error
	input, err = normalizeApplyConfig(input)
	if err != nil {
		t.Fatal(err)
	}
	written := string(patchConfig(nil, input, providerID))
	withLegacyLabel := []byte(strings.Replace(written, `name = "XIASS API"`, `name = "Customer's existing provider"`, 1))
	if err := verifyManagedConfig(withLegacyLabel, input, providerID); err != nil {
		t.Fatalf("display-label variation should not reject a valid configuration: %v", err)
	}

	withoutLabel := []byte(strings.Replace(written, `name = "XIASS API"`, `name = ""`, 1))
	if err := verifyManagedConfig(withoutLabel, input, providerID); err == nil {
		t.Fatal("missing provider display name was accepted")
	}
}

func TestApplyIsIdempotent(t *testing.T) {
	manager := NewConfigManager(t.TempDir())
	input := ApplyConfig{BaseURL: "https://gateway.example.com", APIKey: "sk-test-1234567890"}
	if _, err := manager.Apply(input); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Apply(input); err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(manager.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if count := strings.Count(string(written), "[model_providers.codex_local_access]"); count != 1 {
		t.Fatalf("XIASS provider count after repeated apply = %d, want 1", count)
	}
}

func TestApplyRemovesLegacyXIASSProviderSection(t *testing.T) {
	manager := NewConfigManager(t.TempDir())
	original := `model_provider = "xiass"

[model_providers.xiass]
name = "XIASS API"
base_url = "https://old.example.com"
wire_api = "responses"
requires_openai_auth = false
experimental_bearer_token = "old-secret"
`
	if err := os.WriteFile(manager.ConfigPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Apply(ApplyConfig{BaseURL: "https://gateway.example.com", APIKey: "sk-test-1234567890"}); err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(manager.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(written)
	if strings.Contains(text, "[model_providers.xiass]") || strings.Contains(text, "old-secret") {
		t.Fatal("legacy XIASS provider section was not removed")
	}
	if strings.Count(text, "[model_providers.codex_local_access]") != 1 {
		t.Fatal("new stable provider section is missing")
	}
}

func TestApplyForceCanonicalProviderTakesOverForeignConfiguration(t *testing.T) {
	manager := NewConfigManager(t.TempDir())
	original := `model_provider = "foreign-relay"
model = "foreign-model"
review_model = "foreign-review"
model_reasoning_effort = "high"
model_context_window = 1000000
model_auto_compact_token_limit = 900000
web_search = "cached"

[features]
goals = true

[mcp_servers.customer]
command = "customer-mcp"

[model_providers."foreign-relay"]
name = "Customer relay"
base_url = "https://relay.example.com/v1"
wire_api = "responses"
requires_openai_auth = false
experimental_bearer_token = "foreign-secret"

[model_providers.codex_local_access]
name = "Old XIASS label"
base_url = "https://old.example.com/v1"
wire_api = "responses"
requires_openai_auth = false
experimental_bearer_token = "old-secret"
`
	if err := os.WriteFile(manager.ConfigPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	input := ApplyConfig{
		BaseURL:                    "https://gateway.example.com/v1",
		APIKey:                     "sk-xiass-1234567890",
		Model:                      "gpt-5.6-sol",
		ReviewModel:                "gpt-5.6-sol",
		ModelContextWindow:         512000,
		ModelAutoCompactTokenLimit: 460800,
		ProviderName:               providerName,
		ForceCanonicalProvider:     true,
	}
	result, err := manager.Apply(input)
	if err != nil {
		t.Fatal(err)
	}
	if result.ProviderID != providerID {
		t.Fatalf("provider ID = %q, want %q", result.ProviderID, providerID)
	}

	written, err := os.ReadFile(manager.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyManagedConfig(written, input, providerID); err != nil {
		t.Fatalf("canonical XIASS config verification failed: %v", err)
	}
	var root map[string]any
	if err := toml.Unmarshal(written, &root); err != nil {
		t.Fatal(err)
	}
	if root["model_provider"] != providerID {
		t.Fatalf("active provider = %q, want %q", root["model_provider"], providerID)
	}
	if root["model_reasoning_effort"] != "high" {
		t.Fatalf("unrelated top-level setting was not preserved: %#v", root["model_reasoning_effort"])
	}
	features, ok := root["features"].(map[string]any)
	if !ok || features["goals"] != true {
		t.Fatalf("features were not preserved: %#v", root["features"])
	}
	mcpServers, ok := root["mcp_servers"].(map[string]any)
	if !ok {
		t.Fatalf("MCP servers were not preserved: %#v", root["mcp_servers"])
	}
	customerMCP, ok := mcpServers["customer"].(map[string]any)
	if !ok || customerMCP["command"] != "customer-mcp" {
		t.Fatalf("customer MCP server was not preserved: %#v", mcpServers)
	}
	providers, ok := root["model_providers"].(map[string]any)
	if !ok {
		t.Fatal("model providers table missing")
	}
	foreign, ok := providers["foreign-relay"].(map[string]any)
	if !ok || foreign["base_url"] != "https://relay.example.com/v1" {
		t.Fatalf("inactive foreign provider was not preserved: %#v", providers["foreign-relay"])
	}
	canonical, ok := providers[providerID].(map[string]any)
	if !ok || canonical["base_url"] != input.BaseURL || canonical["experimental_bearer_token"] != input.APIKey {
		t.Fatalf("canonical XIASS provider = %#v", canonical)
	}

	manifest, err := manager.readManifest(result.BackupID)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Reason != "force_apply" {
		t.Fatalf("backup reason = %q, want force_apply", manifest.Reason)
	}
	backup, err := os.ReadFile(manager.originalPath(result.BackupID))
	if err != nil {
		t.Fatal(err)
	}
	if string(backup) != original {
		t.Fatal("force apply did not retain the exact original configuration backup")
	}
}

func TestApplyForceCanonicalProviderRecoversInvalidExistingConfig(t *testing.T) {
	manager := NewConfigManager(t.TempDir())
	original := []byte("[broken\nvalue = true\n")
	if err := os.WriteFile(manager.ConfigPath, original, 0o600); err != nil {
		t.Fatal(err)
	}
	input := ApplyConfig{
		BaseURL:                "https://gateway.example.com/v1",
		APIKey:                 "sk-xiass-1234567890",
		ForceCanonicalProvider: true,
	}
	result, err := manager.Apply(input)
	if err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(manager.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyManagedConfig(written, input, providerID); err != nil {
		t.Fatalf("recovered config verification failed: %v", err)
	}
	manifest, err := manager.readManifest(result.BackupID)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Reason != "force_apply_invalid_config" {
		t.Fatalf("backup reason = %q, want force_apply_invalid_config", manifest.Reason)
	}
	backup, err := os.ReadFile(manager.originalPath(result.BackupID))
	if err != nil {
		t.Fatal(err)
	}
	if string(backup) != string(original) {
		t.Fatal("invalid original configuration was not retained exactly in the backup")
	}
}

func TestUpgradeLegacyProviderReusesConnectionUnderStableID(t *testing.T) {
	manager := NewConfigManager(t.TempDir())
	original := `model_provider = "xiass"
model_context_window = 1000000
model_auto_compact_token_limit = 900000

[model_providers.xiass]
name = "XIASS API"
base_url = "https://gateway.example.com"
wire_api = "responses"
requires_openai_auth = false
experimental_bearer_token = "sk-test-1234567890"
`
	if err := os.WriteFile(manager.ConfigPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	result, upgraded, err := manager.UpgradeLegacyProvider()
	if err != nil {
		t.Fatal(err)
	}
	if !upgraded || result.ProviderID != providerID {
		t.Fatalf("legacy upgrade result = upgraded %v, result %+v", upgraded, result)
	}
	written, err := os.ReadFile(manager.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(written), "[model_providers.xiass]") || !strings.Contains(string(written), "[model_providers.codex_local_access]") {
		t.Fatal("legacy provider was not upgraded to the stable provider ID")
	}
	for _, expected := range []string{
		"model_context_window = 1000000",
		"model_auto_compact_token_limit = 900000",
	} {
		if !strings.Contains(string(written), expected) {
			t.Errorf("legacy migration did not preserve %q", expected)
		}
	}
}

func TestApplyRefusesInvalidExistingConfig(t *testing.T) {
	manager := NewConfigManager(t.TempDir())
	original := []byte("[broken\nvalue = true\n")
	if err := os.WriteFile(manager.ConfigPath, original, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := manager.Apply(ApplyConfig{BaseURL: "https://gateway.example.com", APIKey: "sk-test-1234567890"})
	if err == nil || !strings.Contains(err.Error(), "existing config.toml is invalid") {
		t.Fatalf("Apply() error = %v, want invalid existing config error", err)
	}
	after, readErr := os.ReadFile(manager.ConfigPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(after) != string(original) {
		t.Fatal("invalid existing config was modified")
	}
}

func TestRestoreRejectsCorruptBackup(t *testing.T) {
	manager := NewConfigManager(t.TempDir())
	if err := os.WriteFile(manager.ConfigPath, []byte(testOriginalConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := manager.Apply(ApplyConfig{BaseURL: "https://gateway.example.com", APIKey: "sk-test-1234567890"})
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(manager.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manager.originalPath(result.BackupID), []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Restore(result.BackupID); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("Restore() error = %v, want checksum mismatch", err)
	}
	after, err := os.ReadFile(manager.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("current config changed after corrupt backup restore attempt")
	}
}

func TestRestoreRemovesConfigCreatedByHelper(t *testing.T) {
	manager := NewConfigManager(t.TempDir())
	result, err := manager.Apply(ApplyConfig{BaseURL: "https://gateway.example.com", APIKey: "sk-test-1234567890"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Restore(result.BackupID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(manager.ConfigPath); !os.IsNotExist(err) {
		t.Fatalf("config path still exists after restoring non-existent original: %v", err)
	}
	if _, err := os.Stat(filepath.Join(manager.BackupRoot, result.BackupID, "manifest.json")); err != nil {
		t.Fatal("original backup metadata was unexpectedly removed")
	}
}

func TestApplyRejectsSymbolicLinkConfig(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks requires additional Windows privileges")
	}
	home := t.TempDir()
	target := filepath.Join(home, "actual-config.toml")
	if err := os.WriteFile(target, []byte(testOriginalConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := NewConfigManager(filepath.Join(home, ".codex"))
	if err := os.MkdirAll(filepath.Dir(manager.ConfigPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, manager.ConfigPath); err != nil {
		t.Fatal(err)
	}

	_, err := manager.Apply(ApplyConfig{BaseURL: "https://gateway.example.com", APIKey: "sk-test-1234567890"})
	if err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("Apply() error = %v, want symbolic link rejection", err)
	}
	after, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(after) != testOriginalConfig {
		t.Fatal("symlink target changed after rejected apply")
	}
}

func TestEnsureConfigUnchangedDetectsExternalEdit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	original := []byte("model = \"before\"\n")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("model = \"after\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ensureConfigUnchanged(path, original, true); err == nil || !strings.Contains(err.Error(), "changed during") {
		t.Fatalf("ensureConfigUnchanged() error = %v, want concurrent edit rejection", err)
	}
}

func TestRollbackConfigErrorReportsUnverifiedRollback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config-as-directory")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	err := rollbackConfigError(errors.New("forced mutation failure"), path, []byte("model = \"before\"\n"), true, 0o600)
	var mutationErr *ConfigMutationError
	if !errors.As(err, &mutationErr) || mutationErr.RollbackErr == nil {
		t.Fatalf("rollbackConfigError() = %v, want ConfigMutationError with rollback failure", err)
	}
}

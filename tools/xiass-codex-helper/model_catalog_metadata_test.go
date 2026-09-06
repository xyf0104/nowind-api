package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// All instruction text is synthetic. Never load developer-machine prompts as
// test fixtures or include descriptor values in assertion failures.
func syntheticNativeDescriptors(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	result := make(map[string]json.RawMessage)
	for _, id := range catalogModelOrder {
		encoded, _ := json.Marshal(newCatalogModel(id))
		var model map[string]any
		_ = json.Unmarshal(encoded, &model)
		delete(model, "base_instructions")
		model["model_messages"] = map[string]any{
			"instructions_template":   "Synthetic native template for " + id,
			"persistent_instructions": "Synthetic persistent instructions",
			"collaboration_modes":     map[string]any{"default": "Synthetic default mode", "plan": nil},
			"multi_agent":             map[string]any{"role": map[string]string{"root": "Synthetic root", "subagent": "Synthetic subagent"}, "mode": nil},
			"token_budget":            map[string]any{"enabled": true, "use_history_notes_extension": true, "reminder_threshold_tokens": 1000, "reminder_message_template": "Synthetic reminder", "guidance_message": "Synthetic guidance", "auto_compact_fallback_prompt": "Synthetic compaction", "auto_compact_fallback_buffer_tokens": 100},
		}
		model["multi_agent_version"] = "v2"
		model["multi_agent_reasoning_effort"] = "xhigh"
		model["comp_hash"] = "synthetic-comp-hash"
		model["context_window"], model["max_context_window"] = 272000, 872000
		model["shell_type"] = "unified_exec"
		model["node_repl_auto_review_required"] = true
		model["experimental_supported_tools"] = []string{"send_user_message_async", "clock"}
		model["include_apps_usage_instructions"] = false
		model["include_plugin_usage_instructions"] = false
		model["include_skills_usage_instructions"] = false
		model["use_responses_lite"] = true
		model["future_capability"] = map[string]any{"enabled": true, "version": 2}
		levels := append([]modelCatalogReasoningLevel(nil), catalogReasoningLevels...)
		levels = append(levels, modelCatalogReasoningLevel{Effort: "ultra", Description: "Synthetic ultra description"})
		model["supported_reasoning_levels"] = levels
		raw, err := json.Marshal(model)
		if err != nil {
			t.Fatal("could not encode synthetic descriptor")
		}
		result[id] = raw
	}
	return result
}

func writeSyntheticNativeCache(t *testing.T, home string) {
	t.Helper()
	var models []json.RawMessage
	for _, raw := range syntheticNativeDescriptors(t) {
		models = append(models, raw)
	}
	data, _ := json.Marshal(map[string]any{"client_version": "0.153.4", "models": models})
	if err := os.WriteFile(filepath.Join(home, "models_cache.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCatalogKnownModelsWithoutMetadataKeepNativeResolution(t *testing.T) {
	for _, ids := range [][]string{{"gpt-6-astra"}, {"gpt-6-astra", "custom-model"}} {
		data, err := buildModelCatalogJSON("https://relay.example/v1", "gpt-6-astra", ids)
		if err != nil || len(data) != 0 {
			t.Fatal("ID-only known models must not install a generic override")
		}
	}
	manager := NewConfigManager(t.TempDir())
	original := []byte("model_reasoning_effort = \"ultra\"\nmodel_catalog_json = \"old-generic.json\"\n")
	if err := os.WriteFile(manager.ConfigPath, original, 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := manager.Apply(ApplyConfig{BaseURL: "https://relay.example/v1", APIKey: "synthetic-key", Model: "gpt-6-astra", ModelContextWindow: 800000, ModelAutoCompactTokenLimit: 700000})
	if err != nil {
		t.Fatal(err)
	}
	config, _ := os.ReadFile(manager.ConfigPath)
	if result.CatalogSHA != "" || strings.Contains(string(config), "model_catalog_json") || !strings.Contains(string(config), `model_reasoning_effort = "ultra"`) || !strings.Contains(string(config), "model_context_window = 800000") {
		t.Fatal("native fallback changed effort/context or retained a generic override")
	}
}

func TestCatalogUsesNativeCacheOnlyForRequestedExactIDs(t *testing.T) {
	home := t.TempDir()
	writeSyntheticNativeCache(t, home)
	cachePath := filepath.Join(home, "models_cache.json")
	before, _ := os.ReadFile(cachePath)
	native := readNativeModelDescriptors(cachePath)
	partial := map[string]json.RawMessage{"gpt-6-astra": json.RawMessage(`{"slug":"gpt-6-astra","supports_search_tool":false,"use_responses_lite":false}`)}
	data, err := buildModelCatalogJSON("https://relay.example/v1", "gpt-6-astra", []string{"custom-model"}, partial, native)
	if err != nil {
		t.Fatal(err)
	}
	var catalog struct {
		Models []map[string]json.RawMessage `json:"models"`
	}
	if json.Unmarshal(data, &catalog) != nil || len(catalog.Models) != 2 {
		t.Fatal("native cache expanded the provider's picker roster")
	}
	var expected map[string]json.RawMessage
	_ = json.Unmarshal(native["gpt-6-astra"], &expected)
	var got map[string]json.RawMessage
	for _, model := range catalog.Models {
		if string(model["slug"]) == `"gpt-6-astra"` {
			got = model
		}
	}
	for _, field := range []string{"model_messages", "multi_agent_version", "supported_reasoning_levels", "future_capability"} {
		assertCatalogJSONEqual(t, got[field], expected[field], field)
	}
	if string(got["supports_search_tool"]) != "false" || string(got["use_responses_lite"]) != "false" {
		t.Fatal("native cache overrode provider protocol capabilities")
	}
	after, _ := os.ReadFile(cachePath)
	if string(before) != string(after) {
		t.Fatal("native cache was mutated")
	}
	manager := NewConfigManager(home)
	if _, err := manager.Apply(ApplyConfig{BaseURL: "https://relay.example/v1", APIKey: "synthetic-key", Model: "gpt-6-astra"}); err != nil {
		t.Fatal(err)
	}
	config, _ := os.ReadFile(manager.ConfigPath)
	if !strings.Contains(string(config), "model_catalog_json") {
		t.Fatal("Apply did not use fixed-home native cache")
	}
}

func assertCatalogJSONEqual(t *testing.T, got, want []byte, field string) {
	t.Helper()
	var left, right any
	if json.Unmarshal(got, &left) != nil || json.Unmarshal(want, &right) != nil || !reflect.DeepEqual(left, right) {
		t.Errorf("catalog field %s changed (values withheld)", field)
	}
}

func TestCatalogRejectsUnsafeMetadata(t *testing.T) {
	for _, body := range []string{
		`{"models":[{"slug":"gpt-6-astra","context_window":"bad"}]}`,
		`{"models":[{"slug":"gpt-6-astra","context_window":-1}]}`,
		`{"models":[{"slug":"gpt-6-astra","multi_agent_version":{}}]}`,
		`{"models":[{"slug":"gpt-6-astra","model_messages":[]}]}`,
		`{"models":[{"slug":"gpt-6-astra","extra":{"api_key":"synthetic-secret"}}]}`,
		`{"models":[{"slug":"gpt-6-astra","base_url":"https://secret.example"}]}`,
		`{"models":[{"slug":"gpt-6-astra"},{"slug":"gpt-6-astra","display_name":"conflict"}]}`,
		`{"models":[{"slug":"gpt-6-astra"}]} trailing`,
		strings.Repeat(" ", modelCatalogMaxBytes+1),
	} {
		if _, err := parseDiscoveredModelCatalog([]byte(body)); err == nil {
			t.Fatal("invalid/unsafe catalog was accepted")
		}
	}
	home := t.TempDir()
	writeSyntheticNativeCache(t, home)
	path := filepath.Join(home, "models_cache.json")
	link := filepath.Join(home, "cache-link.json")
	if err := os.Symlink(path, link); err == nil && len(readNativeModelDescriptors(link)) != 0 {
		t.Fatal("symlink cache was trusted")
	}
	if err := os.WriteFile(path, []byte(`{"client_version":"0.153.4","models":[{"slug":"gpt-6-astra"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if len(readNativeModelDescriptors(path)) != 0 {
		t.Fatal("ID-only cache was trusted as native metadata")
	}
	var generic map[string]any
	_ = json.Unmarshal(syntheticNativeDescriptors(t)["gpt-6-astra"], &generic)
	generic["model_messages"] = map[string]string{"instructions_template": newCatalogModel("gpt-6-astra").BaseInstructions}
	data, _ := json.Marshal(map[string]any{"client_version": "0.153.4", "models": []any{generic}})
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if len(readNativeModelDescriptors(path)) != 0 {
		t.Fatal("an old helper generic template was mistaken for native metadata")
	}
}

func TestCatalogProviderDescriptorIsAuthoritative(t *testing.T) {
	native := syntheticNativeDescriptors(t)
	var model map[string]any
	_ = json.Unmarshal(native["gpt-6-astra"], &model)
	model["default_reasoning_level"] = "ultra"
	model["supported_in_api"] = false
	model["model_messages"] = map[string]string{"instructions_template": "Synthetic provider template", "persistent_instructions": "Synthetic provider persistent instructions"}
	raw, _ := json.Marshal(model)
	downloaded := map[string]json.RawMessage{"gpt-6-astra": raw}
	data, err := buildModelCatalogJSON("https://relay.example/v1", "gpt-6-astra", nil, downloaded, native)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Models []json.RawMessage `json:"models"`
	}
	if json.Unmarshal(data, &result) != nil || len(result.Models) != 1 {
		t.Fatal("invalid generated catalog")
	}
	model["use_responses_lite"] = false
	model["supported_in_api"] = true
	expected, _ := json.Marshal(model)
	assertCatalogJSONEqual(t, result.Models[0], expected, "complete provider descriptor")
	if string(downloaded["gpt-6-astra"]) != string(raw) {
		t.Fatal("source descriptor was mutated")
	}
}

func TestCatalogRejectsCredentialEchoBeforeConfigMutation(t *testing.T) {
	manager := NewConfigManager(t.TempDir())
	original := []byte("model_reasoning_effort = \"ultra\"\n")
	if err := os.WriteFile(manager.ConfigPath, original, 0o600); err != nil {
		t.Fatal(err)
	}
	var model map[string]any
	_ = json.Unmarshal(syntheticNativeDescriptors(t)["gpt-6-astra"], &model)
	model["model_messages"] = map[string]string{"instructions_template": "Synthetic credential echo: test-<secret>-marker"}
	raw, _ := json.Marshal(model)
	_, err := manager.Apply(ApplyConfig{BaseURL: "https://relay.example/v1", APIKey: "test-<secret>-marker", Model: "gpt-6-astra", ModelCatalogDescriptors: map[string]json.RawMessage{"gpt-6-astra": raw}})
	if err == nil {
		t.Fatal("credential echo was written to the catalog")
	}
	current, _ := os.ReadFile(manager.ConfigPath)
	if string(current) != string(original) {
		t.Fatal("rejected metadata changed config")
	}
	if _, err := os.Stat(manager.ModelCatalogRoot); !os.IsNotExist(err) {
		t.Fatal("rejected metadata produced a catalog file")
	}
}

func TestHelperCatalogPickerCacheIsCredentialBoundAndPrivate(t *testing.T) {
	helper, err := newTestHelperServer(NewConfigManager(t.TempDir()), "https://relay.example", "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNO1")
	if err != nil {
		t.Fatal(err)
	}
	descriptors := syntheticNativeDescriptors(t)
	var calls int
	helper.listModels = func(string, string) (discoveredModelCatalog, error) {
		calls++
		return discoveredModelCatalog{IDs: []string{"gpt-6-astra"}, Descriptors: descriptors}, nil
	}
	body := []byte(`{"base_url":"https://relay.example/v1","api_key":"synthetic-key"}`)
	response := postHelperJSON(t, helper.routes(), "/api/models", helper.state, body, http.StatusOK)
	if len(response) != 2 || response["models"] == nil {
		t.Fatal("picker exposed descriptor payload")
	}
	helper.prepare = func() error { return nil }
	helper.stop = func(CodexInstallation) error { return nil }
	helper.start = func(CodexInstallation) error { return nil }
	helper.repairHistory = func() (HistoryRepairResult, error) { return HistoryRepairResult{}, nil }
	helper.applyConfig = func(input ApplyConfig) (ApplyResult, error) {
		assertCatalogJSONEqual(t, input.ModelCatalogDescriptors["gpt-6-astra"], descriptors["gpt-6-astra"], "trusted-descriptor")
		return ApplyResult{CatalogSHA: "synthetic"}, nil
	}
	apply := []byte(`{"base_url":"https://relay.example/v1","api_key":"synthetic-key","model":"gpt-6-astra","model_catalog_models":["gpt-6-astra"],"model_catalog_descriptors":{"gpt-6-astra":{"model_messages":{"instructions_template":"injected"}}}}`)
	postHelperJSON(t, helper.routes(), "/api/apply-manual", helper.state, apply, http.StatusBadRequest)
	apply = []byte(`{"base_url":"https://relay.example/v1","api_key":"synthetic-key","model":"gpt-6-astra","model_catalog_models":["gpt-6-astra"]}`)
	postHelperJSON(t, helper.routes(), "/api/apply-manual", helper.state, apply, http.StatusOK)
	if calls != 1 {
		t.Fatal("apply did not reuse the private picker catalog")
	}
	_, _ = helper.loadModelCatalog("https://relay.example/v1", "different-key")
	_, _ = helper.loadModelCatalog("https://other.example/v1", "different-key")
	helper.modelCatalogFetchedAt = time.Now().Add(-6 * time.Minute)
	_, _ = helper.loadModelCatalog("https://other.example/v1", "different-key")
	if calls != 4 {
		t.Fatal("catalog cache crossed connection identities or failed to expire")
	}
}

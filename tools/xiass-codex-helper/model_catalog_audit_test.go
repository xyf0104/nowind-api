package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"reflect"
	"testing"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// Regression from the read-only audit, now exercised through full discovery.
func TestCatalogAuditAstraMetadataRoundTrip(t *testing.T) {
	want := map[string]any{
		"slug": "gpt-6-astra",
		"model_messages": map[string]any{
			"instructions_template":   "synthetic-template-fixture",
			"persistent_instructions": "synthetic-persistent-fixture",
		},
		"multi_agent_version":               "v2",
		"multi_agent_reasoning_effort":      "xhigh",
		"comp_hash":                         "synthetic-comp-hash",
		"context_window":                    float64(272000),
		"max_context_window":                float64(872000),
		"shell_type":                        "unified_exec",
		"node_repl_auto_review_required":    true,
		"experimental_supported_tools":      []any{"send_user_message_async", "clock"},
		"include_apps_usage_instructions":   false,
		"include_plugin_usage_instructions": false,
		"include_skills_usage_instructions": false,
	}
	efforts := []string{"low", "medium", "high", "xhigh", "max", "ultra"}
	levels := make([]map[string]string, 0, len(efforts))
	for _, effort := range efforts {
		levels = append(levels, map[string]string{"effort": effort, "description": "synthetic description"})
	}
	var upstreamModel map[string]any
	_ = json.Unmarshal(syntheticNativeDescriptors(t)["gpt-6-astra"], &upstreamModel)
	for field, value := range want {
		upstreamModel[field] = value
	}
	upstreamModel["supported_reasoning_levels"] = levels
	body, err := json.Marshal(map[string]any{"models": []any{upstreamModel}})
	if err != nil {
		t.Fatal("could not encode synthetic catalog")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" || r.URL.Query().Get("client_version") == "" {
			t.Error("discovery did not request the Codex manifest route")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer server.Close()

	discovered, err := discoverCompatibleModelCatalog(server.URL+"/v1", "synthetic-audit-key")
	if err != nil {
		t.Fatal("loopback fixture discovery failed")
	}
	data, err := buildModelCatalogJSON(server.URL+"/v1", "gpt-6-astra", discovered.IDs, discovered.Descriptors)
	if err != nil {
		t.Fatal("catalog generation failed")
	}
	var catalog struct {
		Models []map[string]any `json:"models"`
	}
	if err := json.Unmarshal(data, &catalog); err != nil || len(catalog.Models) != 1 {
		t.Fatal("generated catalog did not contain the single fixture model")
	}
	got := catalog.Models[0]
	for field, expected := range want {
		t.Run(field, func(t *testing.T) {
			if !reflect.DeepEqual(got[field], expected) {
				t.Errorf("generated catalog does not preserve server field %q (values redacted)", field)
			}
		})
	}
	t.Run("supported_reasoning_levels", func(t *testing.T) {
		var generated modelCatalogFile
		if err := json.Unmarshal(data, &generated); err != nil {
			t.Fatal("could not decode generated reasoning levels")
		}
		var gotEfforts []string
		for _, level := range generated.Models[0].SupportedReasoningLevels {
			gotEfforts = append(gotEfforts, level.Effort)
		}
		if !reflect.DeepEqual(gotEfforts, efforts) {
			t.Errorf("reasoning levels = %v; want %v", gotEfforts, efforts)
		}
	})
}

func TestCatalogAuditReasoningConfigIsPreservedNotSelected(t *testing.T) {
	input, err := normalizeApplyConfig(ApplyConfig{
		BaseURL: "https://catalog-audit.example/v1",
		APIKey:  "synthetic-audit-key",
		Model:   "gpt-6-astra",
	})
	if err != nil {
		t.Fatal("could not normalize synthetic config")
	}
	for _, canonical := range []bool{false, true} {
		mode := "manual"
		if canonical {
			mode = "website"
		}
		for _, existing := range []bool{false, true} {
			name := mode + "/fresh"
			var original []byte
			if existing {
				name = mode + "/existing_ultra"
				original = []byte("model_reasoning_effort = \"ultra\"\n")
			}
			t.Run(name, func(t *testing.T) {
				updated := patchConfig(original, input, providerID)
				if canonical {
					var patchErr error
					updated, patchErr = patchCanonicalXIASSConfig(original, input)
					if patchErr != nil {
						t.Fatal("could not patch synthetic canonical config")
					}
				}
				var root map[string]any
				if err := toml.Unmarshal(updated, &root); err != nil {
					t.Fatal("could not parse synthetic config")
				}
				value, present := root["model_reasoning_effort"]
				if present != existing || (existing && value != "ultra") {
					t.Fatal("helper selected reasoning or changed the existing effort")
				}
			})
		}
	}
}

func TestCatalogAuditInstalledCodexLoadsGeneratedCatalog(t *testing.T) {
	binary := "/Applications/ChatGPT.app/Contents/Resources/codex"
	if _, err := os.Stat(binary); err != nil {
		t.Skip("installed Codex binary is unavailable")
	}
	home := t.TempDir()
	writeSyntheticNativeCache(t, home)
	manager := NewConfigManager(home)
	if _, err := manager.Apply(ApplyConfig{BaseURL: "https://catalog-audit.example/v1", APIKey: "synthetic-key", Model: "gpt-6-astra", ModelContextWindow: 800000, ModelAutoCompactTokenLimit: 700000}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, "debug", "models")
	command.Env = []string{
		"PATH=/usr/bin:/bin", "HOME=" + home, "CODEX_HOME=" + home, "TMPDIR=" + home,
		"HTTP_PROXY=http://127.0.0.1:1", "HTTPS_PROXY=http://127.0.0.1:1", "ALL_PROXY=http://127.0.0.1:1",
	}
	// Keep stderr separate: current Codex can emit startup warnings there.
	output, err := command.Output()
	if err != nil {
		t.Fatal("isolated debug models failed; subprocess output withheld")
	}
	var catalog struct {
		Models []map[string]json.RawMessage `json:"models"`
	}
	if err := json.Unmarshal(output, &catalog); err != nil {
		t.Fatal("isolated debug models stdout was not valid catalog JSON")
	}
	for _, model := range catalog.Models {
		var slug string
		_ = json.Unmarshal(model["slug"], &slug)
		if slug != "gpt-6-astra" {
			continue
		}
		var parsed modelCatalogModel
		encoded, _ := json.Marshal(model)
		if err := json.Unmarshal(encoded, &parsed); err != nil {
			t.Fatal("could not decode installed-client Astra metadata")
		}
		var expected map[string]json.RawMessage
		_ = json.Unmarshal(syntheticNativeDescriptors(t)[slug], &expected)
		for _, field := range []string{"multi_agent_version", "multi_agent_reasoning_effort", "comp_hash", "supported_reasoning_levels", "context_window", "max_context_window", "shell_type", "experimental_supported_tools", "node_repl_auto_review_required"} {
			assertCatalogJSONEqual(t, model[field], expected[field], field)
		}
		var messages map[string]json.RawMessage
		_ = json.Unmarshal(model["model_messages"], &messages)
		var expectedMessages map[string]json.RawMessage
		_ = json.Unmarshal(expected["model_messages"], &expectedMessages)
		for _, field := range []string{"instructions_template", "persistent_instructions", "multi_agent", "collaboration_modes", "token_budget"} {
			assertCatalogJSONEqual(t, messages[field], expectedMessages[field], "model_messages."+field)
		}
		var efforts []string
		for _, level := range parsed.SupportedReasoningLevels {
			efforts = append(efforts, level.Effort)
		}
		if !modelCatalogContains(efforts, "ultra") || parsed.UseResponsesLite {
			t.Fatal("installed-client lost ultra or the full Responses adaptation")
		}
		t.Log("installed Codex preserved synthetic native templates, multi-agent metadata and ultra; full Responses adaptation retained")
		return
	}
	t.Fatal("installed Codex did not load generated Astra model")
}

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestXIASSModelCatalogAddsGPT6WithoutCredentials(t *testing.T) {
	data, err := buildModelCatalogJSON(
		"https://xiass.example/v1",
		"gpt-6-astra",
		[]string{"gpt-5.6-sol", "gpt-5.6-luna"},
	)
	if err != nil {
		t.Fatal(err)
	}
	var catalog modelCatalogFile
	if err := json.Unmarshal(data, &catalog); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, model := range catalog.Models {
		if model.Slug == xiassCodexEnabledModel {
			found = model.Visibility == "list" && model.SupportedInAPI
		}
	}
	if !found {
		t.Fatal("XIASS GPT-6 model is not visible and API-enabled in the generated catalog")
	}
	for _, secret := range []string{"api." + strings.Join([]string{"xiass", "com"}, "."), "Bearer ", "experimental_bearer_token", "api_key"} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("generated model catalog contains forbidden connection data %q", secret)
		}
	}
}

func TestGenericModelCatalogDoesNotAddXIASSGPT6(t *testing.T) {
	data, err := buildModelCatalogJSON(
		"https://relay.example.com/v1",
		"relay-model",
		[]string{"relay-model", "relay-fast"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), xiassCodexEnabledModel) {
		t.Fatal("generic compatible API catalog was augmented with the XIASS-only GPT-6 model")
	}
}

func TestGenericModelCatalogKeepsReturnedGPT6(t *testing.T) {
	data, err := buildModelCatalogJSON(
		"https://relay.example.com/v1",
		"gpt-6-astra",
		[]string{"gpt-6-astra", "relay-fast"},
	)
	if err != nil {
		t.Fatal(err)
	}
	var catalog modelCatalogFile
	if err := json.Unmarshal(data, &catalog); err != nil {
		t.Fatal(err)
	}
	for _, model := range catalog.Models {
		if model.Slug == xiassCodexEnabledModel {
			if !model.SupportedInAPI || !modelCatalogContains(model.InputModalities, "image") || !model.SupportsSearchTool {
				t.Fatalf("returned generic GPT-6 lost capabilities: %+v", model)
			}
			return
		}
	}
	t.Fatal("a generic API's returned GPT-6 model was removed from the local catalog")
}

func TestGeneratedModelCatalogLoadsInInstalledCodex(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("installed Codex catalog parser check is available on the macOS development host")
	}
	binary := "/Applications/ChatGPT.app/Contents/Resources/codex"
	if _, err := os.Stat(binary); err != nil {
		t.Skip("installed Codex binary is unavailable")
	}
	home := t.TempDir()
	catalogPath := filepath.Join(home, "model-catalog.json")
	data, err := buildModelCatalogJSON(
		defaultXIASSAPIURL+"/v1",
		"gpt-6-astra",
		[]string{"gpt-5.6-sol", "gpt-6-astra"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(catalogPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	configValue := `model_catalog_json="` + escapeTOML(catalogPath) + `"`
	command := exec.Command(binary, "debug", "-c", configValue, "models")
	command.Env = append(os.Environ(), "CODEX_HOME="+home)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("installed Codex rejected generated catalog: %v\n%s", err, output)
	}
	var parsed modelCatalogFile
	if err := json.Unmarshal(output, &parsed); err != nil {
		t.Fatalf("decode Codex catalog output: %v", err)
	}
	for _, model := range parsed.Models {
		if model.Slug == xiassCodexEnabledModel {
			return
		}
	}
	t.Fatal("installed Codex loaded the catalog but did not expose gpt-6-astra")
}

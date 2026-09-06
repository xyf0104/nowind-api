package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"runtime"
	"sort"
	"strings"
)

const (
	modelCatalogMaxModels   = 256
	modelCatalogMaxBytes    = 8 << 20
	modelDescriptorMaxBytes = 1 << 20
)

type discoveredModelCatalog struct {
	IDs         []string
	Descriptors map[string]json.RawMessage
}

type modelCatalogFile struct {
	Models []modelCatalogModel `json:"models"`
}

type modelCatalogModel struct {
	AdditionalSpeedTiers           []string                     `json:"additional_speed_tiers"`
	ApplyPatchToolType             string                       `json:"apply_patch_tool_type"`
	AvailableInPlans               []string                     `json:"available_in_plans,omitempty"`
	AvailabilityNUX                any                          `json:"availability_nux"`
	BaseInstructions               string                       `json:"base_instructions"`
	ContextWindow                  int64                        `json:"context_window"`
	DefaultReasoningLevel          string                       `json:"default_reasoning_level"`
	DefaultReasoningSummary        string                       `json:"default_reasoning_summary"`
	DefaultVerbosity               string                       `json:"default_verbosity"`
	Description                    string                       `json:"description"`
	DisplayName                    string                       `json:"display_name"`
	EffectiveContextWindowPercent  int                          `json:"effective_context_window_percent"`
	ExperimentalSupportedTools     []string                     `json:"experimental_supported_tools"`
	IncludeAppsUsageInstructions   bool                         `json:"include_apps_usage_instructions"`
	IncludePluginUsageInstructions bool                         `json:"include_plugin_usage_instructions"`
	IncludeSkillsUsageInstructions bool                         `json:"include_skills_usage_instructions"`
	InputModalities                []string                     `json:"input_modalities"`
	MaxContextWindow               int64                        `json:"max_context_window"`
	MinimalClientVersion           string                       `json:"minimal_client_version,omitempty"`
	NodeReplAutoReviewRequired     bool                         `json:"node_repl_auto_review_required"`
	NodeReplDisabled               bool                         `json:"node_repl_disabled"`
	Priority                       int                          `json:"priority"`
	PreferWebsockets               bool                         `json:"prefer_websockets,omitempty"`
	ServiceTiers                   []modelCatalogServiceTier    `json:"service_tiers"`
	ShellType                      string                       `json:"shell_type"`
	Slug                           string                       `json:"slug"`
	SupportVerbosity               bool                         `json:"support_verbosity"`
	SupportedInAPI                 bool                         `json:"supported_in_api"`
	SupportedReasoningLevels       []modelCatalogReasoningLevel `json:"supported_reasoning_levels"`
	SupportsImageDetailOriginal    bool                         `json:"supports_image_detail_original"`
	SupportsParallelToolCalls      bool                         `json:"supports_parallel_tool_calls,omitempty"`
	SupportsReasoningSummaries     bool                         `json:"supports_reasoning_summaries,omitempty"`
	SupportsSearchTool             bool                         `json:"supports_search_tool"`
	ToolMode                       string                       `json:"tool_mode,omitempty"`
	TruncationPolicy               modelCatalogTruncationPolicy `json:"truncation_policy"`
	Upgrade                        any                          `json:"upgrade"`
	UseResponsesLite               bool                         `json:"use_responses_lite"`
	Visibility                     string                       `json:"visibility"`
	WebSearchToolType              string                       `json:"web_search_tool_type"`
}

type modelCatalogReasoningLevel struct {
	Description string `json:"description"`
	Effort      string `json:"effort"`
}

type modelCatalogServiceTier struct {
	Description string `json:"description"`
	ID          string `json:"id"`
	Name        string `json:"name"`
}

type modelCatalogTruncationPolicy struct {
	Limit int64  `json:"limit"`
	Mode  string `json:"mode"`
}

var catalogModelOrder = []string{
	"gpt-reserve",
	"gpt-6-astra",
	"gpt-5.6-sol",
	"gpt-5.6-terra",
	"gpt-5.6-luna",
	"gpt-5.5",
	"gpt-5.4",
	"gpt-5.4-mini",
	"gpt-5.3-codex-spark",
	"gpt-image-2",
	"codex-auto-review",
}

var catalogReasoningLevels = []modelCatalogReasoningLevel{
	{Effort: "low", Description: "Fast responses with lighter reasoning"},
	{Effort: "medium", Description: "Balances speed and reasoning depth for everyday tasks"},
	{Effort: "high", Description: "Greater reasoning depth for complex problems"},
	{Effort: "xhigh", Description: "Extra high reasoning depth for complex problems"},
	{Effort: "max", Description: "Maximum reasoning depth for the hardest problems"},
}

var catalogAvailablePlans = []string{
	"business", "edu", "education", "enterprise", "free", "free_workspace",
	"go", "plus", "pro", "prolite", "team",
}

// Sources are ordered by authority: provider metadata, then a trusted local
// native cache. A nil catalog leaves native model resolution to Codex instead
// of replacing a known model's instructions with a generic ID-only entry.
func buildModelCatalogJSON(baseURL, selectedModel string, discovered []string, sources ...map[string]json.RawMessage) ([]byte, error) {
	ids, err := normalizeModelIDs(discovered)
	if err != nil {
		return nil, err
	}
	selectedModel = strings.TrimSpace(selectedModel)
	if selectedModel != "" {
		if err := validateModelID(selectedModel); err != nil {
			return nil, err
		}
		ids = appendUniqueModelID(ids, selectedModel)
	}

	canonical := isCanonicalXIASSBaseURL(baseURL)
	if canonical {
		if len(ids) == 0 {
			ids, err = normalizeModelIDs(catalogModelOrder)
			if err != nil {
				return nil, err
			}
		}
		// Older XIASS deployments may return a stale catalog. This is a local
		// compatibility entry; the server is still authoritative at request time.
		ids = appendUniqueModelID(ids, xiassCodexEnabledModel)
	}
	if len(ids) == 0 {
		return nil, errors.New("no usable models were provided for the local catalog")
	}
	if len(ids) > modelCatalogMaxModels {
		return nil, errors.New("too many models for the local catalog")
	}

	order := make(map[string]int, len(catalogModelOrder))
	for index, id := range catalogModelOrder {
		order[id] = index
	}
	sort.SliceStable(ids, func(i, j int) bool {
		left, leftKnown := order[ids[i]]
		right, rightKnown := order[ids[j]]
		if leftKnown && rightKnown {
			return left < right
		}
		if leftKnown != rightKnown {
			return leftKnown
		}
		return ids[i] < ids[j]
	})

	catalog := struct {
		Models []json.RawMessage `json:"models"`
	}{Models: make([]json.RawMessage, 0, len(ids))}
	for _, id := range ids {
		var fields map[string]json.RawMessage
		for _, source := range sources {
			raw := source[id]
			if len(raw) == 0 {
				continue
			}
			if err := validateModelDescriptor(raw, false); err != nil {
				return nil, err
			}
			var candidate map[string]json.RawMessage
			_ = json.Unmarshal(raw, &candidate)
			var slug string
			_ = json.Unmarshal(candidate["slug"], &slug)
			if slug != id {
				return nil, errors.New("model descriptor identity mismatch")
			}
			if fields == nil {
				fields = make(map[string]json.RawMessage)
			}
			for key, value := range candidate {
				if _, exists := fields[key]; !exists {
					fields[key] = value
				}
			}
			merged, _ := json.Marshal(fields)
			if validateModelDescriptor(merged, true) == nil {
				break
			}
		}
		raw, _ := json.Marshal(fields)
		if validateModelDescriptor(raw, true) != nil {
			if isKnownNativeModel(id) {
				return nil, nil
			}
			fallback := newCatalogModel(id)
			fallback.Description = "Fallback metadata for an ID-only compatible API model."
			encoded, _ := json.Marshal(fallback)
			var defaults map[string]json.RawMessage
			_ = json.Unmarshal(encoded, &defaults)
			for key, value := range fields {
				defaults[key] = value
			}
			fields = defaults
		}
		// The selected provider's roster, not first-party API eligibility (for
		// example OAuth-only Spark), controls availability in custom API mode.
		fields["supported_in_api"] = json.RawMessage("true")
		// These models need full Responses for custom-provider web tools. Keep
		// this transport adaptation independent of their native instructions.
		switch id {
		case "gpt-6-astra", "gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna":
			fields["use_responses_lite"] = json.RawMessage("false")
		}
		raw, _ = json.Marshal(fields)
		catalog.Models = append(catalog.Models, raw)
	}
	data, err := json.MarshalIndent(catalog, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode local model catalog: %w", err)
	}
	data = append(data, '\n')
	if err := validateModelCatalog(data); err != nil {
		return nil, err
	}
	return data, nil
}

func normalizeModelIDs(models []string) ([]string, error) {
	if len(models) > modelCatalogMaxModels {
		return nil, errors.New("compatible API returned too many models")
	}
	result := make([]string, 0, len(models))
	for _, raw := range models {
		model := strings.TrimSpace(raw)
		if model == "" {
			continue
		}
		if err := validateModelID(model); err != nil {
			return nil, err
		}
		result = appendUniqueModelID(result, model)
		if len(result) > modelCatalogMaxModels {
			return nil, errors.New("compatible API returned too many models")
		}
	}
	return result, nil
}

func appendUniqueModelID(models []string, candidate string) []string {
	for _, model := range models {
		if model == candidate {
			return models
		}
	}
	return append(models, candidate)
}

func validateModelID(model string) error {
	if model == "" || len(model) > 200 || strings.ContainsAny(model, "\r\n\x00") {
		return errors.New("invalid model name")
	}
	return nil
}

func validateModelCatalog(data []byte) error {
	if len(data) > modelCatalogMaxBytes {
		return errors.New("model catalog is too large")
	}
	var catalog struct {
		Models []json.RawMessage `json:"models"`
	}
	if err := json.Unmarshal(data, &catalog); err != nil {
		return fmt.Errorf("validate local model catalog: %w", err)
	}
	if len(catalog.Models) == 0 || len(catalog.Models) > modelCatalogMaxModels {
		return errors.New("local model catalog has no usable models")
	}
	seen := make(map[string]struct{}, len(catalog.Models))
	for _, raw := range catalog.Models {
		if err := validateModelDescriptor(raw, true); err != nil {
			return err
		}
		var model modelCatalogModel
		_ = json.Unmarshal(raw, &model)
		if _, exists := seen[model.Slug]; exists {
			return errors.New("local model catalog contains duplicate model IDs")
		}
		seen[model.Slug] = struct{}{}
	}
	return nil
}

func isKnownNativeModel(id string) bool {
	for _, known := range catalogModelOrder {
		if id == known {
			return true
		}
	}
	return false
}

func validateModelDescriptor(raw []byte, complete bool) error {
	if len(raw) > modelDescriptorMaxBytes {
		return errors.New("model descriptor is too large")
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return errors.New("invalid model descriptor")
	}
	var model modelCatalogModel
	if json.Unmarshal(raw, &model) != nil {
		return errors.New("invalid model descriptor field type")
	}
	if err := validateModelID(model.Slug); err != nil {
		return err
	}
	if model.ContextWindow < 0 || model.MaxContextWindow < 0 || model.EffectiveContextWindowPercent < 0 || model.EffectiveContextWindowPercent > 100 {
		return errors.New("invalid model context metadata")
	}
	for _, field := range []string{"multi_agent_version", "multi_agent_reasoning_effort", "comp_hash"} {
		if value, ok := fields[field]; ok {
			var text string
			if json.Unmarshal(value, &text) != nil {
				return errors.New("invalid model capability field type")
			}
		}
	}
	var tree any
	_ = json.Unmarshal(raw, &tree)
	if containsCatalogSensitiveData(tree, "") {
		return errors.New("model descriptor contains connection or account data")
	}
	var messages struct {
		InstructionsTemplate string `json:"instructions_template"`
	}
	if value, ok := fields["model_messages"]; ok && !bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		value = bytes.TrimSpace(value)
		if json.Unmarshal(value, &messages) != nil || len(value) == 0 || value[0] != '{' {
			return errors.New("invalid model messages")
		}
	}
	if complete {
		if strings.TrimSpace(model.DisplayName) == "" || strings.TrimSpace(model.Description) == "" {
			return errors.New("incomplete model metadata")
		}
		if strings.TrimSpace(model.BaseInstructions) == "" && strings.TrimSpace(messages.InstructionsTemplate) == "" {
			return errors.New("model descriptor has no instructions")
		}
		if model.ContextWindow <= 0 || model.MaxContextWindow < model.ContextWindow {
			return errors.New("invalid model context metadata")
		}
		if len(model.SupportedReasoningLevels) == 0 {
			return errors.New("model has no reasoning capabilities")
		}
		found := false
		seen := make(map[string]bool)
		for _, level := range model.SupportedReasoningLevels {
			if strings.TrimSpace(level.Effort) == "" || seen[level.Effort] {
				return errors.New("invalid model reasoning levels")
			}
			seen[level.Effort] = true
			if level.Effort == model.DefaultReasoningLevel {
				found = true
			}
		}
		if !found {
			return errors.New("model reasoning default is unsupported")
		}
	}
	return nil
}

func containsCatalogSensitiveData(value any, secret string) bool {
	switch value := value.(type) {
	case map[string]any:
		for key, child := range value {
			if secret != "" && strings.Contains(key, secret) {
				return true
			}
			switch strings.ToLower(key) {
			case "api_key", "access_token", "refresh_token", "id_token", "authorization", "auth", "credentials", "password", "base_url", "provider_url", "http_headers", "account_id", "user_id", "email", "experimental_bearer_token":
				return true
			}
			if containsCatalogSensitiveData(child, secret) {
				return true
			}
		}
	case []any:
		for _, child := range value {
			if containsCatalogSensitiveData(child, secret) {
				return true
			}
		}
	case string:
		return secret != "" && strings.Contains(value, secret)
	}
	return false
}

// Only the fixed cache in the selected Codex home is eligible, never a path
// supplied by the browser. Ignore unsafe, malformed, or ID-only cache entries.
func readNativeModelDescriptors(path string) map[string]json.RawMessage {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > modelCatalogMaxBytes {
		return nil
	}
	// Windows reports synthesized mode bits, not the file's ACL.
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o022 != 0 {
		return nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil
	}
	data, err := io.ReadAll(io.LimitReader(file, modelCatalogMaxBytes+1))
	if err != nil || len(data) > modelCatalogMaxBytes {
		return nil
	}
	var cache struct {
		ClientVersion string            `json:"client_version"`
		Models        []json.RawMessage `json:"models"`
	}
	if json.Unmarshal(data, &cache) != nil || cache.ClientVersion == "" || len(cache.Models) > modelCatalogMaxModels {
		return nil
	}
	models := make(map[string]json.RawMessage)
	for _, raw := range cache.Models {
		if validateModelDescriptor(raw, true) != nil {
			continue
		}
		var model struct {
			Slug     string `json:"slug"`
			Messages struct {
				Template string `json:"instructions_template"`
			} `json:"model_messages"`
		}
		if json.Unmarshal(raw, &model) != nil || strings.TrimSpace(model.Messages.Template) == "" || model.Messages.Template == newCatalogModel(model.Slug).BaseInstructions {
			continue
		}
		if _, duplicate := models[model.Slug]; duplicate {
			return nil
		}
		models[model.Slug] = raw
	}
	return models
}

func parseDiscoveredModelCatalog(body []byte) (discoveredModelCatalog, error) {
	result := discoveredModelCatalog{Descriptors: make(map[string]json.RawMessage)}
	if len(body) > modelCatalogMaxBytes {
		return result, errors.New("compatible API catalog is too large")
	}
	var envelope struct {
		Models []json.RawMessage `json:"models"`
		Data   []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &envelope) != nil {
		return result, errors.New("invalid compatible API model catalog")
	}
	for _, raw := range envelope.Models {
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil || fields == nil {
			return result, errors.New("invalid compatible API model entry")
		}
		var slug string
		_ = json.Unmarshal(fields["slug"], &slug)
		if strings.TrimSpace(slug) == "" {
			_ = json.Unmarshal(fields["id"], &slug)
		}
		slug = strings.TrimSpace(slug)
		if slug == "" {
			continue
		}
		fields["slug"], _ = json.Marshal(slug)
		descriptor, _ := json.Marshal(fields)
		if err := validateModelDescriptor(descriptor, false); err != nil {
			return result, err
		}
		if previous, duplicate := result.Descriptors[slug]; duplicate && !bytes.Equal(previous, descriptor) {
			return result, errors.New("conflicting model descriptors")
		}
		result.Descriptors[slug] = descriptor
		result.IDs = appendUniqueModelID(result.IDs, slug)
		if len(result.IDs) > modelCatalogMaxModels {
			return result, errors.New("compatible API returned too many models")
		}
	}
	for _, entry := range envelope.Data {
		id := strings.TrimSpace(entry.ID)
		if validateModelID(id) != nil {
			continue
		}
		result.IDs = appendUniqueModelID(result.IDs, id)
		if len(result.IDs) > modelCatalogMaxModels {
			return result, errors.New("compatible API returned too many models")
		}
	}
	if len(result.IDs) == 0 {
		return result, errors.New("compatible API did not return any usable model IDs")
	}
	sort.Strings(result.IDs)
	return result, nil
}

func isCanonicalXIASSBaseURL(baseURL string) bool {
	canonical, err := url.Parse(defaultXIASSAPIURL)
	if err != nil {
		return false
	}
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Scheme != canonical.Scheme || !strings.EqualFold(parsed.Host, canonical.Host) {
		return false
	}
	basePath := strings.TrimRight(parsed.Path, "/")
	canonicalPath := strings.TrimRight(canonical.Path, "/")
	return basePath == canonicalPath || basePath == canonicalPath+"/v1"
}

func newCatalogModel(slug string) modelCatalogModel {
	model := modelCatalogModel{
		AdditionalSpeedTiers:           []string{},
		ApplyPatchToolType:             "freeform",
		BaseInstructions:               "You are Codex, a coding agent. Work carefully, use the available tools, and follow the user's instructions.",
		Slug:                           slug,
		DisplayName:                    slug,
		Description:                    "Compatible Responses API model.",
		ContextWindow:                  defaultContextWindow,
		MaxContextWindow:               defaultContextWindow,
		DefaultReasoningLevel:          "medium",
		DefaultReasoningSummary:        "none",
		DefaultVerbosity:               "low",
		EffectiveContextWindowPercent:  95,
		ExperimentalSupportedTools:     []string{},
		IncludeAppsUsageInstructions:   true,
		IncludePluginUsageInstructions: true,
		IncludeSkillsUsageInstructions: true,
		InputModalities:                []string{"text"},
		Priority:                       100,
		ServiceTiers:                   []modelCatalogServiceTier{},
		ShellType:                      "unified_exec",
		SupportVerbosity:               true,
		SupportedInAPI:                 true,
		SupportedReasoningLevels:       append([]modelCatalogReasoningLevel(nil), catalogReasoningLevels...),
		SupportsImageDetailOriginal:    false,
		SupportsSearchTool:             true,
		TruncationPolicy:               modelCatalogTruncationPolicy{Mode: "tokens", Limit: 10000},
		Visibility:                     "list",
		WebSearchToolType:              "text_and_image",
		MinimalClientVersion:           "0.124.0",
		UseResponsesLite:               false,
	}

	switch slug {
	case "gpt-reserve":
		model.DisplayName = "GPT-Reserve"
		model.Description = "Fast and affordable agentic coding model."
		model.Visibility = "hide"
		model.Priority = 3
	case "gpt-6-astra":
		model.DisplayName = "GPT-6 Astra"
		model.Description = "GPT-6 agentic coding model provided by XIASS."
		model.DefaultReasoningLevel = "medium"
		model.Priority = 4
		model.InputModalities = []string{"text", "image"}
		model.ShellType = "shell_command"
		model.SupportVerbosity = true
		model.SupportsImageDetailOriginal = true
		model.SupportsParallelToolCalls = true
		model.SupportsReasoningSummaries = true
		model.SupportsSearchTool = true
		model.ToolMode = "code_mode_only"
		model.UseResponsesLite = false
		model.WebSearchToolType = "text_and_image"
		model.ApplyPatchToolType = "freeform"
		model.AvailableInPlans = append([]string(nil), catalogAvailablePlans...)
	case "gpt-5.6-sol":
		model.DisplayName = "GPT-5.6-Sol"
		model.Description = "Reliable agentic workhorse for everyday tasks."
		model.DefaultReasoningLevel = "low"
		model.Priority = 6
		model.InputModalities = []string{"text", "image"}
		model.ShellType = "shell_command"
		model.SupportVerbosity = true
		model.SupportsImageDetailOriginal = true
		model.SupportsReasoningSummaries = true
		model.SupportsSearchTool = true
		model.ToolMode = "code_mode_only"
		model.UseResponsesLite = true
		model.WebSearchToolType = "text_and_image"
		model.ApplyPatchToolType = "freeform"
		model.AvailableInPlans = append([]string(nil), catalogAvailablePlans...)
	case "gpt-5.6-terra":
		model.DisplayName = "GPT-5.6-Terra"
		model.Description = "Balanced agentic coding model for everyday work."
		model.Priority = 7
		model.InputModalities = []string{"text", "image"}
		model.ShellType = "shell_command"
		model.SupportVerbosity = true
		model.SupportsImageDetailOriginal = true
		model.SupportsReasoningSummaries = true
		model.SupportsSearchTool = true
		model.ToolMode = "code_mode_only"
		model.UseResponsesLite = true
		model.WebSearchToolType = "text_and_image"
		model.ApplyPatchToolType = "freeform"
		model.AvailableInPlans = append([]string(nil), catalogAvailablePlans...)
	case "gpt-5.6-luna":
		model.DisplayName = "GPT-5.6-Luna"
		model.Description = "Fast and affordable agentic coding model."
		model.Priority = 8
		model.InputModalities = []string{"text", "image"}
		model.ShellType = "shell_command"
		model.SupportVerbosity = true
		model.SupportsImageDetailOriginal = true
		model.SupportsReasoningSummaries = true
		model.SupportsSearchTool = true
		model.ToolMode = "code_mode_only"
		model.UseResponsesLite = true
		model.WebSearchToolType = "text_and_image"
		model.ApplyPatchToolType = "freeform"
		model.AvailableInPlans = append([]string(nil), catalogAvailablePlans...)
	case "gpt-5.5":
		model.DisplayName = "GPT-5.5"
		model.Description = "Proven previous-generation model for coding and general tasks."
		model.ContextWindow = 272000
		model.MaxContextWindow = 272000
		model.Priority = 12
		model.InputModalities = []string{"text", "image"}
		model.SupportsImageDetailOriginal = true
		model.ToolMode = ""
		model.UseResponsesLite = false
	case "gpt-5.4":
		model.DisplayName = "GPT-5.4"
		model.Description = "Strong model for everyday coding."
		model.ContextWindow = 272000
		model.MaxContextWindow = 1000000
		model.Priority = 16
		model.InputModalities = []string{"text", "image"}
		model.SupportsImageDetailOriginal = true
		model.ToolMode = ""
		model.UseResponsesLite = false
	case "gpt-5.4-mini":
		model.DisplayName = "GPT-5.4-Mini"
		model.Description = "Small, fast, and cost-efficient model for simpler coding tasks."
		model.ContextWindow = 272000
		model.MaxContextWindow = 272000
		model.Priority = 23
		model.InputModalities = []string{"text", "image"}
		model.SupportsImageDetailOriginal = true
		model.ToolMode = ""
		model.UseResponsesLite = false
	case "gpt-5.3-codex-spark":
		model.DisplayName = "GPT-5.3-Codex-Spark"
		model.Description = "Ultra-fast coding model."
		model.ContextWindow = 128000
		model.MaxContextWindow = 128000
		model.DefaultReasoningLevel = "high"
		model.Priority = 26
		model.InputModalities = []string{"text"}
		model.ToolMode = ""
		model.UseResponsesLite = false
	case "gpt-image-2":
		model.DisplayName = "GPT Image 2"
		model.Description = "GPT Image 2"
		model.ContextWindow = 272000
		model.MaxContextWindow = 1000000
		model.Priority = 1006
		model.Visibility = "hide"
		model.InputModalities = []string{"text", "image"}
		model.SupportsImageDetailOriginal = true
		model.SupportsSearchTool = false
		model.WebSearchToolType = ""
		model.ToolMode = ""
		model.UseResponsesLite = false
	case "codex-auto-review":
		model.DisplayName = "Codex Auto Review"
		model.Description = "Automatic approval review model for Codex."
		model.Visibility = "hide"
		model.Priority = 43
	}
	return model
}

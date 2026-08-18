package service

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

type antigravityWrappedModelProfile struct {
	upstreamModel  string
	thinkingBudget *int
	ensureThinking bool
}

func resolveAntigravityWrappedModelProfile(requestedModel, mappedModel string) antigravityWrappedModelProfile {
	requested := normalizeAntigravityCompatModel(requestedModel)
	mapped := normalizeAntigravityCompatModel(mappedModel)

	if isAntigravityGemini37FlashModel(mapped) {
		tier := mapped
		if isAntigravityGemini37FlashModel(requested) {
			tier = requested
		}

		profile := antigravityWrappedModelProfile{upstreamModel: domain.AntigravityGemini37FlashTieredModel}
		switch tier {
		case "gemini-3.7-flash-high":
			profile.thinkingBudget = antigravityIntPtr(-1)
		case "gemini-3.7-flash-low":
			profile.thinkingBudget = antigravityIntPtr(1000)
		case "gemini-3.7-flash", "gemini-3.7-flash-medium":
			profile.thinkingBudget = antigravityIntPtr(4000)
		}
		return profile
	}

	if mapped == "claude-sonnet-4-6-thinking" ||
		(mapped == "claude-sonnet-4-6" && requested == "claude-sonnet-4-6-thinking") {
		return antigravityWrappedModelProfile{
			upstreamModel:  "claude-sonnet-4-6",
			ensureThinking: true,
		}
	}

	return antigravityWrappedModelProfile{}
}

func applyAntigravityWrappedModelProfile(body []byte, profile antigravityWrappedModelProfile) ([]byte, error) {
	if profile.upstreamModel == "" {
		return body, nil
	}
	if !json.Valid(body) {
		return nil, fmt.Errorf("invalid Antigravity wrapped request")
	}

	currentModel := normalizeAntigravityCompatModel(gjson.GetBytes(body, "model").String())
	if !profileAppliesToWrappedModel(profile, currentModel) {
		// TransformClaudeToGemini may intentionally switch web-search requests to
		// another model. Do not undo that established fallback.
		return body, nil
	}

	updated, err := sjson.SetBytes(body, "model", profile.upstreamModel)
	if err != nil {
		return nil, fmt.Errorf("set Antigravity upstream model: %w", err)
	}
	if profile.thinkingBudget != nil {
		updated, err = sjson.SetBytes(updated, "request.generationConfig.thinkingConfig.thinkingBudget", *profile.thinkingBudget)
		if err != nil {
			return nil, fmt.Errorf("set Antigravity thinking budget: %w", err)
		}
	}
	if profile.ensureThinking {
		budgetPath := "request.generationConfig.thinkingConfig.thinkingBudget"
		if !gjson.GetBytes(updated, budgetPath).Exists() {
			updated, err = sjson.SetBytes(updated, budgetPath, -1)
			if err != nil {
				return nil, fmt.Errorf("enable Antigravity thinking budget: %w", err)
			}
		}
		includePath := "request.generationConfig.thinkingConfig.includeThoughts"
		if !gjson.GetBytes(updated, includePath).Exists() {
			updated, err = sjson.SetBytes(updated, includePath, true)
			if err != nil {
				return nil, fmt.Errorf("enable Antigravity thinking output: %w", err)
			}
		}
	}
	return updated, nil
}

func profileAppliesToWrappedModel(profile antigravityWrappedModelProfile, currentModel string) bool {
	if profile.upstreamModel == domain.AntigravityGemini37FlashTieredModel {
		return isAntigravityGemini37FlashModel(currentModel)
	}
	return currentModel == "claude-sonnet-4-6" || currentModel == "claude-sonnet-4-6-thinking"
}

func isAntigravityGemini37FlashModel(model string) bool {
	switch normalizeAntigravityCompatModel(model) {
	case "gemini-3.7-flash", "gemini-3.7-flash-high", "gemini-3.7-flash-medium", "gemini-3.7-flash-low", domain.AntigravityGemini37FlashTieredModel:
		return true
	default:
		return false
	}
}

func isAntigravityGemini37InternalModel(model string) bool {
	switch normalizeAntigravityCompatModel(model) {
	case "gemini-3.7-flash", domain.AntigravityGemini37FlashTieredModel:
		return true
	default:
		return false
	}
}

func publicAntigravityModelIDs(models []string) []string {
	publicModels := make([]string, 0, len(models)+2)
	for _, model := range models {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		if isAntigravityGemini37InternalModel(model) {
			publicModels = append(publicModels,
				"gemini-3.7-flash-high",
				"gemini-3.7-flash-medium",
				"gemini-3.7-flash-low",
			)
			continue
		}
		publicModels = append(publicModels, model)
	}
	return dedupeAndSortModelIDs(publicModels)
}

func antigravityWrappedRequestModel(body []byte) string {
	return strings.TrimSpace(gjson.GetBytes(body, "model").String())
}

func normalizeAntigravityCompatModel(model string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(model), "models/"))
}

func antigravityIntPtr(value int) *int {
	return &value
}

package service

import (
	"context"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
)

// resolveGrokTextModel is the single runtime model-resolution point for all
// Grok protocol adapters.  An explicit account mapping always wins.  The
// administrator's cross-client switch only treats well-known foreign-client
// aliases as requests for the configured default; arbitrary/custom Grok IDs
// remain pass-through so an account-level mapping or a future upstream model
// is never silently destroyed.
func (s *OpenAIGatewayService) resolveGrokTextModel(ctx context.Context, account *Account, requestedModel string) string {
	defaultModel := grokDefaultResponsesModel
	crossClient := true
	if s != nil && s.settingService != nil {
		if configured := strings.TrimSpace(s.settingService.GetGrokDefaultTextModel(ctx)); configured != "" {
			defaultModel = configured
		}
		crossClient = s.settingService.GetGrokCrossClientModelMapEnabled(ctx)
	}
	requestedModel = strings.TrimSpace(requestedModel)
	if account != nil {
		if mapped, matched := account.ResolveMappedModel(requestedModel); matched {
			return xai.ResolveGrokTextResponsesModelID(mapped, defaultModel)
		}
	}
	if requestedModel == "" || (crossClient && isGrokCrossClientModel(requestedModel)) {
		requestedModel = defaultModel
	}
	return xai.ResolveGrokTextResponsesModelID(requestedModel, defaultModel)
}

// isGrokCrossClientModel intentionally stays conservative.  These are client
// model families that the XIASS admin switch is designed to make usable on a
// Grok channel; unknown names must not be rewritten by a global setting.
func isGrokCrossClientModel(model string) bool {
	lower := strings.ToLower(strings.TrimSpace(xai.StripGrokProviderPrefix(model)))
	for _, prefix := range []string{"gpt-", "chatgpt-", "codex-", "o1", "o3", "o4", "claude-", "anthropic-"} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

func (s *OpenAIGatewayService) isGrokRequestModelSupported(ctx context.Context, account *Account, requestedModel string) bool {
	if account == nil {
		return false
	}
	if account.IsModelSupported(requestedModel) {
		return true
	}
	if !account.IsGrok() || len(account.GetModelMapping()) != 0 {
		return false
	}
	return s != nil && s.settingService != nil && s.settingService.GetGrokCrossClientModelMapEnabled(ctx) && isGrokCrossClientModel(requestedModel)
}

func (s *OpenAIGatewayService) canonicalGrokSchedulingModel(ctx context.Context, account *Account, requestedModel string) string {
	if account == nil || !account.IsGrok() {
		return canonicalOpenAIAccountSchedulingModel(account, requestedModel)
	}
	return s.resolveGrokTextModel(ctx, account, requestedModel)
}

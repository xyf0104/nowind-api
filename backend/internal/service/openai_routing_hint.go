package service

import (
	"context"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
	"golang.org/x/net/http/httpguts"
)

const openAICodexRoutingHintHeader = "x-codex-routing-hint"

// setOpenAICodexRoutingHint mirrors the Codex backend routing-hint contract for
// OpenAI OAuth requests. model and serviceTier must already be final upstream
// values after model mapping and local tier policy have run.
func setOpenAICodexRoutingHint(headers http.Header, account *Account, model, serviceTier string) {
	if headers == nil {
		return
	}

	// The hint is gateway-owned. Strip every spelling before synthesizing it so
	// neither client input nor API-key header overrides can forge routing state.
	deleteOpenAIHeaderEqualFold(headers, openAICodexRoutingHintHeader)
	if account == nil || !account.IsOpenAIOAuth() {
		return
	}

	model = strings.TrimSpace(model)
	if model == "" || strings.ContainsAny(model, ";=") {
		return
	}

	canonicalTier := normalizedOpenAIServiceTierValue(serviceTier)
	switch canonicalTier {
	case OpenAIFastTierPriority, OpenAIFastTierFlex, OpenAIFastTierUltrafast:
	default:
		canonicalTier = ""
	}

	hint := "model=" + model
	if canonicalTier != "" {
		hint += ";tier=" + canonicalTier
	}
	if !httpguts.ValidHeaderFieldValue(hint) {
		return
	}
	headers.Set(openAICodexRoutingHintHeader, hint)
}

func deleteOpenAIHeaderEqualFold(headers http.Header, name string) {
	if headers == nil {
		return
	}
	name = strings.TrimSpace(name)
	for key := range headers {
		if strings.EqualFold(strings.TrimSpace(key), name) {
			delete(headers, key)
		}
	}
}

func setOpenAICodexRoutingHintFromBody(headers http.Header, account *Account, body []byte) {
	fields := gjson.GetManyBytes(body, "model", "service_tier")
	setOpenAICodexRoutingHint(headers, account, fields[0].String(), fields[1].String())
}

// logOpenAIRoutingDiagnostics records gateway-derived routing state only. It
// intentionally excludes all raw header values and credentials.
func logOpenAIRoutingDiagnostics(
	ctx context.Context,
	account *Account,
	transport, model, serviceTier string,
	hintGenerated bool,
	wsAffinityDecision string,
) {
	if ctx == nil {
		ctx = context.Background()
	}
	accountID := int64(0)
	if account != nil {
		accountID = account.ID
	}
	logger.FromContext(ctx).Debug("openai routing decision",
		zap.String("component", "service.openai_routing"),
		zap.String("transport", strings.TrimSpace(transport)),
		zap.Int64("account_id", accountID),
		zap.String("final_model", strings.TrimSpace(model)),
		zap.String("final_service_tier", normalizedOpenAIServiceTierValue(serviceTier)),
		zap.Bool("routing_hint_generated", hintGenerated),
		zap.String("ws_affinity_decision", strings.TrimSpace(wsAffinityDecision)),
	)
}

func logOpenAIRoutingDiagnosticsFromBody(
	ctx context.Context,
	account *Account,
	transport string,
	headers http.Header,
	body []byte,
	wsAffinityDecision string,
) {
	fields := gjson.GetManyBytes(body, "model", "service_tier")
	logOpenAIRoutingDiagnostics(
		ctx,
		account,
		transport,
		fields[0].String(),
		fields[1].String(),
		strings.TrimSpace(headers.Get(openAICodexRoutingHintHeader)) != "",
		wsAffinityDecision,
	)
}

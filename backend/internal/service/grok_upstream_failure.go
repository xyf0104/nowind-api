package service

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/tidwall/gjson"
)

type GrokUpstreamFailureClass string

const (
	GrokFailureNone          GrokUpstreamFailureClass = ""
	GrokFailureFreeUsage     GrokUpstreamFailureClass = "subscription:free-usage-exhausted"
	GrokFailureBilling       GrokUpstreamFailureClass = "billing_quota"
	GrokFailureEmptyUpstream GrokUpstreamFailureClass = "empty_upstream"
	GrokFailureModelCapacity GrokUpstreamFailureClass = "model_capacity"
	GrokFailureRateLimit     GrokUpstreamFailureClass = "rate_limit"
	GrokFailureAuth          GrokUpstreamFailureClass = "auth_error"
	GrokFailureServer        GrokUpstreamFailureClass = "server_error"
)

type GrokUpstreamFailureDecision struct {
	Class          GrokUpstreamFailureClass
	Model          string
	Cooldown       time.Duration
	ShouldCooldown bool
	ShouldFailover bool
	BlockModel     bool
	Reason         string
	TokensActual   *int64
	TokensLimit    *int64
}

var (
	reGrokTokenPair = regexp.MustCompile(`(?i)tokens?\s*(?:\(actual\s*/\s*limit\))?\s*[:=]?\s*(\d+)\s*/\s*(\d+)`)
	reGrokModelFor  = regexp.MustCompile(`(?i)(?:for\s+model|model|模型)\s*[:：]?\s*([a-z0-9][a-z0-9._-]{2,80})`)
)

func classifyGrokUpstreamFailure(statusCode int, responseBody []byte, requestedModel string) GrokUpstreamFailureDecision {
	text, code, low := grokUpstreamErrorCorpus(statusCode, responseBody)
	model := extractGrokFailureModel(text, responseBody, requestedModel)
	actual, limit, hasTokens := parseGrokTokenPair(text)
	if !hasTokens {
		actual, limit, hasTokens = parseGrokTokenPair(string(responseBody))
	}

	if isGrokFreeUsageExhaustedText(low) || isGrokFreeUsageCode(code) || isGrokFreeUsageCode(text) {
		decision := GrokUpstreamFailureDecision{
			Class: GrokFailureFreeUsage, Model: model, Cooldown: grokFreeUsageCooldownDuration(low),
			ShouldCooldown: true, ShouldFailover: true, BlockModel: false, Reason: firstNonEmpty(text, code, "free usage exhausted"),
		}
		if hasTokens {
			actualCopy, limitCopy := actual, limit
			decision.TokensActual, decision.TokensLimit = &actualCopy, &limitCopy
		}
		return decision
	}
	if isGrokBillingQuotaText(low) || statusCode == http.StatusPaymentRequired {
		reason := firstNonEmpty(text, "billing quota")
		if statusCode == http.StatusPaymentRequired && text == "" {
			reason = "payment required"
		}
		return GrokUpstreamFailureDecision{Class: GrokFailureBilling, Model: model, Cooldown: 30 * time.Minute, ShouldCooldown: true, ShouldFailover: true, BlockModel: model != "", Reason: reason}
	}
	if isGrokEmptyModelOutputText(low) || isGrokEmptyModelOutputCode(code) {
		return GrokUpstreamFailureDecision{Class: GrokFailureEmptyUpstream, Model: model, Cooldown: 4 * time.Minute, ShouldCooldown: true, ShouldFailover: true, BlockModel: model != "", Reason: firstNonEmpty(text, "empty model output")}
	}
	if isGrokModelCapacityText(low) {
		return GrokUpstreamFailureDecision{Class: GrokFailureModelCapacity, Model: model, Cooldown: 3 * time.Minute, ShouldCooldown: true, ShouldFailover: true, Reason: firstNonEmpty(text, "model capacity")}
	}
	if statusCode == http.StatusTooManyRequests || isGrokRateLimitText(low) {
		return GrokUpstreamFailureDecision{Class: GrokFailureRateLimit, Model: model, Cooldown: 10 * time.Minute, ShouldCooldown: true, ShouldFailover: true, Reason: firstNonEmpty(text, "rate limit")}
	}
	if statusCode >= 500 && statusCode <= 599 {
		return GrokUpstreamFailureDecision{Class: GrokFailureServer, Cooldown: 2 * time.Minute, ShouldCooldown: true, ShouldFailover: true, Reason: firstNonEmpty(text, "server error")}
	}
	return GrokUpstreamFailureDecision{Reason: text}
}

func grokUpstreamErrorCorpus(statusCode int, responseBody []byte) (text, code, low string) {
	_ = statusCode
	raw := strings.TrimSpace(string(responseBody))
	if _, body, ok := unwrapGrokUpstreamErrorText(raw); ok {
		raw = body
	}
	text = raw
	codeFromJSON, messageFromJSON := parseGrokUpstreamErrorJSON(raw)
	if messageFromJSON != "" && (text == "" || len(messageFromJSON) > len(text)/2 || looksLikeGrokQuotaMessage(messageFromJSON)) {
		text = messageFromJSON
	}
	if len(responseBody) > 0 {
		message := strings.TrimSpace(firstNonEmpty(
			gjson.GetBytes(responseBody, "error.message").String(),
			gjson.GetBytes(responseBody, "message").String(),
			gjson.GetBytes(responseBody, "error").String(),
		))
		if message != "" && (text == "" || looksLikeGrokQuotaMessage(message)) {
			text = message
		}
		if structuredCode := strings.TrimSpace(firstNonEmpty(
			gjson.GetBytes(responseBody, "error.code").String(),
			gjson.GetBytes(responseBody, "code").String(),
		)); structuredCode != "" {
			codeFromJSON = structuredCode
		}
	}
	code = codeFromJSON
	low = strings.ToLower(strings.TrimSpace(text))
	if code != "" && !strings.Contains(low, strings.ToLower(code)) {
		low = strings.ToLower(code) + " " + low
	}
	return text, code, low
}

func unwrapGrokUpstreamErrorText(errText string) (status int, body string, ok bool) {
	text := strings.TrimSpace(errText)
	if text == "" {
		return 0, "", false
	}
	lower := strings.ToLower(text)
	for _, prefix := range []string{"upstream status ", "status "} {
		if !strings.HasPrefix(lower, prefix) {
			continue
		}
		rest := strings.TrimSpace(text[len(prefix):])
		index := 0
		for index < len(rest) && rest[index] >= '0' && rest[index] <= '9' {
			status = status*10 + int(rest[index]-'0')
			index++
		}
		if status <= 0 || index == 0 {
			return 0, "", false
		}
		rest = strings.TrimSpace(rest[index:])
		if strings.HasPrefix(rest, ":") {
			rest = strings.TrimSpace(rest[1:])
		}
		return status, rest, true
	}
	return 0, "", false
}

func parseGrokUpstreamErrorJSON(errText string) (code, message string) {
	text := strings.TrimSpace(errText)
	if text == "" || text[0] != '{' {
		return "", ""
	}
	var payload map[string]any
	if json.Unmarshal([]byte(text), &payload) != nil {
		return "", ""
	}
	code, _ = payload["code"].(string)
	message, _ = payload["message"].(string)
	if errObj, ok := payload["error"].(map[string]any); ok {
		if value, ok := errObj["code"].(string); ok && code == "" {
			code = value
		}
		if value, ok := errObj["message"].(string); ok && message == "" {
			message = value
		}
	}
	if errText, ok := payload["error"].(string); ok && message == "" {
		message = errText
	}
	return strings.TrimSpace(code), strings.TrimSpace(message)
}

func looksLikeGrokQuotaMessage(value string) bool {
	low := strings.ToLower(value)
	return strings.Contains(low, "quota") || strings.Contains(low, "usage") || strings.Contains(low, "credit") || strings.Contains(low, "额度") || strings.Contains(low, "free")
}

func isGrokFreeUsageCode(code string) bool {
	code = strings.ToLower(strings.TrimSpace(code))
	if code == "" {
		return false
	}
	if strings.Contains(code, "subscription:free-usage-exhausted") || strings.Contains(code, "free-usage-exhausted") ||
		strings.Contains(code, "free_usage_exhausted") || strings.Contains(code, "usage-limit-exceeded") || strings.Contains(code, "usage_limit_exceeded") {
		return true
	}
	return (strings.Contains(code, "free-usage") || strings.Contains(code, "free_usage")) &&
		(strings.Contains(code, "exhaust") || strings.Contains(code, "exceed") || strings.Contains(code, "limit"))
}

func isGrokFreeUsageExhaustedText(low string) bool {
	if low == "" {
		return false
	}
	for _, phrase := range []string{
		"free-usage-exhausted", "free_usage_exhausted", "subscription:free-usage", "usage-limit-exceeded", "usage_limit_exceeded",
		"free-tier-limit", "free_tier_limit", "free usage", "included free usage", "used all the included free",
		"you've used all the included free", "you have used all the included free", "free quota", "no remaining free", "out of free",
		"usage resets over a rolling", "额度耗尽", "额度用完", "额度不足", "额度已用尽", "额度已耗尽", "免费额度", "免费用量",
		"用量用完", "用量耗尽", "用量超限", "用量已用尽", "配额耗尽", "配额已用尽", "配额不足", "配额超限", "配额用完",
		"没有额度", "没额度", "无额度", "可用额度不足", "模型额度", "临时额度", "额度已满", "额度超限", "额度达到上限",
		"模型额度用完", "模型额度耗尽", "账号额度用完", "账号额度耗尽", "额度不够", "没额度了", "额度没了", "用完额度", "耗尽额度",
	} {
		if strings.Contains(low, phrase) {
			return true
		}
	}
	if strings.Contains(low, "free tier") && (strings.Contains(low, "exhaust") || strings.Contains(low, "limit") || strings.Contains(low, "exceed")) {
		return true
	}
	if (strings.Contains(low, "quota") && (strings.Contains(low, "exhaust") || strings.Contains(low, "exceed") || strings.Contains(low, "limit"))) ||
		(strings.Contains(low, "usage") && (strings.Contains(low, "exhaust") || strings.Contains(low, "exceed")) && (strings.Contains(low, "limit") || strings.Contains(low, "free") || strings.Contains(low, "model"))) {
		if strings.Contains(low, "free") || strings.Contains(low, "rolling") || strings.Contains(low, "24-hour") || strings.Contains(low, "24 hour") ||
			strings.Contains(low, "model") || strings.Contains(low, "subscription") || strings.Contains(low, "included") || strings.Contains(low, "tokens") {
			return true
		}
	}
	if actual, limit, ok := parseGrokTokenPair(low); ok && limit > 0 && actual >= limit {
		return strings.Contains(low, "free") || strings.Contains(low, "subscription") || strings.Contains(low, "included") ||
			strings.Contains(low, "model") || strings.Contains(low, "usage") || strings.Contains(low, "quota") || strings.Contains(low, "rolling")
	}
	return false
}

func isGrokBillingQuotaText(low string) bool {
	return strings.Contains(low, "insufficient_quota") ||
		(strings.Contains(low, "billing") && strings.Contains(low, "quota")) ||
		(strings.Contains(low, "payment") && (strings.Contains(low, "required") || strings.Contains(low, "fail"))) ||
		strings.Contains(low, "spending limit") || strings.Contains(low, "run out of credits") || strings.Contains(low, "out of credits") ||
		strings.Contains(low, "余额不足") || strings.Contains(low, "欠费") || strings.Contains(low, "需要付费")
}

func isGrokModelCapacityText(low string) bool {
	return strings.Contains(low, "capacity") || strings.Contains(low, "overloaded") || strings.Contains(low, "server_busy") ||
		strings.Contains(low, "too many concurrent") || strings.Contains(low, "engine_overloaded")
}

func isGrokRateLimitText(low string) bool {
	return strings.Contains(low, "rate limit") || strings.Contains(low, "rate_limit") || strings.Contains(low, "too many requests") ||
		strings.Contains(low, "请求过于频繁") || strings.Contains(low, "速率限制")
}

func isGrokEmptyModelOutputText(low string) bool {
	return strings.Contains(low, "empty model output") || strings.Contains(low, "no content/tool_calls") ||
		strings.Contains(low, "no client-visible content") || strings.Contains(low, "empty_upstream") || strings.Contains(low, "empty upstream")
}

func isGrokEmptyModelOutputCode(code string) bool {
	code = strings.ToLower(strings.TrimSpace(code))
	return code == "empty_upstream" || code == "empty-model-output" || code == "empty_model_output" ||
		strings.Contains(code, "empty_upstream") || strings.Contains(code, "empty-model-output")
}

func grokFreeUsageCooldownDuration(_ string) time.Duration {
	return grokFreeUsageProbeCooldown
}

const grokFreeUsageProbeCooldown = 10 * time.Minute

func parseGrokTokenPair(errText string) (actual, limit int64, ok bool) {
	match := reGrokTokenPair.FindStringSubmatch(errText)
	if len(match) != 3 {
		return 0, 0, false
	}
	actual, errA := strconv.ParseInt(match[1], 10, 64)
	limit, errB := strconv.ParseInt(match[2], 10, 64)
	return actual, limit, errA == nil && errB == nil
}

func extractGrokFailureModel(text string, responseBody []byte, fallback string) string {
	if match := reGrokModelFor.FindStringSubmatch(text); len(match) == 2 {
		return normalizeGrokFailureModelID(match[1])
	}
	if len(responseBody) > 0 {
		if model := strings.TrimSpace(firstNonEmpty(gjson.GetBytes(responseBody, "error.model").String(), gjson.GetBytes(responseBody, "model").String())); model != "" {
			return normalizeGrokFailureModelID(model)
		}
	}
	return normalizeGrokFailureModelID(fallback)
}

func normalizeGrokFailureModelID(model string) string {
	return strings.TrimSpace(strings.TrimRight(strings.TrimSpace(model), ".,;:!?"))
}

func isGrokHeavyTransientModel(requestedModel string) bool {
	model := strings.ToLower(strings.TrimSpace(requestedModel))
	return strings.Contains(model, "multi-agent")
}

func persistGrokTransientModelCooldown(account *Account, decision GrokUpstreamFailureDecision) bool {
	if account == nil {
		return false
	}
	model := strings.TrimSpace(decision.Model)
	if model == "" || !isGrokHeavyTransientModel(model) {
		return false
	}
	cooldown := decision.Cooldown
	if cooldown <= 0 {
		cooldown = 3 * time.Minute
	}
	markGrokModelTransientBlock(account.ID, model, time.Now().Add(cooldown))
	return true
}

func (s *OpenAIGatewayService) applyGrokUpstreamFailureDecision(ctx context.Context, account *Account, decision GrokUpstreamFailureDecision) bool {
	if s == nil || account == nil || !decision.ShouldCooldown || decision.Cooldown <= 0 {
		return false
	}
	var reason string
	switch decision.Class {
	case GrokFailureFreeUsage:
		reason = "grok free usage exhausted"
		if decision.Model != "" && isGrokModelSpecificFreeUsage(strings.ToLower(decision.Reason), decision.Model) {
			markGrokModelQuotaBlock(account.ID, decision.Model, time.Now().Add(decision.Cooldown))
			return true
		}
	case GrokFailureBilling:
		low := strings.ToLower(decision.Reason)
		if strings.Contains(low, "spending") || strings.Contains(low, "credits") {
			s.rateLimitGrok(ctx, account, grokSpendingLimitResetAt(account, time.Now()))
			return true
		}
		reason = "grok payment required"
	case GrokFailureEmptyUpstream:
		reason = "grok empty model output"
	case GrokFailureModelCapacity:
		if persistGrokTransientModelCooldown(account, decision) {
			return true
		}
		reason = "grok model capacity"
	case GrokFailureRateLimit:
		return false
	case GrokFailureServer:
		reason = "grok upstream temporary error"
	default:
		return false
	}
	s.tempUnscheduleGrok(ctx, account, decision.Cooldown, reason)
	return true
}

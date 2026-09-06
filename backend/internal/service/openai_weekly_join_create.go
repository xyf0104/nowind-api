package service

import (
	"context"
	"time"
)

const openAIWeeklyJoinCaptureTimeout = 5 * time.Second

type openAIWeeklyJoinCaptureDeadlineKey struct{}

// The deadline bounds optional upstream reads only. It must not cancel the
// caller's database/import context when the shared batch budget is exhausted.
func WithOpenAIWeeklyJoinCaptureBudget(ctx context.Context, budget time.Duration) context.Context {
	deadline := time.Now().Add(budget)
	if previous, ok := ctx.Value(openAIWeeklyJoinCaptureDeadlineKey{}).(time.Time); ok && previous.Before(deadline) {
		deadline = previous
	}
	return context.WithValue(ctx, openAIWeeklyJoinCaptureDeadlineKey{}, deadline)
}

type openAIWeeklyJoinQuotaWindow struct {
	UsedPercent        *float64 `json:"used_percent"`
	LimitWindowSeconds int64    `json:"limit_window_seconds"`
	ResetAt            int64    `json:"reset_at"`
	ResetAfterSeconds  int64    `json:"reset_after_seconds"`
}

type openAIWeeklyJoinQuotaPayload struct {
	AccountID string `json:"account_id"`
	UserID    string `json:"user_id"`
	RateLimit *struct {
		PrimaryWindow   *openAIWeeklyJoinQuotaWindow `json:"primary_window"`
		SecondaryWindow *openAIWeeklyJoinQuotaWindow `json:"secondary_window"`
	} `json:"rate_limit"`
}

// Read the quota before the row can be scheduled. A new local row has no local
// spending yet; imported client snapshots cannot establish that fact. Failure
// does not change the requested scheduling setting or prevent account creation.
func (s *adminServiceImpl) captureOpenAIWeeklyJoinForCreate(ctx context.Context, account *Account) {
	if account == nil || account.ID != 0 {
		return
	}
	observation := s.readOpenAIWeeklyJoinObservation(ctx, account)
	if observation == nil {
		return
	}
	identity := observation.account.GetCredential("chatgpt_account_id")
	state, ok := newOpenAIWeeklyFrozenEstimateStateFromBaseline(observation.percent, 0, observation.resetAt, identity, observation.observedAt, "pre_create_quota_read")
	if !ok {
		return
	}
	updates := openAIWeeklyFrozenEstimateStateUpdate(state)
	baseline, ok := updates[openAIWeeklyEstimateBaselineKey].(map[string]any)
	if !ok {
		return
	}
	baseline["join_evidence"] = map[string]any{
		"kind": "pre_create_quota_read", "identity": identity, "percent": observation.percent, "cost": 0.0,
		"observed_at": observation.observedAt.Format(time.RFC3339Nano), "reset_at": observation.resetAt.Format(time.RFC3339Nano),
	}
	mergeAccountExtra(account, updates)
	mergeAccountExtra(account, observation.rawUpdates())
}

type openAIWeeklyJoinObservation struct {
	account    *Account
	percent    float64
	resetAt    time.Time
	observedAt time.Time
}

func (o *openAIWeeklyJoinObservation) rawUpdates() map[string]any {
	return map[string]any{
		"codex_7d_used_percent":  o.percent,
		"codex_7d_reset_at":      o.resetAt.Format(time.RFC3339Nano),
		"codex_usage_updated_at": o.observedAt.Format(time.RFC3339Nano),
	}
}

// Reads only the identity snapshot supplied by the entry point. Reused rows
// consume the RAW observation, never the new-row zero-local-cost assumption.
func (s *adminServiceImpl) readOpenAIWeeklyJoinObservation(ctx context.Context, account *Account) *openAIWeeklyJoinObservation {
	if s == nil || s.privacyClientFactory == nil || account == nil ||
		!account.IsOpenAIOAuth() || account.IsShadow() || account.IsOpenAIAgentIdentity() {
		return nil
	}
	expected, err := cloneOpenAICodexSnapshotIdentity(account)
	if err != nil {
		return nil
	}
	identity, token := expected.GetCredential("chatgpt_account_id"), expected.GetOpenAIAccessToken()
	if identity == "" || token == "" {
		return nil
	}
	deadline := time.Now().Add(openAIWeeklyJoinCaptureTimeout)
	if budget, ok := ctx.Value(openAIWeeklyJoinCaptureDeadlineKey{}).(time.Time); ok && budget.Before(deadline) {
		deadline = budget
	}
	if !time.Now().Before(deadline) {
		return nil
	}
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	proxyURL := ""
	if expected.ProxyID != nil {
		if s.proxyRepo == nil {
			return nil
		}
		proxy, err := s.proxyRepo.GetByID(ctx, *expected.ProxyID)
		if err != nil || proxy == nil || proxy.ID != *expected.ProxyID || proxy.URL() == "" {
			return nil
		}
		proxyURL = proxy.URL()
	}
	client, err := s.privacyClientFactory(proxyURL)
	if err != nil || client == nil {
		return nil
	}
	var payload openAIWeeklyJoinQuotaPayload
	resp, err := client.R().SetContext(ctx).
		SetHeaders(buildCodexCommonHeaders(token, identity, expected.IsChatGPTAccountFedRAMP())).
		SetSuccessResult(&payload).Get(chatGPTUsageURL)
	if err != nil || resp == nil || !resp.IsSuccessState() || payload.RateLimit == nil ||
		(payload.AccountID != "" && payload.AccountID != identity) ||
		(payload.UserID != "" && expected.GetCredential("chatgpt_user_id") != "" && payload.UserID != expected.GetCredential("chatgpt_user_id")) {
		return nil
	}
	current, err := cloneOpenAICodexSnapshotIdentity(account)
	if err != nil || !sameOpenAIWeeklyJoinRequestIdentity(expected, current) {
		return nil
	}
	observed := time.Now().UTC().Truncate(time.Microsecond)
	var weekly *openAIWeeklyJoinQuotaWindow
	for _, window := range []*openAIWeeklyJoinQuotaWindow{payload.RateLimit.PrimaryWindow, payload.RateLimit.SecondaryWindow} {
		if window == nil || window.LimitWindowSeconds != int64((7*24*time.Hour)/time.Second) {
			continue
		}
		if weekly != nil {
			return nil
		}
		weekly = window
	}
	if weekly == nil || weekly.UsedPercent == nil || !validOpenAIWeeklyEstimateValue(*weekly.UsedPercent) || *weekly.UsedPercent >= 100 {
		return nil
	}
	reset := time.Time{}
	if weekly.ResetAt > 0 {
		reset = time.Unix(weekly.ResetAt, 0).UTC()
	} else if weekly.ResetAfterSeconds > 0 && weekly.ResetAfterSeconds <= 7*24*60*60 {
		reset = observed.Add(time.Duration(weekly.ResetAfterSeconds) * time.Second)
	}
	if !reset.After(observed) || reset.Sub(observed) > 7*24*time.Hour+time.Minute {
		return nil
	}
	return &openAIWeeklyJoinObservation{account: expected, percent: *weekly.UsedPercent, resetAt: reset, observedAt: observed}
}

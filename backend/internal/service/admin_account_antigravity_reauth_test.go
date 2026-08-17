//go:build unit

package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

type antigravityReauthRepo struct {
	mockAccountRepoForGemini
	account           *Account
	credentialUpdates []map[string]any
	extraUpdates      []map[string]any
}

func (r *antigravityReauthRepo) GetByID(context.Context, int64) (*Account, error) {
	return r.account, nil
}

func (r *antigravityReauthRepo) UpdateCredentials(_ context.Context, _ int64, credentials map[string]any) error {
	r.account.Credentials = shallowCopyMap(credentials)
	r.credentialUpdates = append(r.credentialUpdates, shallowCopyMap(credentials))
	return nil
}

func (r *antigravityReauthRepo) UpdateExtra(_ context.Context, _ int64, updates map[string]any) error {
	if r.account.Extra == nil {
		r.account.Extra = make(map[string]any)
	}
	copy := shallowCopyMap(updates)
	for key, value := range copy {
		r.account.Extra[key] = value
	}
	r.extraUpdates = append(r.extraUpdates, copy)
	return nil
}

func TestApplyAntigravityOAuthCredentialsPreservesConfigAndReplacesIdentity(t *testing.T) {
	const previousVersion = int64(9_000_000_000_000)
	repo := &antigravityReauthRepo{account: &Account{
		ID:       104,
		Platform: PlatformAntigravity,
		Type:     AccountTypeOAuth,
		Status:   StatusActive,
		Credentials: map[string]any{
			"access_token":              "old-access",
			"refresh_token":             "old-refresh",
			"email":                     "old@example.com",
			"project_id":                "old-project",
			"plan_type":                 "old-plan",
			"scope":                     "old-scope",
			"_token_version":            previousVersion,
			"model_mapping":             map[string]any{"custom": "upstream-custom"},
			"antigravity_project_id":    "manual-fallback",
			"intercept_warmup_requests": true,
		},
		Extra: map[string]any{
			"privacy_mode":           AntigravityPrivacySet,
			"window_cost_limit":      12.5,
			"antigravity_ai_credits": true,
		},
	}}

	updated, err := (&adminServiceImpl{accountRepo: repo}).ApplyAntigravityOAuthCredentials(
		context.Background(),
		104,
		AntigravityOAuthCredentialsInput{
			Type: AccountTypeOAuth,
			Credentials: map[string]any{
				"access_token":  "new-access",
				"refresh_token": "new-refresh",
				"token_type":    "Bearer",
				"expires_at":    "1900000000",
				"email":         "new@example.com",
				"project_id":    "new-project",
				"plan_type":     "pro",
				"model_mapping": map[string]any{"attacker": "must-not-replace-config"},
				"privacy_mode":  "must-not-enter-credentials",
			},
			PrivacyMode: AntigravityPrivacySet,
		},
	)

	require.NoError(t, err)
	require.Same(t, repo.account, updated)
	require.Len(t, repo.credentialUpdates, 1)
	require.Equal(t, "new-access", updated.Credentials["access_token"])
	require.Equal(t, "new-refresh", updated.Credentials["refresh_token"])
	require.Equal(t, "new@example.com", updated.Credentials["email"])
	require.Equal(t, "new-project", updated.Credentials["project_id"])
	require.Equal(t, "pro", updated.Credentials["plan_type"])
	require.NotContains(t, updated.Credentials, "scope", "an omitted identity field must not survive from the old principal")
	require.Equal(t, map[string]any{"custom": "upstream-custom"}, updated.Credentials["model_mapping"])
	require.Equal(t, "manual-fallback", updated.Credentials["antigravity_project_id"])
	require.Equal(t, true, updated.Credentials["intercept_warmup_requests"])
	require.Equal(t, previousVersion+1, updated.GetCredentialAsInt64("_token_version"))
	require.Equal(t, 12.5, updated.Extra["window_cost_limit"])
	require.Equal(t, true, updated.Extra["antigravity_ai_credits"])
	require.Len(t, repo.extraUpdates, 2)
	require.Equal(t, AntigravityPrivacyFailed, repo.extraUpdates[0]["privacy_mode"])
	require.Equal(t, AntigravityPrivacySet, repo.extraUpdates[1]["privacy_mode"])
}

func TestApplyAntigravityOAuthCredentialsRequiresFreshRefreshToken(t *testing.T) {
	repo := &antigravityReauthRepo{account: &Account{
		ID:          105,
		Platform:    PlatformAntigravity,
		Type:        AccountTypeOAuth,
		Credentials: map[string]any{"refresh_token": "old-refresh"},
	}}

	_, err := (&adminServiceImpl{accountRepo: repo}).ApplyAntigravityOAuthCredentials(
		context.Background(),
		105,
		AntigravityOAuthCredentialsInput{
			Type:        AccountTypeOAuth,
			Credentials: map[string]any{"access_token": "new-access"},
		},
	)

	require.Error(t, err)
	require.Empty(t, repo.credentialUpdates)
	require.Empty(t, repo.extraUpdates)
	require.Equal(t, "old-refresh", repo.account.Credentials["refresh_token"])
}

func TestBuildAntigravityReauthorizedCredentialsClearsOmittedIdentityFields(t *testing.T) {
	credentials, err := buildAntigravityReauthorizedCredentials(
		map[string]any{
			"access_token":   "old-access",
			"refresh_token":  "old-refresh",
			"email":          "old@example.com",
			"project_id":     "old-project",
			"plan_type":      "old-plan",
			"model_mapping":  map[string]any{"custom": "custom"},
			"_token_version": int64(7),
		},
		map[string]any{
			"access_token":  "new-access",
			"refresh_token": "new-refresh",
		},
	)

	require.NoError(t, err)
	require.NotContains(t, credentials, "email")
	require.NotContains(t, credentials, "project_id")
	require.NotContains(t, credentials, "plan_type")
	require.Equal(t, map[string]any{"custom": "custom"}, credentials["model_mapping"])
}

func TestAntigravityTokenInfoExposesReauthorizationMetadata(t *testing.T) {
	payload, err := json.Marshal(AntigravityTokenInfo{
		PlanType:    "pro",
		PrivacyMode: AntigravityPrivacySet,
	})
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(payload, &decoded))
	require.Equal(t, "pro", decoded["plan_type"])
	require.Equal(t, AntigravityPrivacySet, decoded["privacy_mode"])
}

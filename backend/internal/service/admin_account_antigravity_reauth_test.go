//go:build unit

package service

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type antigravityReauthRepo struct {
	mockAccountRepoForGemini
	account           *Account
	credentialUpdates []map[string]any
	extraUpdates      []map[string]any
	beforeReauthCAS   func(*antigravityReauthRepo)
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

func (r *antigravityReauthRepo) UpdateAntigravityOAuthCredentialsAndPrivacyIfUnchanged(
	_ context.Context,
	id int64,
	expectedCredentials map[string]any,
	expectedProxyID *int64,
	_ *time.Time,
	credentials map[string]any,
) (bool, error) {
	if r.beforeReauthCAS != nil {
		r.beforeReauthCAS(r)
	}
	if r.account == nil || r.account.ID != id ||
		!reflect.DeepEqual(r.account.Credentials, expectedCredentials) ||
		!reflect.DeepEqual(r.account.ProxyID, expectedProxyID) {
		return false, nil
	}
	r.account.Credentials = shallowCopyMap(credentials)
	r.credentialUpdates = append(r.credentialUpdates, shallowCopyMap(credentials))
	if err := r.UpdateExtra(context.Background(), r.account.ID, map[string]any{"privacy_mode": AntigravityPrivacyFailed}); err != nil {
		return false, err
	}
	return true, nil
}

func TestApplyAntigravityOAuthCredentialsRejectsStaleConcurrentReauthorization(t *testing.T) {
	repo := &antigravityReauthRepo{account: &Account{
		ID:          106,
		Platform:    PlatformAntigravity,
		Type:        AccountTypeOAuth,
		Credentials: map[string]any{"access_token": "old", "refresh_token": "old-refresh"},
	}}
	repo.beforeReauthCAS = func(repo *antigravityReauthRepo) {
		repo.account.Credentials = map[string]any{"access_token": "new", "refresh_token": "new-refresh"}
	}

	updated, err := (&adminServiceImpl{accountRepo: repo}).ApplyAntigravityOAuthCredentials(
		context.Background(),
		106,
		AntigravityOAuthCredentialsInput{
			Type: AccountTypeOAuth,
			Credentials: map[string]any{
				"access_token":  "stale-access",
				"refresh_token": "stale-refresh",
			},
		},
	)

	require.Error(t, err)
	require.Nil(t, updated)
	require.Empty(t, repo.credentialUpdates)
	require.Equal(t, "new-refresh", repo.account.Credentials["refresh_token"])
}

func (r *antigravityReauthRepo) SetAntigravityOAuthRefreshErrorIfCredentialsUnchanged(context.Context, int64, map[string]any, *int64, *time.Time, string) (bool, error) {
	return true, nil
}

func (r *antigravityReauthRepo) SetAntigravityOAuthRefreshTempUnschedulableIfCredentialsUnchanged(context.Context, int64, map[string]any, *int64, *time.Time, time.Time, string) (bool, error) {
	return true, nil
}

func (r *antigravityReauthRepo) UpdateAntigravityOAuthRefreshExtraIfCredentialsUnchanged(ctx context.Context, _ int64, _ map[string]any, _ *int64, _ *time.Time, updates map[string]any) (bool, error) {
	if err := r.UpdateExtra(ctx, r.account.ID, updates); err != nil {
		return false, err
	}
	return true, nil
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

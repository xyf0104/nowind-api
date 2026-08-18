//go:build unit

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func newAntigravityCASAccount(id int64) *Account {
	return &Account{
		ID:          id,
		Platform:    PlatformAntigravity,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Schedulable: true,
		Credentials: map[string]any{
			"access_token":   "attempt-access",
			"refresh_token":  "attempt-refresh",
			"_token_version": int64(1),
		},
	}
}

func TestAntigravityTokenProviderProjectBackfillUsesCredentialCAS(t *testing.T) {
	account := newAntigravityCASAccount(801)
	repo := &tokenRefreshAccountRepo{}
	repo.accountsByID = map[int64]*Account{account.ID: account}
	provider := NewAntigravityTokenProvider(repo, nil, nil)

	applied, err := provider.persistProjectIDIfUnchanged(context.Background(), account, "project-a")

	require.NoError(t, err)
	require.True(t, applied)
	require.Equal(t, "project-a", account.GetCredential("project_id"))
	require.Equal(t, 1, repo.conditionalSuccessCalls)
}

func TestAntigravityTokenProviderProjectBackfillCannotOverwriteReauthorization(t *testing.T) {
	attempted := newAntigravityCASAccount(802)
	current := snapshotOAuthRefreshAccount(attempted)
	current.Credentials = map[string]any{
		"access_token":   "reauthorized-access",
		"refresh_token":  "reauthorized-refresh",
		"project_id":     "reauthorized-project",
		"_token_version": int64(2),
	}
	repo := &tokenRefreshAccountRepo{}
	repo.accountsByID = map[int64]*Account{current.ID: current}
	provider := NewAntigravityTokenProvider(repo, nil, nil)

	applied, err := provider.persistProjectIDIfUnchanged(context.Background(), attempted, "late-project")

	require.NoError(t, err)
	require.False(t, applied)
	require.Equal(t, "reauthorized-project", current.GetCredential("project_id"))
	require.Empty(t, attempted.GetCredential("project_id"))
}

func TestAntigravityTokenProviderTempUnschedulableUsesCredentialCAS(t *testing.T) {
	t.Run("matching identity is quarantined", func(t *testing.T) {
		account := newAntigravityCASAccount(803)
		repo := &tokenRefreshAccountRepo{}
		repo.accountsByID = map[int64]*Account{account.ID: account}
		provider := NewAntigravityTokenProvider(repo, nil, nil)

		provider.markTempUnschedulable(snapshotOAuthRefreshAccount(account), errors.New("refresh timeout"))

		require.Equal(t, 1, repo.conditionalTempCalls)
		require.Equal(t, 1, repo.setTempUnschedCalls)
		require.NotNil(t, account.TempUnschedulableUntil)
	})

	t.Run("stale identity cannot quarantine reauthorization", func(t *testing.T) {
		attempted := newAntigravityCASAccount(804)
		current := snapshotOAuthRefreshAccount(attempted)
		current.Credentials = map[string]any{
			"access_token":   "reauthorized-access",
			"refresh_token":  "reauthorized-refresh",
			"_token_version": int64(2),
		}
		repo := &tokenRefreshAccountRepo{}
		repo.accountsByID = map[int64]*Account{current.ID: current}
		provider := NewAntigravityTokenProvider(repo, nil, nil)

		provider.markTempUnschedulable(attempted, errors.New("late refresh timeout"))

		require.Equal(t, 1, repo.conditionalTempCalls)
		require.Zero(t, repo.setTempUnschedCalls)
		require.Nil(t, current.TempUnschedulableUntil)
	})
}

func TestUpdateAntigravityOAuthExtraIfCredentialsUnchanged(t *testing.T) {
	attempted := newAntigravityCASAccount(805)
	current := snapshotOAuthRefreshAccount(attempted)
	repo := &tokenRefreshAccountRepo{}
	repo.accountsByID = map[int64]*Account{current.ID: current}

	applied, err := updateAntigravityOAuthExtraIfCredentialsUnchanged(
		context.Background(), repo, attempted, map[string]any{"privacy_mode": AntigravityPrivacySet},
	)
	require.NoError(t, err)
	require.True(t, applied)
	require.Equal(t, AntigravityPrivacySet, current.Extra["privacy_mode"])

	stale := snapshotOAuthRefreshAccount(current)
	current.Credentials = map[string]any{
		"access_token":   "reauthorized-access",
		"refresh_token":  "reauthorized-refresh",
		"_token_version": int64(2),
	}
	applied, err = updateAntigravityOAuthExtraIfCredentialsUnchanged(
		context.Background(), repo, stale, map[string]any{"privacy_mode": AntigravityPrivacyFailed},
	)
	require.NoError(t, err)
	require.False(t, applied)
	require.Equal(t, AntigravityPrivacySet, current.Extra["privacy_mode"])
}

func TestAntigravityTokenProvider_GetAccessToken_Upstream(t *testing.T) {
	provider := &AntigravityTokenProvider{}

	t.Run("upstream account with valid api_key", func(t *testing.T) {
		account := &Account{
			Platform: PlatformAntigravity,
			Type:     AccountTypeUpstream,
			Credentials: map[string]any{
				"api_key": "sk-test-key-12345",
			},
		}
		token, err := provider.GetAccessToken(context.Background(), account)
		require.NoError(t, err)
		require.Equal(t, "sk-test-key-12345", token)
	})

	t.Run("upstream account missing api_key", func(t *testing.T) {
		account := &Account{
			Platform:    PlatformAntigravity,
			Type:        AccountTypeUpstream,
			Credentials: map[string]any{},
		}
		token, err := provider.GetAccessToken(context.Background(), account)
		require.Error(t, err)
		require.Contains(t, err.Error(), "upstream account missing api_key")
		require.Empty(t, token)
	})

	t.Run("upstream account with empty api_key", func(t *testing.T) {
		account := &Account{
			Platform: PlatformAntigravity,
			Type:     AccountTypeUpstream,
			Credentials: map[string]any{
				"api_key": "",
			},
		}
		token, err := provider.GetAccessToken(context.Background(), account)
		require.Error(t, err)
		require.Contains(t, err.Error(), "upstream account missing api_key")
		require.Empty(t, token)
	})

	t.Run("upstream account with nil credentials", func(t *testing.T) {
		account := &Account{
			Platform: PlatformAntigravity,
			Type:     AccountTypeUpstream,
		}
		token, err := provider.GetAccessToken(context.Background(), account)
		require.Error(t, err)
		require.Contains(t, err.Error(), "upstream account missing api_key")
		require.Empty(t, token)
	})
}

func TestAntigravityTokenProvider_GetAccessToken_Guards(t *testing.T) {
	provider := &AntigravityTokenProvider{}

	t.Run("nil account", func(t *testing.T) {
		token, err := provider.GetAccessToken(context.Background(), nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "account is nil")
		require.Empty(t, token)
	})

	t.Run("non-antigravity platform", func(t *testing.T) {
		account := &Account{
			Platform: PlatformAnthropic,
			Type:     AccountTypeOAuth,
		}
		token, err := provider.GetAccessToken(context.Background(), account)
		require.Error(t, err)
		require.Contains(t, err.Error(), "not an antigravity account")
		require.Empty(t, token)
	})

	t.Run("unsupported account type", func(t *testing.T) {
		account := &Account{
			Platform: PlatformAntigravity,
			Type:     AccountTypeAPIKey,
		}
		token, err := provider.GetAccessToken(context.Background(), account)
		require.Error(t, err)
		require.Contains(t, err.Error(), "not an antigravity oauth account")
		require.Empty(t, token)
	})
}

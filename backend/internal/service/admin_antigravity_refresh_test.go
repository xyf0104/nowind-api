//go:build unit

package service

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type adminAntigravityRefreshCASRepo struct {
	mockAccountRepoForGemini
	current             *Account
	beforeCAS           func(*adminAntigravityRefreshCASRepo)
	casCalls            int
	expectedCredentials map[string]any
	expectedProxyID     *int64
}

func (r *adminAntigravityRefreshCASRepo) GetByID(_ context.Context, _ int64) (*Account, error) {
	return snapshotOAuthRefreshAccount(r.current), nil
}

func (r *adminAntigravityRefreshCASRepo) UpdateAntigravityOAuthCredentialsIfUnchanged(
	_ context.Context,
	id int64,
	expectedCredentials map[string]any,
	expectedProxyID *int64,
	_ *time.Time,
	credentials map[string]any,
) (bool, error) {
	r.casCalls++
	r.expectedCredentials = shallowCopyMap(expectedCredentials)
	r.expectedProxyID = cloneInt64Pointer(expectedProxyID)
	if r.beforeCAS != nil {
		r.beforeCAS(r)
	}
	if r.current == nil || r.current.ID != id || r.current.Platform != PlatformAntigravity ||
		r.current.Type != AccountTypeOAuth ||
		!reflect.DeepEqual(r.current.Credentials, expectedCredentials) ||
		!reflect.DeepEqual(r.current.ProxyID, expectedProxyID) {
		return false, nil
	}
	r.current.Credentials = shallowCopyMap(credentials)
	return true, nil
}

func TestApplyAntigravityOAuthRefreshIfUnchangedPersistsWithMonotonicVersion(t *testing.T) {
	proxyID := int64(41)
	current := &Account{
		ID:       104,
		Platform: PlatformAntigravity,
		Type:     AccountTypeOAuth,
		ProxyID:  &proxyID,
		Credentials: map[string]any{
			"access_token":   "old-access",
			"refresh_token":  "old-refresh",
			"_token_version": int64(9_999_999_999_999),
		},
	}
	repo := &adminAntigravityRefreshCASRepo{current: current}
	svc := &adminServiceImpl{accountRepo: repo}
	attempted := snapshotOAuthRefreshAccount(current)

	updated, applied, err := svc.ApplyAntigravityOAuthRefreshIfUnchanged(
		context.Background(),
		attempted,
		map[string]any{"access_token": "new-access", "refresh_token": "new-refresh"},
	)

	require.NoError(t, err)
	require.True(t, applied)
	require.Equal(t, 1, repo.casCalls)
	require.Equal(t, "old-refresh", repo.expectedCredentials["refresh_token"])
	require.Equal(t, &proxyID, repo.expectedProxyID)
	require.Equal(t, "new-refresh", updated.GetCredential("refresh_token"))
	require.Greater(t, updated.GetCredentialAsInt64("_token_version"), int64(9_999_999_999_999))
}

func TestApplyAntigravityOAuthRefreshIfUnchangedReturnsConcurrentReauthorization(t *testing.T) {
	proxyID := int64(42)
	current := &Account{
		ID:       105,
		Platform: PlatformAntigravity,
		Type:     AccountTypeOAuth,
		ProxyID:  &proxyID,
		Credentials: map[string]any{
			"access_token":   "old-access",
			"refresh_token":  "old-refresh",
			"_token_version": int64(1),
		},
	}
	repo := &adminAntigravityRefreshCASRepo{current: current}
	repo.beforeCAS = func(repo *adminAntigravityRefreshCASRepo) {
		repo.current.Credentials = map[string]any{
			"access_token":   "reauthorized-access",
			"refresh_token":  "reauthorized-refresh",
			"_token_version": int64(2),
		}
	}
	svc := &adminServiceImpl{accountRepo: repo}
	attempted := snapshotOAuthRefreshAccount(current)

	updated, applied, err := svc.ApplyAntigravityOAuthRefreshIfUnchanged(
		context.Background(),
		attempted,
		map[string]any{"access_token": "late-access", "refresh_token": "late-refresh"},
	)

	require.NoError(t, err)
	require.False(t, applied)
	require.Equal(t, "reauthorized-refresh", updated.GetCredential("refresh_token"))
	require.Equal(t, int64(2), updated.GetCredentialAsInt64("_token_version"))
}

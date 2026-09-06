//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type issuanceCacheStub struct {
	RefreshTokenCache
	stored   *RefreshTokenData
	storeErr error
	calls    []string
}

func (s *issuanceCacheStub) StoreRefreshToken(_ context.Context, _ string, data *RefreshTokenData, _ time.Duration) error {
	s.calls = append(s.calls, "store")
	s.stored = data
	return s.storeErr
}
func (s *issuanceCacheStub) AddToUserTokenSet(context.Context, int64, string, time.Duration) error {
	s.calls = append(s.calls, "user")
	return nil
}
func (s *issuanceCacheStub) AddToFamilyTokenSet(context.Context, string, string, time.Duration) error {
	s.calls = append(s.calls, "family")
	return nil
}

type issuancePreparerStub struct {
	*issuanceCacheStub
	ticket *RefreshTokenIssuance
	err    error
}

func (s *issuancePreparerStub) PrepareRefreshTokenIssuance(context.Context, int64) (*RefreshTokenIssuance, error) {
	s.calls = append(s.calls, "prepare")
	return s.ticket, s.err
}

func TestAuthRefreshIssuancePreparedBeforeCredentials(t *testing.T) {
	user := &User{ID: 17, Email: "synthetic@example.invalid", Role: RoleUser}
	for _, tc := range []struct {
		name   string
		ticket *RefreshTokenIssuance
		err    error
	}{
		{name: "database unavailable", err: errors.New("synthetic database unavailable")},
		{name: "missing ticket"},
		{name: "wrong user", ticket: &RefreshTokenIssuance{ID: "synthetic", UserID: 18}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cache := &issuancePreparerStub{issuanceCacheStub: &issuanceCacheStub{}, ticket: tc.ticket, err: tc.err}
			// No signing configuration: reaching credential generation would panic.
			svc := &AuthService{refreshTokenCache: cache}
			pair, err := svc.generateTokenPair(context.Background(), user, "", time.Now().Add(time.Hour))
			require.Error(t, err)
			require.Nil(t, pair)
			require.Equal(t, []string{"prepare"}, cache.calls)
			require.Nil(t, cache.stored)
		})
	}
}

func TestAuthRefreshIssuancePreservesAdmissionAndAbsoluteExpiry(t *testing.T) {
	user := &User{ID: 17, Email: "synthetic@example.invalid", Role: RoleUser}
	deadline := time.Now().Add(2 * time.Hour).Truncate(time.Microsecond)
	ticket := &RefreshTokenIssuance{ID: "synthetic-ticket", UserID: 17, UserGeneration: 2, GlobalGeneration: 3}
	for _, storeErr := range []error{nil, errors.New("synthetic revoked admission")} {
		cache := &issuancePreparerStub{issuanceCacheStub: &issuanceCacheStub{storeErr: storeErr}, ticket: ticket}
		svc := &AuthService{refreshTokenCache: cache, cfg: &config.Config{JWT: config.JWTConfig{Secret: "synthetic-test-only-signing-key", ExpireHour: 168}}}
		pair, err := svc.generateTokenPair(context.Background(), user, "synthetic-family", deadline)
		require.Same(t, ticket, cache.stored.Issuance)
		require.Equal(t, deadline, cache.stored.ExpiresAt)
		require.Equal(t, deadline, cache.stored.FamilyExpiresAt)
		encoded, marshalErr := json.Marshal(cache.stored)
		require.NoError(t, marshalErr)
		require.NotContains(t, string(encoded), ticket.ID, "admission must never be serialized to Redis or clients")
		if storeErr != nil {
			require.ErrorIs(t, err, storeErr)
			require.Nil(t, pair)
			require.Equal(t, []string{"prepare", "store"}, cache.calls)
		} else {
			require.NoError(t, err)
			require.NotEmpty(t, pair.AccessToken)
			require.NotEmpty(t, pair.RefreshToken)
			require.LessOrEqual(t, pair.ExpiresIn, 7200)
			require.Equal(t, []string{"prepare", "store", "user", "family"}, cache.calls)
		}
	}
}

func TestAuthRefreshLegacyStoreDoesNotRequireAdmission(t *testing.T) {
	cache := &issuanceCacheStub{}
	svc := &AuthService{refreshTokenCache: cache, cfg: &config.Config{JWT: config.JWTConfig{Secret: "synthetic-test-only-signing-key", ExpireHour: 168}}}
	pair, err := svc.GenerateTokenPair(context.Background(), &User{ID: 17, Role: RoleUser}, "")
	require.NoError(t, err)
	require.NotEmpty(t, pair.RefreshToken)
	require.Nil(t, cache.stored.Issuance)
	require.Equal(t, []string{"store", "user", "family"}, cache.calls)
}

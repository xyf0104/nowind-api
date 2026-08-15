//go:build unit

package service_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
)

func newRefreshSecurityService() (*service.AuthService, *emailBindRefreshTokenCacheStub) {
	user := &service.User{
		ID:           77,
		Email:        "refresh@example.com",
		PasswordHash: "password-fingerprint",
		Role:         service.RoleUser,
		Status:       service.StatusActive,
	}
	cache := newEmailBindRefreshTokenCacheStub()
	cfg := &config.Config{JWT: config.JWTConfig{
		Secret:                 "refresh-security-test-secret",
		ExpireHour:             168,
		RefreshTokenExpireDays: 30,
	}}
	return service.NewAuthService(nil, newEmailBindUserRepoStub(user), nil, cache, cfg, nil, nil, nil, nil, nil, nil, nil, nil), cache
}

func onlyRefreshTokenData(t *testing.T, cache *emailBindRefreshTokenCacheStub) *service.RefreshTokenData {
	t.Helper()
	cache.mu.Lock()
	defer cache.mu.Unlock()
	require.Len(t, cache.tokens, 1)
	for _, data := range cache.tokens {
		cloned := *data
		return &cloned
	}
	return nil
}

func setOnlyRefreshTokenDeadline(t *testing.T, cache *emailBindRefreshTokenCacheStub, deadline time.Time) {
	t.Helper()
	cache.mu.Lock()
	defer cache.mu.Unlock()
	require.Len(t, cache.tokens, 1)
	for _, data := range cache.tokens {
		data.ExpiresAt = deadline
		data.FamilyExpiresAt = deadline
	}
}

func TestTokenPairRotationKeepsAbsoluteSevenDayFamilyExpiry(t *testing.T) {
	ctx := context.Background()
	svc, cache := newRefreshSecurityService()

	account := &service.User{
		ID:           77,
		Email:        "refresh@example.com",
		PasswordHash: "password-fingerprint",
		Role:         service.RoleUser,
		Status:       service.StatusActive,
	}
	startedAt := time.Now()
	pair, err := svc.GenerateTokenPair(ctx, account, "")
	require.NoError(t, err)

	initialData := onlyRefreshTokenData(t, cache)
	require.WithinDuration(t, startedAt.Add(7*24*time.Hour), initialData.FamilyExpiresAt, 2*time.Second)
	require.Equal(t, initialData.FamilyExpiresAt, initialData.ExpiresAt)

	deadline := time.Now().Add(2 * time.Minute)
	setOnlyRefreshTokenDeadline(t, cache, deadline)
	rotated, err := svc.RefreshTokenPair(ctx, pair.RefreshToken)
	require.NoError(t, err)
	require.LessOrEqual(t, rotated.ExpiresIn, int((2*time.Minute)/time.Second))

	rotatedData := onlyRefreshTokenData(t, cache)
	require.Equal(t, deadline, rotatedData.FamilyExpiresAt)
	require.Equal(t, deadline, rotatedData.ExpiresAt)

	claims := &service.JWTClaims{}
	parsed, err := jwt.ParseWithClaims(rotated.AccessToken, claims, func(*jwt.Token) (any, error) {
		return []byte("refresh-security-test-secret"), nil
	})
	require.NoError(t, err)
	require.True(t, parsed.Valid)
	require.NotNil(t, claims.ExpiresAt)
	require.WithinDuration(t, deadline, claims.ExpiresAt.Time, time.Second)
}

func TestRefreshTokenPairConcurrentReplayMintsOnePair(t *testing.T) {
	ctx := context.Background()
	svc, _ := newRefreshSecurityService()
	account := &service.User{
		ID:           77,
		Email:        "refresh@example.com",
		PasswordHash: "password-fingerprint",
		Role:         service.RoleUser,
		Status:       service.StatusActive,
	}
	pair, err := svc.GenerateTokenPair(ctx, account, "")
	require.NoError(t, err)

	const callers = 20
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := svc.RefreshTokenPair(ctx, pair.RefreshToken)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)

	successes := 0
	rejected := 0
	for err := range errs {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, service.ErrRefreshTokenInvalid):
			rejected++
		default:
			require.NoError(t, err)
		}
	}
	require.Equal(t, 1, successes)
	require.Equal(t, callers-1, rejected)
}

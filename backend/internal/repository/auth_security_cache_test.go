package repository

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func newAuthSecurityRedis(t *testing.T) (*redis.Client, func()) {
	t.Helper()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	return client, func() { require.NoError(t, client.Close()) }
}

func TestRefreshTokenConsumeIsAtomic(t *testing.T) {
	ctx := context.Background()
	client, closeClient := newAuthSecurityRedis(t)
	defer closeClient()
	cache := NewRefreshTokenCache(client)

	deadline := time.Now().Add(7 * 24 * time.Hour)
	require.NoError(t, cache.StoreRefreshToken(ctx, "same-token", &service.RefreshTokenData{
		UserID:          9,
		FamilyID:        "family-1",
		CreatedAt:       time.Now(),
		ExpiresAt:       deadline,
		FamilyExpiresAt: deadline,
	}, 7*24*time.Hour))

	const callers = 32
	type result struct {
		data *service.RefreshTokenData
		err  error
	}
	results := make(chan result, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			data, err := cache.ConsumeRefreshToken(ctx, "same-token")
			results <- result{data: data, err: err}
		}()
	}
	wg.Wait()
	close(results)

	successes := 0
	notFound := 0
	for result := range results {
		switch {
		case result.err == nil:
			successes++
			require.Equal(t, int64(9), result.data.UserID)
		case errors.Is(result.err, service.ErrRefreshTokenNotFound):
			notFound++
		default:
			require.NoError(t, result.err)
		}
	}
	require.Equal(t, 1, successes)
	require.Equal(t, callers-1, notFound)
}

func TestUserRefreshTokenSetTTLDoesNotShrinkForOlderFamily(t *testing.T) {
	ctx := context.Background()
	client, closeClient := newAuthSecurityRedis(t)
	defer closeClient()
	cache := NewRefreshTokenCache(client)

	require.NoError(t, cache.AddToUserTokenSet(ctx, 9, "new-family-token", 7*24*time.Hour))
	require.NoError(t, cache.AddToUserTokenSet(ctx, 9, "old-family-token", time.Hour))

	ttl, err := client.TTL(ctx, userRefreshTokensKey(9)).Result()
	require.NoError(t, err)
	require.Greater(t, ttl, 6*24*time.Hour)
}

func TestTotpLoginSessionConsumeIsAtomic(t *testing.T) {
	ctx := context.Background()
	client, closeClient := newAuthSecurityRedis(t)
	defer closeClient()
	cache := NewTotpCache(client)

	require.NoError(t, cache.SetLoginSession(ctx, "temp-token", &service.TotpLoginSession{
		UserID:      11,
		Email:       "user@example.com",
		TokenExpiry: time.Now().Add(5 * time.Minute),
	}, 5*time.Minute))

	const callers = 24
	results := make(chan *service.TotpLoginSession, callers)
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			session, err := cache.ConsumeLoginSession(ctx, "temp-token")
			results <- session
			errs <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errs)

	for err := range errs {
		require.NoError(t, err)
	}
	successes := 0
	for session := range results {
		if session != nil {
			successes++
			require.Equal(t, int64(11), session.UserID)
		}
	}
	require.Equal(t, 1, successes)
}

func TestTotpVerifyAttemptReservationIsAtomic(t *testing.T) {
	ctx := context.Background()
	client, closeClient := newAuthSecurityRedis(t)
	defer closeClient()
	cache := NewTotpCache(client)

	const callers = 40
	const maxAttempts = 5
	results := make(chan bool, callers)
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			allowed, err := cache.ReserveVerifyAttempt(ctx, 17, maxAttempts, 15*time.Minute)
			results <- allowed
			errs <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errs)

	for err := range errs {
		require.NoError(t, err)
	}
	allowedCount := 0
	for allowed := range results {
		if allowed {
			allowedCount++
		}
	}
	require.Equal(t, maxAttempts, allowedCount)
}

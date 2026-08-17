package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestRedisPublicOpenAIOAuthSessionStoreRetainsSessionAfterInvalidProof(t *testing.T) {
	redisServer := miniredis.RunT(t)
	redisClient := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	defer func() { _ = redisClient.Close() }()
	store := NewRedisPublicOpenAIOAuthSessionStore(redisClient)
	ctx := context.Background()

	require.NoError(t, store.Store(ctx, "session", &openai.OAuthSession{
		State:              "expected-state",
		CodeVerifier:       "verifier",
		RedirectURI:        openai.DefaultRedirectURI,
		BrowserBindingHash: "expected-browser",
		CreatedAt:          time.Now(),
	}))

	_, found, err := store.Consume(ctx, "session", "wrong-state", "expected-browser")
	require.False(t, found)
	require.ErrorIs(t, err, service.ErrPublicOpenAIOAuthStateMismatch)

	_, found, err = store.Consume(ctx, "session", "expected-state", "wrong-browser")
	require.False(t, found)
	require.ErrorIs(t, err, service.ErrPublicOpenAIOAuthBrowserBindingMismatch)

	session, found, err := store.Consume(ctx, "session", "expected-state", "expected-browser")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "verifier", session.CodeVerifier)

	_, found, err = store.Consume(ctx, "session", "expected-state", "expected-browser")
	require.NoError(t, err)
	require.False(t, found)
}

func TestRedisPublicOpenAIOAuthSessionStoreRejectsInvalidInput(t *testing.T) {
	store := NewRedisPublicOpenAIOAuthSessionStore(nil)
	require.Error(t, store.Store(context.Background(), "session", &openai.OAuthSession{}))
	_, _, err := store.Consume(context.Background(), "session", "state", "browser")
	require.Error(t, err)
	require.False(t, errors.Is(err, service.ErrPublicOpenAIOAuthStateMismatch))
}

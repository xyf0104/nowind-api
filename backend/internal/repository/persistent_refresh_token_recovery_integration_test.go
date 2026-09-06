//go:build integration

package repository

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
)

// This is the green companion, not a replacement for the deliberately red
// TestRedisPromotionMustNotResurrectAcknowledgedRefreshRevocation. PostgreSQL is
// unchanged; only disposable Redis test containers are paused/killed/promoted.
func TestPersistentRefreshRedisPromotionPreservesAcknowledgedRevocation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	startRedis := func() (*tcredis.RedisContainer, *redis.Client) {
		t.Helper()
		container, err := tcredis.Run(ctx, redisImageTag)
		require.NoError(t, err)
		t.Cleanup(func() {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cleanupCancel()
			require.NoError(t, container.Terminate(cleanupCtx))
		})
		endpoint, err := container.ConnectionString(ctx)
		require.NoError(t, err)
		opts, err := redis.ParseURL(endpoint)
		require.NoError(t, err)
		opts.MaxRetries = -1
		client := redis.NewClient(opts)
		t.Cleanup(func() { require.NoError(t, client.Close()) })
		return container, client
	}
	primaryContainer, primary := startRedis()
	replicaContainer, replica := startRedis()
	primaryIP, err := primaryContainer.ContainerIP(ctx)
	require.NoError(t, err)
	require.NoError(t, replica.Do(ctx, "REPLICAOF", primaryIP, "6379").Err())
	user := mustCreateUser(t, testEntClient(t), &service.User{Balance: 17.25})
	userRepo := NewUserRepository(testEntClient(t), integrationDB)
	cfg := &config.Config{JWT: config.JWTConfig{
		Secret: "persistent-recovery-test-only-signing-secret", ExpireHour: 168, RefreshTokenExpireDays: 7,
	}}
	newAuth := func(store service.RefreshTokenCache) *service.AuthService {
		return service.NewAuthService(testEntClient(t), userRepo, nil, store, cfg,
			nil, nil, nil, nil, nil, nil, nil, nil)
	}
	durable := NewPersistentRefreshTokenStore(integrationDB)
	auth := newAuth(durable)
	revoked, err := auth.GenerateTokenPair(ctx, user, "")
	require.NoError(t, err)
	control, err := newAuth(durable).GenerateTokenPair(ctx, user, "")
	require.NoError(t, err)
	revokedHash := persistentRefreshTestHash(revoked.RefreshToken)
	controlHash := persistentRefreshTestHash(control.RefreshToken)
	legacyPrimary, legacyReplica := NewRefreshTokenCache(primary), NewRefreshTokenCache(replica)

	// A test-only shadow of issued metadata makes the same lost-DEL gap visible
	// in Redis. The durable store neither writes nor reads this shadow.
	for _, hash := range []string{revokedHash, controlHash} {
		data, err := durable.GetRefreshToken(ctx, hash)
		require.NoError(t, err)
		require.NoError(t, legacyPrimary.StoreRefreshToken(ctx, hash, data, time.Until(data.ExpiresAt)))
		require.NoError(t, legacyPrimary.AddToUserTokenSet(ctx, data.UserID, hash, time.Until(data.ExpiresAt)))
		require.NoError(t, legacyPrimary.AddToFamilyTokenSet(ctx, data.FamilyID, hash, time.Until(data.ExpiresAt)))
	}
	// This valid-looking token exists ONLY in Redis and must never be adopted.
	legacyOnly, err := newAuth(legacyPrimary).GenerateTokenPair(ctx, user, "")
	require.NoError(t, err)
	legacyOnlyHash := persistentRefreshTestHash(legacyOnly.RefreshToken)
	require.Eventually(t, func() bool {
		info, err := replica.Info(ctx, "replication").Result()
		if err != nil || !strings.Contains(info, "master_link_status:up") {
			return false
		}
		for _, hash := range []string{revokedHash, controlHash, legacyOnlyHash} {
			data, err := legacyReplica.GetRefreshToken(ctx, hash)
			if err != nil || data == nil {
				return false
			}
			member, err := legacyReplica.IsTokenInFamily(ctx, data.FamilyID, hash)
			if err != nil || !member {
				return false
			}
		}
		return true
	}, 20*time.Second, 100*time.Millisecond)
	docker := func(args ...string) {
		t.Helper()
		output, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
		require.NoError(t, err, "docker %s: %s", args[0], output)
	}
	paused := false
	t.Cleanup(func() {
		if paused {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cleanupCancel()
			output, err := exec.CommandContext(cleanupCtx, "docker", "unpause", replicaContainer.GetContainerID()).CombinedOutput()
			require.NoError(t, err, "unpause test replica: %s", output)
		}
	})
	docker("pause", replicaContainer.GetContainerID())
	paused = true
	killed, err := primary.Do(ctx, "CLIENT", "KILL", "TYPE", "replica").Int()
	require.NoError(t, err)
	require.GreaterOrEqual(t, killed, 1)
	info, err := primary.Info(ctx, "replication").Result()
	require.NoError(t, err)
	require.Contains(t, info, "connected_slaves:0")
	require.NoError(t, auth.RevokeRefreshToken(ctx, revoked.RefreshToken), "PG revocation committed before acknowledgment")
	require.NoError(t, legacyPrimary.DeleteRefreshToken(ctx, revokedHash), "shadow DEL is also acknowledged on Redis primary")
	_, err = auth.RefreshTokenPair(ctx, revoked.RefreshToken)
	require.ErrorIs(t, err, service.ErrRefreshTokenInvalid)
	docker("kill", "--signal", "KILL", primaryContainer.GetContainerID())
	docker("unpause", replicaContainer.GetContainerID())
	paused = false
	require.NoError(t, replica.Do(ctx, "REPLICAOF", "NO", "ONE").Err())
	info, err = replica.Info(ctx, "replication").Result()
	require.NoError(t, err)
	require.Contains(t, info, "role:master")
	stale, err := legacyReplica.GetRefreshToken(ctx, revokedHash)
	require.NoError(t, err, "the real promoted Redis must actually contain the revoked credential")
	require.Equal(t, user.ID, stale.UserID)
	member, err := legacyReplica.IsTokenInFamily(ctx, stale.FamilyID, revokedHash)
	require.NoError(t, err)
	require.True(t, member, "both stale payload and family index survived the lost DEL")

	// Recreate the store/AuthService over a fresh connection to the SAME PG DB.
	// No process memory, Redis state, user change, or signing-key rotation helps.
	db, err := openSQLWithRetry(ctx, integrationPostgresDSN, 5*time.Second)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	postPromotion := NewPersistentRefreshTokenStore(db)
	promotedAuth := newAuth(postPromotion)
	persisted, err := userRepo.GetByID(ctx, user.ID)
	require.NoError(t, err)
	require.Equal(t, user.PasswordHash, persisted.PasswordHash)
	require.Equal(t, user.Balance, persisted.Balance)
	validPair, err := promotedAuth.RefreshTokenPair(ctx, control.RefreshToken)
	require.NoError(t, err, "independent unrevoked control remains usable")
	require.NotEmpty(t, validPair.AccessToken)
	invalidPair, err := promotedAuth.RefreshTokenPair(ctx, revoked.RefreshToken)
	require.Nil(t, invalidPair)
	require.ErrorIs(t, err, service.ErrRefreshTokenInvalid)
	invalidPair, err = promotedAuth.RefreshTokenPair(ctx, legacyOnly.RefreshToken)
	require.Nil(t, invalidPair)
	require.ErrorIs(t, err, service.ErrRefreshTokenInvalid, "Redis-only sessions must not be auto-imported")
	assertPersistentRefreshMissing(t, postPromotion, revokedHash)
	assertPersistentRefreshMissing(t, postPromotion, legacyOnlyHash)
}

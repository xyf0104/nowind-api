//go:build integration

package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// HA activation includes the explicit legacy-session migration now exposed by
// the executable. The fault and both original assertions remain: acknowledged
// revocation cannot resurrect, and the independent control session must work.
// Raw Redis-only sessions are not a supported automatic-promotion authority.
func TestRedisPromotionMustNotResurrectAcknowledgedRefreshRevocation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	ctx = service.WithSessionBinding(ctx, &service.SessionBinding{IP: "192.0.2.42", UserAgent: "redis-promotion-contract"})
	stateDB := refreshTransitionPG(t)
	options, containers, clients := refreshTransitionTestGroup(t, "redis:7.4-alpine", 1)
	primaryContainer, replicaContainer := containers[0], containers[1]
	primary, replica := clients[0], clients[1]

	user := mustCreateUser(t, testEntClient(t), &service.User{Balance: 17.25})
	userRepo := NewUserRepository(testEntClient(t), integrationDB)
	cfg := &config.Config{JWT: config.JWTConfig{
		Secret: "redis-recovery-test-only-signing-secret", ExpireHour: 168, RefreshTokenExpireDays: 7, RefreshTokenStore: "redis",
	}}
	newAuth := func(client *redis.Client) *service.AuthService {
		t.Helper()
		store, err := NewRefreshTokenStore(stateDB, client, cfg)
		require.NoError(t, err)
		return service.NewAuthService(testEntClient(t), userRepo, nil, store, cfg,
			nil, nil, nil, nil, nil, nil, nil, nil)
	}
	auth := newAuth(primary)
	revoked, err := auth.GenerateTokenPair(ctx, user, "")
	require.NoError(t, err)
	control, err := auth.GenerateTokenPair(ctx, user, "")
	require.NoError(t, err)
	tokenHash := func(token string) string {
		sum := sha256.Sum256([]byte(token))
		return hex.EncodeToString(sum[:])
	}
	revokedHash := tokenHash(revoked.RefreshToken)
	controlHash := tokenHash(control.RefreshToken)
	replicaCache := NewRefreshTokenCache(replica)
	require.Eventually(t, func() bool {
		info, infoErr := replica.Info(ctx, "replication").Result()
		if infoErr != nil || !strings.Contains(info, "master_link_status:up") {
			return false
		}
		for _, hash := range []string{revokedHash, controlHash} {
			data, getErr := replicaCache.GetRefreshToken(ctx, hash)
			if getErr != nil || data == nil {
				return false
			}
			member, memberErr := replicaCache.IsTokenInFamily(ctx, data.FamilyID, hash)
			if memberErr != nil || !member {
				return false
			}
		}
		return true
	}, 20*time.Second, 100*time.Millisecond, "real replica must receive both sessions before injecting lag")

	// Adopt the actual sessions, never a fabricated authority marker or an
	// empty replacement store. CLI/HTTP acceptance separately covers this entry.
	transition, err := NewPersistentRefreshTokenStore(stateDB).AdoptLegacyRefreshTokens(ctx, options)
	require.NoError(t, err)
	require.EqualValues(t, 2, transition.Imported)
	fenced := refreshTransitionGroupFenceClients(t, options, transition.TransitionID)
	primary, replica = fenced[0], fenced[1]
	for _, node := range fenced {
		require.NoError(t, node.ACLSetUser(ctx, "contract-replication", "reset", "on", ">replication-contract-only", "+ping", "+replconf", "+psync").Err())
		require.NoError(t, node.Do(ctx, "ACL", "SAVE").Err())
	}
	require.NoError(t, replica.Do(ctx, "CONFIG", "SET", "masteruser", "contract-replication", "masterauth", "replication-contract-only").Err())
	require.Eventually(t, func() bool {
		info, err := replica.Info(ctx, "replication").Result()
		return err == nil && strings.Contains(info, "master_link_status:up")
	}, 15*time.Second, 50*time.Millisecond)
	cfg.JWT.RefreshTokenStore = "postgres"
	auth = newAuth(primary)

	docker := func(args ...string) {
		t.Helper()
		output, commandErr := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
		require.NoError(t, commandErr, "docker %s: %s", args[0], output)
	}
	paused := false
	t.Cleanup(func() {
		if paused {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cleanupCancel()
			output, cleanupErr := exec.CommandContext(cleanupCtx, "docker", "unpause", replicaContainer.GetContainerID()).CombinedOutput()
			require.NoError(t, cleanupErr, "unpause test replica: %s", output)
		}
	})

	// Disconnect after suspension so the acknowledged DEL cannot already be
	// waiting in the replica's TCP receive buffer when it resumes.
	docker("pause", replicaContainer.GetContainerID())
	paused = true
	killed, err := primary.Do(ctx, "CLIENT", "KILL", "TYPE", "replica").Int()
	require.NoError(t, err)
	require.GreaterOrEqual(t, killed, 1)
	info, err := primary.Info(ctx, "replication").Result()
	require.NoError(t, err)
	require.Contains(t, info, "connected_slaves:0")
	require.NoError(t, auth.RevokeRefreshToken(ctx, revoked.RefreshToken), "revocation was acknowledged")
	_, err = auth.RefreshTokenPair(ctx, revoked.RefreshToken)
	require.ErrorIs(t, err, service.ErrRefreshTokenInvalid, "old primary must reject the same credential")
	require.EqualValues(t, 1, primary.Exists(ctx, refreshTokenKey(revokedHash)).Val(), "legacy Redis data stays stale; rejection must come from the migrated authority")

	docker("kill", "--signal", "KILL", primaryContainer.GetContainerID())
	docker("unpause", replicaContainer.GetContainerID())
	paused = false
	require.NoError(t, replica.Do(ctx, "REPLICAOF", "NO", "ONE").Err())
	info, err = replica.Info(ctx, "replication").Result()
	require.NoError(t, err)
	require.Contains(t, info, "role:master")
	require.EqualValues(t, 1, replica.Exists(ctx, refreshTokenKey(revokedHash)).Val(), "the promoted replica really contains the revoked legacy credential")

	// The PG user and signing key are unchanged. A valid second session is a
	// control against accidentally passing because all authentication is broken.
	persisted, err := userRepo.GetByID(ctx, user.ID)
	require.NoError(t, err)
	require.Equal(t, user.PasswordHash, persisted.PasswordHash)
	require.Equal(t, user.Balance, persisted.Balance)
	promotedAuth := newAuth(replica)
	validPair, err := promotedAuth.RefreshTokenPair(ctx, control.RefreshToken)
	require.NoError(t, err, "unrevoked control session must remain usable")
	require.NotEmpty(t, validPair.AccessToken)
	resurrected, err := promotedAuth.RefreshTokenPair(ctx, revoked.RefreshToken)
	if err == nil && resurrected != nil && resurrected.AccessToken != "" {
		t.Fatal("acknowledged refresh revocation was lost: promoted replica minted a new access token for the same revoked credential")
	}
	require.ErrorIs(t, err, service.ErrRefreshTokenInvalid, "promotion must preserve acknowledged revocation")
}

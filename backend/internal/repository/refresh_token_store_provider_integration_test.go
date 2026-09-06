//go:build integration

package repository

import (
	"context"
	"database/sql"
	"net/url"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func refreshProviderTestDB(t *testing.T) *sql.DB {
	t.Helper()
	schema := "refresh_provider_" + uuid.New().String()[:8]
	_, err := integrationDB.Exec(`CREATE SCHEMA ` + schema)
	require.NoError(t, err)
	u, err := url.Parse(integrationPostgresDSN)
	require.NoError(t, err)
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, err := sql.Open("postgres", u.String())
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, db.Close())
		_, err := integrationDB.Exec(`DROP SCHEMA ` + schema + ` CASCADE`)
		require.NoError(t, err)
	})
	ddl, err := migrations.FS.ReadFile("238_persistent_refresh_tokens.sql")
	require.NoError(t, err)
	_, err = db.Exec(string(ddl))
	require.NoError(t, err)
	// The guard only needs the authority contract here. Migration/cutover tests
	// separately verify how the production marker may be activated.
	_, err = db.Exec(`CREATE TABLE refresh_token_authority (
		singleton boolean PRIMARY KEY CHECK (singleton),
		backend text NOT NULL CHECK (backend IN ('redis', 'postgres')),
		activated_at timestamptz);
		INSERT INTO refresh_token_authority (singleton, backend) VALUES (TRUE, 'redis')`)
	require.NoError(t, err)
	return db
}

func TestRefreshTokenProviderRealAuthoritySwitch(t *testing.T) {
	ctx := context.Background()
	db := refreshProviderTestDB(t)
	rdb := testRedis(t)
	legacy, err := NewRefreshTokenStore(db, rdb, &config.Config{})
	require.NoError(t, err)
	now := time.Now().UTC().Truncate(time.Microsecond)
	hash := persistentRefreshTestHash(t.Name())
	data := &service.RefreshTokenData{UserID: 11, TokenVersion: 8,
		FamilyID:  persistentRefreshTestHash("provider family")[:32],
		CreatedAt: now, ExpiresAt: now.Add(time.Hour), FamilyExpiresAt: now.Add(time.Hour)}
	require.NoError(t, legacy.StoreRefreshToken(ctx, hash, data, time.Hour))
	require.NoError(t, legacy.AddToUserTokenSet(ctx, data.UserID, hash, time.Hour))
	require.NoError(t, legacy.AddToFamilyTokenSet(ctx, data.FamilyID, hash, time.Hour))
	got, err := legacy.GetRefreshToken(ctx, hash)
	require.NoError(t, err)
	require.Equal(t, data, got)
	pgConfig := &config.Config{JWT: config.JWTConfig{RefreshTokenStore: "postgres"}}
	_, err = NewRefreshTokenStore(db, rdb, pgConfig)
	require.ErrorIs(t, err, ErrRefreshTokenAuthority, "changing env must not implicitly import or log out sessions")

	// Deliberate test fixture activation, not an adoption implementation.
	_, err = db.Exec(`UPDATE refresh_token_authority SET backend='postgres', activated_at=clock_timestamp()`)
	require.NoError(t, err)
	got, err = legacy.GetRefreshToken(ctx, hash)
	require.Nil(t, got)
	require.ErrorIs(t, err, ErrRefreshTokenAuthority, "an already-running upgraded Redis node must stop after activation")
	_, err = NewRefreshTokenStore(db, rdb, &config.Config{})
	require.ErrorIs(t, err, ErrRefreshTokenAuthority, "a node started with old config must not revive Redis sessions")
	persistent, err := NewRefreshTokenStore(db, nil, pgConfig)
	require.NoError(t, err)
	_, err = persistent.GetRefreshToken(ctx, hash)
	require.ErrorIs(t, err, service.ErrRefreshTokenNotFound, "persistent authority has no Redis fallback")
	preparer, ok := persistent.(service.RefreshTokenIssuancePreparer)
	require.True(t, ok)
	data.Issuance, err = preparer.PrepareRefreshTokenIssuance(ctx, data.UserID)
	require.NoError(t, err)
	newHash := persistentRefreshTestHash("new provider token")
	require.NoError(t, persistent.StoreRefreshToken(ctx, newHash, data, time.Hour))
	got, err = persistent.GetRefreshToken(ctx, newHash)
	require.NoError(t, err)
	require.Equal(t, persistentRefreshPayload(data), got)
	require.NoError(t, persistent.DeleteRefreshToken(ctx, newHash))
	_, err = persistent.GetRefreshToken(ctx, newHash)
	require.ErrorIs(t, err, service.ErrRefreshTokenNotFound)
}

func TestRefreshTokenProviderActivationWaitsForInFlightLegacyOperation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db := refreshProviderTestDB(t)
	guard := &authorityCheckedRedisRefreshStore{db: db}
	entered, release := make(chan struct{}), make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		_, err := withRedisRefreshAuthority(ctx, guard, func(ctx context.Context) (int, error) {
			close(entered)
			select {
			case <-release:
				return 7, nil
			case <-ctx.Done():
				return 0, ctx.Err()
			}
		})
		finished <- err
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("guard did not acquire authority lock")
	}
	activation, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer activation.Rollback()
	_, err = activation.ExecContext(ctx, `SET LOCAL lock_timeout = '100ms'`)
	require.NoError(t, err)
	_, err = activation.ExecContext(ctx, `UPDATE refresh_token_authority SET backend='postgres'`)
	require.ErrorContains(t, err, "lock timeout", "activation cannot overtake an in-flight Redis write")
	require.NoError(t, activation.Rollback())
	close(release)
	require.NoError(t, <-finished)
	_, err = db.ExecContext(ctx, `UPDATE refresh_token_authority SET backend='postgres'`)
	require.NoError(t, err)
	called := false
	_, err = withRedisRefreshAuthority(ctx, guard, func(context.Context) (int, error) {
		called = true
		return 7, nil
	})
	require.ErrorIs(t, err, ErrRefreshTokenAuthority)
	require.False(t, called)
}

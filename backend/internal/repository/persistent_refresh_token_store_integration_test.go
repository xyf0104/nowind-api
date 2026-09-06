//go:build integration

package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

var persistentRefreshTestSequence atomic.Int64

func persistentRefreshTestHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func persistentRefreshTestData(t *testing.T, userID int64) (string, *service.RefreshTokenData) {
	t.Helper()
	id := persistentRefreshTestSequence.Add(1)
	if userID == 0 {
		userID = 9000000000000000 + id
	}
	ticket, err := NewPersistentRefreshTokenStore(integrationDB).PrepareRefreshTokenIssuance(context.Background(), userID)
	require.NoError(t, err)
	var now time.Time
	require.NoError(t, integrationDB.QueryRow(`SELECT clock_timestamp()`).Scan(&now))
	return persistentRefreshTestHash(fmt.Sprintf("token:%s:%d", t.Name(), id)), &service.RefreshTokenData{
		UserID: userID, Issuance: ticket, TokenVersion: -4321, BindingHash: persistentRefreshTestHash("test binding"),
		FamilyID:  persistentRefreshTestHash(fmt.Sprintf("family:%s:%d", t.Name(), id))[:32],
		CreatedAt: now, ExpiresAt: now.Add(7 * 24 * time.Hour), FamilyExpiresAt: now.Add(7 * 24 * time.Hour),
	}
}

func persistentRefreshPayload(data *service.RefreshTokenData) *service.RefreshTokenData {
	payload := *data
	payload.Issuance = nil
	return &payload
}

func assertPersistentRefreshMissing(t *testing.T, store *PersistentRefreshTokenStore, hash string) {
	t.Helper()
	data, err := store.GetRefreshToken(context.Background(), hash)
	require.Nil(t, data)
	require.ErrorIs(t, err, service.ErrRefreshTokenNotFound)
	data, err = store.ConsumeRefreshToken(context.Background(), hash)
	require.Nil(t, data)
	require.ErrorIs(t, err, service.ErrRefreshTokenNotFound)
}

func TestPersistentRefreshTokenStoreMetadataAndAtomicIndexes(t *testing.T) {
	ctx := context.Background()
	s := NewPersistentRefreshTokenStore(integrationDB)
	hash, data := persistentRefreshTestData(t, 0)
	data.ExpiresAt = data.CreatedAt.Add(3 * 24 * time.Hour)
	require.NoError(t, s.StoreRefreshToken(ctx, hash, data, 2*24*time.Hour))
	got, err := s.GetRefreshToken(ctx, hash)
	require.NoError(t, err)
	require.Equal(t, persistentRefreshPayload(data), got)
	hashes, err := s.GetUserTokenHashes(ctx, data.UserID)
	require.NoError(t, err)
	require.Equal(t, []string{hash}, hashes, "membership is committed by Store, without Add calls")
	hashes, err = s.GetFamilyTokenHashes(ctx, data.FamilyID)
	require.NoError(t, err)
	require.Equal(t, []string{hash}, hashes)
	member, err := s.IsTokenInFamily(ctx, data.FamilyID, hash)
	require.NoError(t, err)
	require.True(t, member)
	require.NoError(t, s.AddToUserTokenSet(ctx, data.UserID, hash, 100*24*time.Hour))
	require.NoError(t, s.AddToFamilyTokenSet(ctx, data.FamilyID, hash, 100*24*time.Hour))
	var until time.Time
	require.NoError(t, integrationDB.QueryRow(`SELECT valid_until FROM refresh_tokens WHERE token_hash = $1`, hash).Scan(&until))
	require.True(t, until.Equal(data.CreatedAt.Add(2*24*time.Hour)))
	require.ErrorIs(t, s.AddToUserTokenSet(ctx, data.UserID+1, hash, time.Hour), ErrPersistentRefreshTokenMetadata)
	require.ErrorIs(t, s.AddToFamilyTokenSet(ctx, strings.Repeat("e", 32), hash, time.Hour), ErrPersistentRefreshTokenMetadata)

	// Retrying Store cannot replace metadata or extend the original lifetime.
	require.ErrorIs(t, s.StoreRefreshToken(ctx, hash, data, 7*24*time.Hour), ErrPersistentRefreshTokenRejected)
	rotatedHash, rotated := persistentRefreshTestData(t, data.UserID)
	rotated.FamilyID = data.FamilyID
	require.ErrorIs(t, s.StoreRefreshToken(ctx, rotatedHash, rotated, time.Hour), ErrPersistentRefreshTokenRejected, "family deadline is immutable")
	rotated.FamilyExpiresAt = data.FamilyExpiresAt
	require.NoError(t, s.StoreRefreshToken(ctx, rotatedHash, rotated, time.Hour))
	wrongOwnerHash, wrongOwner := persistentRefreshTestData(t, data.UserID+1000)
	wrongOwner.FamilyID, wrongOwner.FamilyExpiresAt = data.FamilyID, data.FamilyExpiresAt
	require.ErrorIs(t, s.StoreRefreshToken(ctx, wrongOwnerHash, wrongOwner, time.Hour), ErrPersistentRefreshTokenRejected)
}

func TestPersistentRefreshTokenStoreActualHTTPBindingIssuance(t *testing.T) {
	ctx := service.WithSessionBinding(context.Background(), &service.SessionBinding{IP: "192.0.2.4", UserAgent: "actual-http-binding"})
	client := testEntClient(t)
	user := mustCreateUser(t, client, &service.User{Balance: 17.25})
	store := NewPersistentRefreshTokenStore(integrationDB)
	cfg := &config.Config{JWT: config.JWTConfig{Secret: "binding-issuance-test-only", ExpireHour: 168, RefreshTokenExpireDays: 7}}
	auth := service.NewAuthService(client, NewUserRepository(client, integrationDB), nil, store, cfg, nil, nil, nil, nil, nil, nil, nil, nil)
	pair, err := auth.GenerateTokenPair(ctx, user, "")
	require.NoError(t, err)
	original, err := store.GetRefreshToken(ctx, persistentRefreshTestHash(pair.RefreshToken))
	require.NoError(t, err)
	require.Equal(t, service.SessionBindingFromContext(ctx).Hash(), original.BindingHash)
	require.Len(t, original.BindingHash, 32)
	rotated, err := auth.RefreshTokenPair(ctx, pair.RefreshToken)
	require.NoError(t, err)
	got, err := store.GetRefreshToken(ctx, persistentRefreshTestHash(rotated.RefreshToken))
	require.NoError(t, err)
	require.Equal(t, original.BindingHash, got.BindingHash)
	require.Equal(t, original.FamilyExpiresAt, got.FamilyExpiresAt)
}

func TestPersistentRefreshTokenStoreConsumeRaceAndReplay(t *testing.T) {
	ctx := context.Background()
	s := NewPersistentRefreshTokenStore(integrationDB)
	hash, data := persistentRefreshTestData(t, 0)
	require.NoError(t, s.StoreRefreshToken(ctx, hash, data, 7*24*time.Hour))
	const workers = 24
	start := make(chan struct{})
	results := make(chan error, workers)
	var successes atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			// Distinct store instances exercise DB, not a process-local mutex.
			got, err := NewPersistentRefreshTokenStore(integrationDB).ConsumeRefreshToken(ctx, hash)
			if err == nil && got != nil && got.FamilyID == data.FamilyID {
				successes.Add(1)
			} else if err == nil {
				err = fmt.Errorf("consume returned wrong metadata")
			}
			results <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			require.ErrorIs(t, err, service.ErrRefreshTokenNotFound)
		}
	}
	require.EqualValues(t, 1, successes.Load())
	assertPersistentRefreshMissing(t, s, hash)
	require.ErrorIs(t, s.StoreRefreshToken(ctx, hash, data, 7*24*time.Hour), ErrPersistentRefreshTokenRejected)
	require.ErrorIs(t, s.AddToUserTokenSet(ctx, data.UserID, hash, time.Hour), service.ErrRefreshTokenNotFound)
	require.ErrorIs(t, s.AddToFamilyTokenSet(ctx, data.FamilyID, hash, time.Hour), service.ErrRefreshTokenNotFound)
	hashes, err := s.GetUserTokenHashes(ctx, data.UserID)
	require.NoError(t, err)
	require.Empty(t, hashes)
	var consumed sql.NullTime
	require.NoError(t, integrationDB.QueryRow(`SELECT consumed_at FROM refresh_tokens WHERE token_hash = $1`, hash).Scan(&consumed))
	require.True(t, consumed.Valid, "consumption retains a permanent tombstone")
	// Rotation to a different hash in the same unrevoked family remains valid.
	nextHash, next := persistentRefreshTestData(t, data.UserID)
	next.FamilyID, next.FamilyExpiresAt = data.FamilyID, data.FamilyExpiresAt
	require.NoError(t, s.StoreRefreshToken(ctx, nextHash, next, time.Hour))
}

func TestPersistentRefreshTokenStoreRevocationScopes(t *testing.T) {
	ctx := context.Background()
	s := NewPersistentRefreshTokenStore(integrationDB)
	hash, data := persistentRefreshTestData(t, 0)
	controlHash, control := persistentRefreshTestData(t, data.UserID)
	otherHash, other := persistentRefreshTestData(t, 0)
	for h, d := range map[string]*service.RefreshTokenData{hash: data, controlHash: control, otherHash: other} {
		require.NoError(t, s.StoreRefreshToken(ctx, h, d, 7*24*time.Hour))
	}
	require.NoError(t, s.DeleteRefreshToken(ctx, hash))
	require.NoError(t, s.DeleteRefreshToken(ctx, hash))
	assertPersistentRefreshMissing(t, s, hash)
	require.ErrorIs(t, s.StoreRefreshToken(ctx, hash, data, time.Hour), ErrPersistentRefreshTokenRejected)
	// A hash revocation does not destroy an independent token in the same family.
	nextHash, next := persistentRefreshTestData(t, data.UserID)
	next.FamilyID, next.FamilyExpiresAt = data.FamilyID, data.FamilyExpiresAt
	require.NoError(t, s.StoreRefreshToken(ctx, nextHash, next, time.Hour))
	require.NoError(t, s.DeleteTokenFamily(ctx, data.FamilyID))
	require.NoError(t, s.DeleteTokenFamily(ctx, data.FamilyID))
	assertPersistentRefreshMissing(t, s, nextHash)
	lateHash, late := persistentRefreshTestData(t, data.UserID)
	late.FamilyID, late.FamilyExpiresAt = data.FamilyID, data.FamilyExpiresAt
	require.ErrorIs(t, s.StoreRefreshToken(ctx, lateHash, late, time.Hour), ErrPersistentRefreshTokenRejected)
	_, err := s.GetRefreshToken(ctx, controlHash)
	require.NoError(t, err, "family revocation preserves independent same-user session")

	// A never-stored family must also be fenced by user revocation.
	unseenHash, unseen := persistentRefreshTestData(t, data.UserID)
	require.NoError(t, s.DeleteUserRefreshTokens(ctx, data.UserID))
	assertPersistentRefreshMissing(t, s, controlHash)
	require.ErrorIs(t, s.StoreRefreshToken(ctx, unseenHash, unseen, time.Hour), ErrPersistentRefreshTokenRejected)
	newHash, newSession := persistentRefreshTestData(t, data.UserID)
	require.NoError(t, s.StoreRefreshToken(ctx, newHash, newSession, time.Hour))
	_, err = s.GetRefreshToken(ctx, otherHash)
	require.NoError(t, err, "user revocation preserves another user")

	globalUnseenHash, globalUnseen := persistentRefreshTestData(t, 0)
	require.NoError(t, s.DeleteAllRefreshTokens(ctx))
	assertPersistentRefreshMissing(t, s, newHash)
	assertPersistentRefreshMissing(t, s, otherHash)
	require.ErrorIs(t, s.StoreRefreshToken(ctx, globalUnseenHash, globalUnseen, time.Hour), ErrPersistentRefreshTokenRejected)
	globalNewHash, globalNew := persistentRefreshTestData(t, newSession.UserID)
	require.NoError(t, s.StoreRefreshToken(ctx, globalNewHash, globalNew, time.Hour))
	lateFamilyHash, lateFamily := persistentRefreshTestData(t, data.UserID)
	lateFamily.FamilyID, lateFamily.FamilyExpiresAt = newSession.FamilyID, newSession.FamilyExpiresAt
	require.ErrorIs(t, s.StoreRefreshToken(ctx, lateFamilyHash, lateFamily, time.Hour), ErrPersistentRefreshTokenRejected)
}

func TestPersistentRefreshTokenStoreUnknownTombstones(t *testing.T) {
	ctx := context.Background()
	s := NewPersistentRefreshTokenStore(integrationDB)
	hash, data := persistentRefreshTestData(t, 0)
	require.NoError(t, s.DeleteRefreshToken(ctx, hash))
	require.ErrorIs(t, s.StoreRefreshToken(ctx, hash, data, time.Hour), ErrPersistentRefreshTokenRejected)
	assertPersistentRefreshMissing(t, s, hash)
	var count int
	require.NoError(t, integrationDB.QueryRow(`SELECT count(*) FROM refresh_token_families WHERE family_id = $1`, data.FamilyID).Scan(&count))
	require.Zero(t, count, "failed store rolls back family membership")

	familyHash, family := persistentRefreshTestData(t, 0)
	require.NoError(t, s.DeleteTokenFamily(ctx, family.FamilyID))
	require.ErrorIs(t, s.StoreRefreshToken(ctx, familyHash, family, time.Hour), ErrPersistentRefreshTokenRejected)
	unknownUserHash, unknownUser := persistentRefreshTestData(t, 0)
	require.NoError(t, s.DeleteUserRefreshTokens(ctx, unknownUser.UserID))
	require.ErrorIs(t, s.StoreRefreshToken(ctx, unknownUserHash, unknownUser, time.Hour), ErrPersistentRefreshTokenRejected)
}

func TestPersistentRefreshTokenStoreConcurrentUserRevocation(t *testing.T) {
	ctx := context.Background()
	s := NewPersistentRefreshTokenStore(integrationDB)
	for iteration := 0; iteration < 12; iteration++ {
		hash, data := persistentRefreshTestData(t, 0)
		start := make(chan struct{})
		stored, revoked := make(chan error, 1), make(chan error, 1)
		go func() { <-start; stored <- s.StoreRefreshToken(ctx, hash, data, time.Hour) }()
		go func() { <-start; revoked <- s.DeleteUserRefreshTokens(ctx, data.UserID) }()
		close(start)
		storeErr, revokeErr := <-stored, <-revoked
		require.NoError(t, revokeErr)
		if storeErr != nil {
			require.ErrorIs(t, storeErr, ErrPersistentRefreshTokenRejected)
		}
		assertPersistentRefreshMissing(t, s, hash)
		hashes, err := s.GetUserTokenHashes(ctx, data.UserID)
		require.NoError(t, err)
		require.Empty(t, hashes)
	}
}

func TestPersistentRefreshTokenStoreConcurrentOtherRevocations(t *testing.T) {
	ctx := context.Background()
	s := NewPersistentRefreshTokenStore(integrationDB)
	for _, scope := range []string{"hash", "family", "global"} {
		t.Run(scope, func(t *testing.T) {
			for iteration := 0; iteration < 8; iteration++ {
				hash, data := persistentRefreshTestData(t, 0)
				start := make(chan struct{})
				stored, revoked := make(chan error, 1), make(chan error, 1)
				go func() { <-start; stored <- s.StoreRefreshToken(ctx, hash, data, time.Hour) }()
				go func() {
					<-start
					switch scope {
					case "hash":
						revoked <- s.DeleteRefreshToken(ctx, hash)
					case "family":
						revoked <- s.DeleteTokenFamily(ctx, data.FamilyID)
					case "global":
						revoked <- s.DeleteAllRefreshTokens(ctx)
					}
				}()
				close(start)
				storeErr, revokeErr := <-stored, <-revoked
				require.NoError(t, revokeErr)
				if storeErr != nil {
					require.ErrorIs(t, storeErr, ErrPersistentRefreshTokenRejected)
				}
				assertPersistentRefreshMissing(t, s, hash)
			}
		})
	}
}

func TestPersistentRefreshTokenStoreRevocationBetweenConsumeAndRotation(t *testing.T) {
	ctx := context.Background()
	s := NewPersistentRefreshTokenStore(integrationDB)
	for _, scope := range []string{"family", "user", "global"} {
		t.Run(scope, func(t *testing.T) {
			hash, data := persistentRefreshTestData(t, 0)
			require.NoError(t, s.StoreRefreshToken(ctx, hash, data, time.Hour))
			_, err := s.ConsumeRefreshToken(ctx, hash)
			require.NoError(t, err)
			switch scope {
			case "family":
				require.NoError(t, s.DeleteTokenFamily(ctx, data.FamilyID))
			case "user":
				require.NoError(t, s.DeleteUserRefreshTokens(ctx, data.UserID))
			case "global":
				require.NoError(t, s.DeleteAllRefreshTokens(ctx))
			}
			// Rotation is generated after revocation, so CreatedAt alone cannot
			// fence it. The retained family tombstone must do so.
			childHash, child := persistentRefreshTestData(t, data.UserID)
			child.FamilyID, child.FamilyExpiresAt = data.FamilyID, data.FamilyExpiresAt
			require.NoError(t, integrationDB.QueryRow(`SELECT clock_timestamp()`).Scan(&child.CreatedAt))
			require.ErrorIs(t, s.StoreRefreshToken(ctx, childHash, child, time.Hour), ErrPersistentRefreshTokenRejected)
			assertPersistentRefreshMissing(t, s, childHash)
			independentHash, independent := persistentRefreshTestData(t, data.UserID)
			require.NoError(t, s.StoreRefreshToken(ctx, independentHash, independent, time.Hour))
		})
	}
}

func TestPersistentRefreshTokenStoreWaitsForRevocationCommit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s := NewPersistentRefreshTokenStore(integrationDB)
	hash, data := persistentRefreshTestData(t, 0)
	tx, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `UPDATE refresh_token_users SET generation = generation + 1, revoked_at = clock_timestamp() WHERE user_id = $1`, data.UserID)
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() { done <- s.StoreRefreshToken(ctx, hash, data, time.Hour) }()
	select {
	case err := <-done:
		t.Fatalf("Store passed an uncommitted user fence: %v", err)
	case <-time.After(75 * time.Millisecond):
	}
	require.NoError(t, tx.Commit())
	require.ErrorIs(t, <-done, ErrPersistentRefreshTokenRejected)
	assertPersistentRefreshMissing(t, s, hash)
}

func TestPersistentRefreshTokenStoreExpiryAndRetention(t *testing.T) {
	ctx := context.Background()
	s := NewPersistentRefreshTokenStore(integrationDB)
	for _, expiry := range []string{"ttl", "token", "family"} {
		t.Run(expiry, func(t *testing.T) {
			hash, data := persistentRefreshTestData(t, 0)
			ttl := 7 * 24 * time.Hour
			switch expiry {
			case "ttl":
				ttl = 250 * time.Millisecond
			case "token":
				data.ExpiresAt = data.CreatedAt.Add(250 * time.Millisecond)
			case "family":
				data.FamilyExpiresAt = data.CreatedAt.Add(250 * time.Millisecond)
			}
			require.NoError(t, s.StoreRefreshToken(ctx, hash, data, ttl))
			got, err := s.GetRefreshToken(ctx, hash)
			require.NoError(t, err)
			require.Equal(t, persistentRefreshPayload(data), got, "absolute metadata must not be replaced with TTL")
			require.Eventually(t, func() bool {
				_, err := s.GetRefreshToken(ctx, hash)
				return err == service.ErrRefreshTokenNotFound
			}, 3*time.Second, 20*time.Millisecond)
			assertPersistentRefreshMissing(t, s, hash)
			hashes, err := s.GetFamilyTokenHashes(ctx, data.FamilyID)
			require.NoError(t, err)
			require.Empty(t, hashes)
			member, err := s.IsTokenInFamily(ctx, data.FamilyID, hash)
			require.NoError(t, err)
			require.False(t, member)
			var count int
			require.NoError(t, integrationDB.QueryRow(`SELECT count(*) FROM refresh_tokens WHERE token_hash = $1`, hash).Scan(&count))
			require.Equal(t, 1, count, "expiry must never silently erase replay protection")
			_, replacement := persistentRefreshTestData(t, data.UserID)
			require.ErrorIs(t, s.StoreRefreshToken(ctx, hash, replacement, time.Hour), ErrPersistentRefreshTokenRejected)
		})
	}
	hash, future := persistentRefreshTestData(t, 0)
	future.CreatedAt = future.CreatedAt.Add(time.Minute)
	require.NoError(t, s.StoreRefreshToken(ctx, hash, future, time.Hour), "generation admission does not depend on the application clock")
	hash, expired := persistentRefreshTestData(t, 0)
	expired.CreatedAt = expired.CreatedAt.Add(-2 * time.Hour)
	require.ErrorIs(t, s.StoreRefreshToken(ctx, hash, expired, time.Hour), ErrPersistentRefreshTokenRejected, "late Store must not restart TTL")
}

// A deferred constraint trigger fails at COMMIT, after all writes and RETURNING
// metadata were available. It is scoped to a synthetic test credential only.
func persistentRefreshFailCommit(t *testing.T, table, condition string) func() {
	t.Helper()
	name := fmt.Sprintf("persistent_refresh_fail_%d", persistentRefreshTestSequence.Add(1))
	_, err := integrationDB.Exec(fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN IF %s THEN RAISE EXCEPTION 'test-only refresh commit failure'; END IF; RETURN NEW; END $$`, name, condition))
	require.NoError(t, err)
	_, err = integrationDB.Exec(fmt.Sprintf(`CREATE CONSTRAINT TRIGGER %s AFTER INSERT OR UPDATE ON %s
		DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION %s()`, name, table, name))
	require.NoError(t, err)
	var once sync.Once
	cleanup := func() {
		once.Do(func() {
			_, err := integrationDB.Exec(fmt.Sprintf(`DROP TRIGGER %s ON %s`, name, table))
			require.NoError(t, err)
			_, err = integrationDB.Exec(fmt.Sprintf(`DROP FUNCTION %s()`, name))
			require.NoError(t, err)
		})
	}
	t.Cleanup(cleanup)
	return cleanup
}

func TestPersistentRefreshTokenStoreCommitFailureRollsBackAllState(t *testing.T) {
	ctx := context.Background()
	s := NewPersistentRefreshTokenStore(integrationDB)
	hash, data := persistentRefreshTestData(t, 0)
	cleanup := persistentRefreshFailCommit(t, "refresh_tokens", fmt.Sprintf("NEW.token_hash = '%s'", hash))
	err := s.StoreRefreshToken(ctx, hash, data, time.Hour)
	require.ErrorContains(t, err, "commit refresh token transaction")
	for _, query := range []string{
		`SELECT count(*) FROM refresh_tokens WHERE user_id = $1`,
		`SELECT count(*) FROM refresh_token_families WHERE user_id = $1`,
	} {
		var count int
		require.NoError(t, integrationDB.QueryRow(query, data.UserID).Scan(&count))
		require.Zero(t, count)
	}
	cleanup()
	require.NoError(t, s.StoreRefreshToken(ctx, hash, data, time.Hour))
	cleanup = persistentRefreshFailCommit(t, "refresh_tokens", fmt.Sprintf("NEW.token_hash = '%s'", hash))
	got, err := s.ConsumeRefreshToken(ctx, hash)
	require.ErrorContains(t, err, "commit refresh token transaction")
	require.Nil(t, got, "uncommitted consumed metadata must not escape")
	require.ErrorContains(t, s.DeleteRefreshToken(ctx, hash), "commit refresh token transaction")
	cleanup()
	got, err = s.GetRefreshToken(ctx, hash)
	require.NoError(t, err)
	require.Equal(t, persistentRefreshPayload(data), got)

	cleanup = persistentRefreshFailCommit(t, "refresh_token_families", fmt.Sprintf("NEW.family_id = '%s'", data.FamilyID))
	require.ErrorContains(t, s.DeleteTokenFamily(ctx, data.FamilyID), "commit refresh token transaction")
	require.ErrorContains(t, s.DeleteUserRefreshTokens(ctx, data.UserID), "commit refresh token transaction")
	require.ErrorContains(t, s.DeleteAllRefreshTokens(ctx), "commit refresh token transaction")
	cleanup()
	got, err = s.GetRefreshToken(ctx, hash)
	require.NoError(t, err, "scope fences must roll back with failed revocation")
	require.Equal(t, persistentRefreshPayload(data), got)
	got, err = s.ConsumeRefreshToken(ctx, hash)
	require.NoError(t, err)
	require.Equal(t, persistentRefreshPayload(data), got)
}

func TestPersistentRefreshTokenStoreDatabaseErrorsFailClosed(t *testing.T) {
	db, err := sql.Open("postgres", integrationPostgresDSN)
	require.NoError(t, err)
	require.NoError(t, db.Close())
	s := NewPersistentRefreshTokenStore(db)
	ctx := context.Background()
	hash, data := persistentRefreshTestData(t, 0)
	ticket, err := s.PrepareRefreshTokenIssuance(ctx, data.UserID)
	require.Nil(t, ticket)
	require.Error(t, err)
	require.Error(t, s.StoreRefreshToken(ctx, hash, data, time.Hour))
	got, err := s.GetRefreshToken(ctx, hash)
	require.Nil(t, got)
	require.Error(t, err)
	require.NotErrorIs(t, err, service.ErrRefreshTokenNotFound)
	got, err = s.ConsumeRefreshToken(ctx, hash)
	require.Nil(t, got)
	require.Error(t, err)
	require.Error(t, s.DeleteRefreshToken(ctx, hash))
	require.Error(t, s.DeleteTokenFamily(ctx, data.FamilyID))
	require.Error(t, s.DeleteUserRefreshTokens(ctx, data.UserID))
	require.Error(t, s.DeleteAllRefreshTokens(ctx))
	require.Error(t, s.AddToUserTokenSet(ctx, data.UserID, hash, time.Hour))
	require.Error(t, s.AddToFamilyTokenSet(ctx, data.FamilyID, hash, time.Hour))
	hashes, err := s.GetUserTokenHashes(ctx, data.UserID)
	require.Nil(t, hashes)
	require.Error(t, err)
	hashes, err = s.GetFamilyTokenHashes(ctx, data.FamilyID)
	require.Nil(t, hashes)
	require.Error(t, err)
	member, err := s.IsTokenInFamily(ctx, data.FamilyID, hash)
	require.False(t, member)
	require.Error(t, err)
}

func TestPersistentRefreshTokenStoreMigration238RepeatPreservesTombstones(t *testing.T) {
	ctx := context.Background()
	s := NewPersistentRefreshTokenStore(integrationDB)
	hash, data := persistentRefreshTestData(t, 0)
	require.NoError(t, s.StoreRefreshToken(ctx, hash, data, time.Hour))
	require.NoError(t, s.DeleteUserRefreshTokens(ctx, data.UserID))
	migration, err := migrations.FS.ReadFile("238_persistent_refresh_tokens.sql")
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, string(migration))
	require.NoError(t, err)
	assertPersistentRefreshMissing(t, s, hash)
	require.ErrorIs(t, s.StoreRefreshToken(ctx, hash, data, time.Hour), ErrPersistentRefreshTokenRejected)
}

func TestPersistentRefreshTokenStoreAuthServiceSevenDayRotation(t *testing.T) {
	ctx := context.Background()
	user := mustCreateUser(t, testEntClient(t), &service.User{Balance: 17.25})
	s := NewPersistentRefreshTokenStore(integrationDB)
	cfg := &config.Config{JWT: config.JWTConfig{
		Secret: "persistent-refresh-test-only-signing-secret", ExpireHour: 168, RefreshTokenExpireDays: 7,
	}}
	newAuth := func() *service.AuthService {
		return service.NewAuthService(testEntClient(t), NewUserRepository(testEntClient(t), integrationDB), nil,
			s, cfg, nil, nil, nil, nil, nil, nil, nil, nil)
	}
	pair, err := newAuth().GenerateTokenPair(ctx, user, "")
	require.NoError(t, err)
	original, err := s.GetRefreshToken(ctx, persistentRefreshTestHash(pair.RefreshToken))
	require.NoError(t, err)
	require.InDelta(t, (7 * 24 * time.Hour).Seconds(), original.FamilyExpiresAt.Sub(original.CreatedAt).Seconds(), 2)
	rotated, err := newAuth().RefreshTokenPair(ctx, pair.RefreshToken)
	require.NoError(t, err)
	require.NotEmpty(t, rotated.AccessToken)
	current, err := s.GetRefreshToken(ctx, persistentRefreshTestHash(rotated.RefreshToken))
	require.NoError(t, err)
	require.True(t, original.ExpiresAt.Equal(current.ExpiresAt))
	require.True(t, original.FamilyExpiresAt.Equal(current.FamilyExpiresAt))
	_, err = newAuth().RefreshTokenPair(ctx, pair.RefreshToken)
	require.ErrorIs(t, err, service.ErrRefreshTokenInvalid)
	require.NoError(t, s.DeleteTokenFamily(ctx, current.FamilyID))
	_, err = newAuth().RefreshTokenPair(ctx, rotated.RefreshToken)
	require.ErrorIs(t, err, service.ErrRefreshTokenInvalid)
}

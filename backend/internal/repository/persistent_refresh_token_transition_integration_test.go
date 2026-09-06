//go:build integration

package repository

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
)

const refreshTransitionTestPassword = "transition-test-legacy-password"

func refreshTransitionPG(t *testing.T) *sql.DB {
	t.Helper()
	schema := "transition_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err := integrationDB.Exec(`CREATE SCHEMA ` + schema)
	require.NoError(t, err)
	u, err := url.Parse(integrationPostgresDSN)
	require.NoError(t, err)
	query := u.Query()
	query.Set("search_path", schema)
	u.RawQuery = query.Encode()
	db, err := sql.Open("postgres", u.String())
	require.NoError(t, err)
	db.SetMaxOpenConns(8)
	t.Cleanup(func() {
		require.NoError(t, db.Close())
		_, err := integrationDB.Exec(`DROP SCHEMA ` + schema + ` CASCADE`)
		require.NoError(t, err)
	})
	for _, name := range []string{"238_persistent_refresh_tokens.sql", "240_refresh_token_authority_transition.sql", "241_refresh_token_transition_group_fence.sql"} {
		migration, err := migrations.FS.ReadFile(name)
		require.NoError(t, err)
		_, err = db.Exec(string(migration))
		require.NoError(t, err)
	}
	return db
}

func refreshTransitionRedis(t *testing.T) (*tcredis.RedisContainer, *redis.Client) {
	return refreshTransitionRedisImage(t, "redis:7.4-alpine")
}

func refreshTransitionRedisImage(t *testing.T, image string) (*tcredis.RedisContainer, *redis.Client) {
	t.Helper()
	ctx := context.Background()
	c, err := tcredis.Run(ctx, image,
		testcontainers.WithCmdArgs("--aclfile", "/data/refresh-transition.acl", "--save", ""),
		testcontainers.WithFiles(testcontainers.ContainerFile{Reader: strings.NewReader("user default on >" + refreshTransitionTestPassword + " ~* &* +@all\n"), ContainerFilePath: "/data/refresh-transition.acl", FileMode: 0o644}))
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		require.NoError(t, c.Terminate(ctx))
	})
	endpoint, err := c.ConnectionString(ctx)
	require.NoError(t, err)
	opts, err := redis.ParseURL(endpoint)
	require.NoError(t, err)
	opts.Password = refreshTransitionTestPassword
	opts.MaxRetries = -1
	client := redis.NewClient(opts)
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	require.NoError(t, client.Ping(ctx).Err())
	return c, client
}

func refreshTransitionOptions(t *testing.T, source *redis.Client) LegacyRefreshTransitionOptions {
	t.Helper()
	info, err := source.Info(context.Background(), "server").Result()
	require.NoError(t, err)
	runID := ""
	for _, line := range strings.Split(info, "\n") {
		if strings.HasPrefix(line, "run_id:") {
			runID = strings.TrimSpace(strings.TrimPrefix(line, "run_id:"))
		}
	}
	secret, err := NewLegacyRefreshTransitionRecoverySecret()
	require.NoError(t, err)
	return LegacyRefreshTransitionOptions{Source: source, ExpectedRunID: runID, RecoverySecret: secret}
}

func refreshTransitionFenceClient(t *testing.T, options LegacyRefreshTransitionOptions, resultID string) *redis.Client {
	t.Helper()
	o := *options.Source.Options()
	o.Dialer = nil
	o.Username = "xiass-refresh-transition-" + resultID
	o.Password = hex.EncodeToString(options.RecoverySecret)
	client := redis.NewClient(&o)
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	return client
}

func refreshTransitionSeed(t *testing.T, client *redis.Client, hash string, d *service.RefreshTokenData, ttl time.Duration) {
	t.Helper()
	ctx := context.Background()
	legacy := NewRefreshTokenCache(client)
	require.NoError(t, legacy.StoreRefreshToken(ctx, hash, d, ttl))
	require.NoError(t, legacy.AddToUserTokenSet(ctx, d.UserID, hash, ttl))
	require.NoError(t, legacy.AddToFamilyTokenSet(ctx, d.FamilyID, hash, ttl))
}

func TestPersistentRefreshTransitionPGAdoptsAndFencesExistingConnections(t *testing.T) {
	ctx := context.Background()
	db := refreshTransitionPG(t)
	container, source := refreshTransitionRedis(t)
	options := refreshTransitionOptions(t, source)
	store := NewPersistentRefreshTokenStore(db)
	d := refreshTransitionTestData()
	hash := persistentRefreshTestHash(t.Name())
	refreshTransitionSeed(t, source, hash, d, 2*time.Hour)
	expires, err := source.Do(ctx, "PEXPIRETIME", refreshTokenKey(hash)).Int64()
	require.NoError(t, err)
	oldConnection := source.Conn()
	defer oldConnection.Close()
	require.NoError(t, oldConnection.Ping(ctx).Err(), "authenticate this physical connection before fencing")
	result, err := store.AdoptLegacyRefreshTokens(ctx, options)
	require.NoError(t, err)
	require.EqualValues(t, 1, result.Imported)
	_, err = oldConnection.Get(ctx, refreshTokenKey(hash)).Result()
	require.Error(t, err)
	require.Contains(t, err.Error(), "NOPERM", "existing authenticated connection must lose commands, not just fail a future AUTH")
	require.Error(t, oldConnection.Set(ctx, refreshTokenKey(hash), "late writer", time.Hour).Err())
	_, err = NewRefreshTokenCache(source).GetRefreshToken(ctx, hash)
	require.Error(t, err)
	got, err := store.GetRefreshToken(ctx, hash)
	require.NoError(t, err)
	require.Equal(t, d, got)
	var until time.Time
	require.NoError(t, db.QueryRow(`SELECT valid_until FROM refresh_tokens WHERE token_hash=$1`, hash).Scan(&until))
	require.Equal(t, time.UnixMilli(expires).UTC(), until)
	indexes, err := store.GetFamilyTokenHashes(ctx, d.FamilyID)
	require.NoError(t, err)
	require.Equal(t, []string{hash}, indexes)
	indexes, err = store.GetUserTokenHashes(ctx, d.UserID)
	require.NoError(t, err)
	require.Equal(t, []string{hash}, indexes)
	_, err = store.ConsumeRefreshToken(ctx, hash)
	require.NoError(t, err)
	retry, err := store.AdoptLegacyRefreshTokens(ctx, options)
	require.NoError(t, err)
	require.Equal(t, result, retry)
	assertPersistentRefreshMissing(t, store, hash)
	for _, statement := range []string{`UPDATE refresh_token_authority SET backend='redis',activated_at=NULL`,
		`UPDATE refresh_token_authority SET activated_at=activated_at+interval '1 second'`,
		`DELETE FROM refresh_token_authority`, `TRUNCATE refresh_token_authority`,
		`DELETE FROM refresh_token_legacy_transition`, `TRUNCATE refresh_token_legacy_transition`,
		`UPDATE refresh_token_legacy_transition SET imported_count=99`} {
		_, err := db.Exec(statement)
		require.Error(t, err)
	}
	// ACL SAVE must survive a real server restart. It is not a temporary CLIENT
	// PAUSE or a check that only affects new connections.
	require.NoError(t, container.Stop(ctx, nil))
	require.NoError(t, container.Start(ctx))
	endpoint, err := container.ConnectionString(ctx)
	require.NoError(t, err)
	restartedOptions, err := redis.ParseURL(endpoint)
	require.NoError(t, err)
	restartedOptions.Username = "xiass-refresh-transition-" + result.TransitionID
	restartedOptions.Password = hex.EncodeToString(options.RecoverySecret)
	restartedOptions.MaxRetries = -1
	fenced := redis.NewClient(restartedOptions)
	defer fenced.Close()
	require.Eventually(t, func() bool { return fenced.Ping(ctx).Err() == nil }, 10*time.Second, 100*time.Millisecond)
	oldOptions := *restartedOptions
	oldOptions.Dialer, oldOptions.Username, oldOptions.Password = nil, "default", refreshTransitionTestPassword
	oldAfterRestart := redis.NewClient(&oldOptions)
	defer oldAfterRestart.Close()
	require.ErrorContains(t, oldAfterRestart.Ping(ctx).Err(), "WRONGPASS")
	retry, err = store.AdoptLegacyRefreshTokens(ctx, options)
	require.NoError(t, err)
	require.Equal(t, result, retry, "completed retry is PG-only even after Redis restart")
}

type refreshTransitionLostCommitDriver struct {
	driver.Driver
	commits atomic.Int32
}

type refreshTransitionLostCommitConn struct {
	driver.Conn
	owner *refreshTransitionLostCommitDriver
}

type refreshTransitionLostCommitTx struct {
	driver.Tx
	owner *refreshTransitionLostCommitDriver
}

func (d *refreshTransitionLostCommitDriver) Open(name string) (driver.Conn, error) {
	c, err := d.Driver.Open(name)
	if err != nil {
		return nil, err
	}
	return &refreshTransitionLostCommitConn{Conn: c, owner: d}, nil
}

func (c *refreshTransitionLostCommitConn) BeginTx(ctx context.Context, options driver.TxOptions) (driver.Tx, error) {
	tx, err := c.Conn.(driver.ConnBeginTx).BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	return &refreshTransitionLostCommitTx{Tx: tx, owner: c.owner}, nil
}

func (t *refreshTransitionLostCommitTx) Commit() error {
	if err := t.Tx.Commit(); err != nil {
		return err
	}
	if t.owner.commits.Add(1) == 3 {
		return errors.New("synthetic lost activation commit acknowledgment")
	}
	return nil
}

func TestPersistentRefreshTransitionPGLostCommitRetryDoesNotResurrect(t *testing.T) {
	ctx := context.Background()
	db := refreshTransitionPG(t)
	var schema string
	require.NoError(t, db.QueryRow(`SELECT current_schema()`).Scan(&schema))
	u, err := url.Parse(integrationPostgresDSN)
	require.NoError(t, err)
	query := u.Query()
	query.Set("search_path", schema)
	u.RawQuery = query.Encode()
	driverName := "refresh-transition-lost-commit-" + uuid.NewString()
	sql.Register(driverName, &refreshTransitionLostCommitDriver{Driver: &pq.Driver{}})
	lossyDB, err := sql.Open(driverName, u.String())
	require.NoError(t, err)
	defer lossyDB.Close()
	_, source := refreshTransitionRedis(t)
	options := refreshTransitionOptions(t, source)
	d := refreshTransitionTestData()
	hash := persistentRefreshTestHash(t.Name())
	refreshTransitionSeed(t, source, hash, d, time.Hour)
	result, err := NewPersistentRefreshTokenStore(lossyDB).AdoptLegacyRefreshTokens(ctx, options)
	require.Nil(t, result)
	require.ErrorContains(t, err, "lost activation commit acknowledgment")
	var backend string
	require.NoError(t, db.QueryRow(`SELECT backend FROM refresh_token_authority`).Scan(&backend))
	require.Equal(t, "postgres", backend)
	store := NewPersistentRefreshTokenStore(db)
	require.NoError(t, store.DeleteRefreshToken(ctx, hash))
	result, err = store.AdoptLegacyRefreshTokens(ctx, options)
	require.NoError(t, err)
	require.EqualValues(t, 1, result.Imported)
	assertPersistentRefreshMissing(t, store, hash)
}

func TestPersistentRefreshTransitionPGConcurrentUserRevoke(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db := refreshTransitionPG(t)
	_, source := refreshTransitionRedis(t)
	options := refreshTransitionOptions(t, source)
	d := refreshTransitionTestData()
	hash := persistentRefreshTestHash(t.Name())
	refreshTransitionSeed(t, source, hash, d, time.Hour)
	gateID := int64(731000) + persistentRefreshTestSequence.Add(1)
	gate, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer gate.Rollback()
	_, err = gate.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, gateID)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `CREATE FUNCTION pause_transition_test_insert() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
		PERFORM pg_advisory_xact_lock(`+strconv.FormatInt(gateID, 10)+`); RETURN NEW; END; $$;
		CREATE TRIGGER pause_transition_test_insert BEFORE INSERT ON refresh_tokens FOR EACH ROW EXECUTE FUNCTION pause_transition_test_insert()`)
	require.NoError(t, err)
	store := NewPersistentRefreshTokenStore(db)
	adopted := make(chan error, 1)
	go func() { _, err := store.AdoptLegacyRefreshTokens(ctx, options); adopted <- err }()
	require.Eventually(t, func() bool {
		var waiting bool
		err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE locktype='advisory' AND objid=$1 AND NOT granted)`, gateID).Scan(&waiting)
		return err == nil && waiting
	}, 10*time.Second, 25*time.Millisecond)
	revoked := make(chan error, 1)
	go func() { revoked <- store.DeleteUserRefreshTokens(ctx, d.UserID) }()
	select {
	case err := <-revoked:
		t.Fatalf("revocation escaped activation locks: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	require.NoError(t, gate.Commit())
	require.NoError(t, <-adopted)
	require.NoError(t, <-revoked)
	assertPersistentRefreshMissing(t, store, hash)
	_, err = store.AdoptLegacyRefreshTokens(ctx, options)
	require.NoError(t, err)
	assertPersistentRefreshMissing(t, store, hash)
}

func TestPersistentRefreshTransitionPGPreflightRejectsWithoutDisabling(t *testing.T) {
	for _, tc := range []string{"other database", "unknown key", "unknown principal", "ACL SAVE failure", "missing indexes", "malformed metadata", "no expiry", "wrong run ID", "existing PG revocation"} {
		t.Run(tc, func(t *testing.T) {
			ctx := context.Background()
			db := refreshTransitionPG(t)
			container, source := refreshTransitionRedis(t)
			options := refreshTransitionOptions(t, source)
			d := refreshTransitionTestData()
			hash := persistentRefreshTestHash(tc)
			refreshTransitionSeed(t, source, hash, d, time.Hour)
			switch tc {
			case "other database":
				o := *source.Options()
				o.DB = 1
				other := redis.NewClient(&o)
				defer other.Close()
				require.NoError(t, other.Set(ctx, "other-service", "x", 0).Err())
			case "unknown key":
				require.NoError(t, source.Set(ctx, "other-service:key", "x", 0).Err())
			case "unknown principal":
				require.NoError(t, source.ACLSetUser(ctx, "other-service", "reset", "on", ">test-other", "~*", "+@all").Err())
			case "ACL SAVE failure":
				code, _, err := container.Exec(ctx, []string{"sh", "-c", "rm /data/refresh-transition.acl && mkdir /data/refresh-transition.acl"})
				require.NoError(t, err)
				require.Zero(t, code)
			case "missing indexes":
				require.NoError(t, source.Del(ctx, tokenFamilyKey(d.FamilyID)).Err())
			case "malformed metadata":
				require.NoError(t, source.Set(ctx, refreshTokenKey(hash), `{"refresh_token":"raw-secret-must-not-leak"}`, time.Hour).Err())
			case "no expiry":
				require.NoError(t, source.Persist(ctx, refreshTokenKey(hash)).Err())
			case "wrong run ID":
				options.ExpectedRunID = strings.Repeat("0", 40)
			case "existing PG revocation":
				require.NoError(t, NewPersistentRefreshTokenStore(db).DeleteUserRefreshTokens(ctx, d.UserID))
			}
			result, err := NewPersistentRefreshTokenStore(db).AdoptLegacyRefreshTokens(ctx, options)
			require.Nil(t, result)
			require.ErrorIs(t, err, ErrRefreshTransitionUnsafe)
			require.NotContains(t, err.Error(), "raw-secret-must-not-leak")
			require.NoError(t, source.Ping(ctx).Err(), "preflight must leave existing Redis permissions intact")
			var backend string
			require.NoError(t, db.QueryRow(`SELECT backend FROM refresh_token_authority`).Scan(&backend))
			require.Equal(t, "redis", backend)
		})
	}
	t.Run("no configured aclfile", func(t *testing.T) {
		db := refreshTransitionPG(t)
		options := refreshTransitionOptions(t, integrationRedis)
		_, err := NewPersistentRefreshTokenStore(db).AdoptLegacyRefreshTokens(context.Background(), options)
		require.ErrorIs(t, err, ErrRefreshTransitionUnsafe)
		require.NoError(t, integrationRedis.Ping(context.Background()).Err())
	})
	t.Run("modules are not a proven service boundary", func(t *testing.T) {
		db := refreshTransitionPG(t)
		_, source := refreshTransitionRedisImage(t, redisImageTag)
		_, err := NewPersistentRefreshTokenStore(db).AdoptLegacyRefreshTokens(context.Background(), refreshTransitionOptions(t, source))
		require.ErrorContains(t, err, "module service boundary")
		require.NoError(t, source.Ping(context.Background()).Err())
	})
}

func TestPersistentRefreshTransitionPGRollbackRetryAndNoReplay(t *testing.T) {
	ctx := context.Background()
	db := refreshTransitionPG(t)
	_, source := refreshTransitionRedis(t)
	options := refreshTransitionOptions(t, source)
	d := refreshTransitionTestData()
	hash := persistentRefreshTestHash(t.Name())
	refreshTransitionSeed(t, source, hash, d, time.Hour)
	_, err := db.Exec(`CREATE FUNCTION reject_transition_test_insert() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic transition rollback'; END; $$;
		CREATE TRIGGER reject_transition_test_insert BEFORE INSERT ON refresh_tokens FOR EACH ROW EXECUTE FUNCTION reject_transition_test_insert()`)
	require.NoError(t, err)
	store := NewPersistentRefreshTokenStore(db)
	result, err := store.AdoptLegacyRefreshTokens(ctx, options)
	require.Nil(t, result)
	require.ErrorContains(t, err, "synthetic transition rollback")
	require.Error(t, source.Ping(ctx).Err(), "failed import must not un-fence legacy writers")
	for _, table := range []string{"refresh_tokens", "refresh_token_families", "refresh_token_users", "refresh_token_issuances"} {
		var n int
		require.NoError(t, db.QueryRow(`SELECT count(*) FROM `+table).Scan(&n))
		require.Zero(t, n)
	}
	var backend, state string
	require.NoError(t, db.QueryRow(`SELECT backend FROM refresh_token_authority`).Scan(&backend))
	require.Equal(t, "redis", backend)
	require.NoError(t, db.QueryRow(`SELECT state FROM refresh_token_legacy_transition`).Scan(&state))
	require.Equal(t, "fenced", state)
	_, err = db.Exec(`DROP TRIGGER reject_transition_test_insert ON refresh_tokens`)
	require.NoError(t, err)
	result, err = store.AdoptLegacyRefreshTokens(ctx, options)
	require.NoError(t, err)
	require.EqualValues(t, 1, result.Imported)
	require.NoError(t, store.DeleteTokenFamily(ctx, d.FamilyID))
	retry, err := store.AdoptLegacyRefreshTokens(ctx, options)
	require.NoError(t, err)
	require.Equal(t, result, retry)
	assertPersistentRefreshMissing(t, store, hash)
	bad := options
	bad.RecoverySecret = make([]byte, 32)
	_, err = store.AdoptLegacyRefreshTokens(ctx, bad)
	require.ErrorIs(t, err, ErrRefreshTransitionUnsafe)
}

func TestPersistentRefreshTransitionPGExpiryMetadataAndActivationGuard(t *testing.T) {
	ctx := context.Background()
	db := refreshTransitionPG(t)
	_, source := refreshTransitionRedis(t)
	options := refreshTransitionOptions(t, source)
	_, err := db.Exec(`UPDATE refresh_token_authority SET backend='postgres',activated_at=clock_timestamp()`)
	require.Error(t, err, "marker alone cannot claim migration completion")
	d := refreshTransitionTestData()
	d.FamilyExpiresAt = time.Time{}
	d.ExpiresAt = d.CreatedAt.Add(3 * 24 * time.Hour)
	hash := persistentRefreshTestHash("missing-family")
	refreshTransitionSeed(t, source, hash, d, 7*24*time.Hour)
	expired := refreshTransitionTestData()
	expired.FamilyID = strings.Repeat("c", 32)
	now := time.Now().UTC().Truncate(time.Microsecond)
	// Keep an exact seven-day lifetime with sub-microsecond metadata, while
	// leaving the Redis key alive to prove metadata expiry controls adoption.
	expired.CreatedAt = now.Add(-8*24*time.Hour + 999*time.Nanosecond)
	expired.ExpiresAt = expired.CreatedAt.Add(7 * 24 * time.Hour)
	expired.FamilyExpiresAt = expired.ExpiresAt
	require.Equal(t, 7*24*time.Hour, expired.FamilyExpiresAt.Sub(expired.CreatedAt))
	require.True(t, expired.ExpiresAt.Before(now))
	expiredHash := persistentRefreshTestHash("expired-json")
	refreshTransitionSeed(t, source, expiredHash, expired, time.Hour)
	result, err := NewPersistentRefreshTokenStore(db).AdoptLegacyRefreshTokens(ctx, options)
	require.NoError(t, err)
	require.EqualValues(t, 1, result.Imported)
	require.EqualValues(t, 1, result.Expired)
	got, err := NewPersistentRefreshTokenStore(db).GetRefreshToken(ctx, hash)
	require.NoError(t, err)
	require.Equal(t, d.ExpiresAt, got.ExpiresAt)
	require.Equal(t, d.ExpiresAt, got.FamilyExpiresAt)
	var expiredRows int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM refresh_tokens WHERE token_hash=$1`, expiredHash).Scan(&expiredRows))
	require.Zero(t, expiredRows, "expired metadata is skipped, not merely hidden by active-read filtering")
	fenced := refreshTransitionFenceClient(t, options, result.TransitionID)
	remaining, err := fenced.PTTL(ctx, refreshTokenKey(expiredHash)).Result()
	require.NoError(t, err)
	require.Positive(t, remaining, "the expired token's Redis key itself has not expired")
}

func TestPersistentRefreshTransitionPGRejectsReplicaAndPromotedHistory(t *testing.T) {
	ctx := context.Background()
	db := refreshTransitionPG(t)
	masterContainer, master := refreshTransitionRedis(t)
	_, replica := refreshTransitionRedis(t)
	masterIP, err := masterContainer.ContainerIP(ctx)
	require.NoError(t, err)
	require.NoError(t, replica.ConfigSet(ctx, "masterauth", refreshTransitionTestPassword).Err())
	require.NoError(t, replica.Do(ctx, "REPLICAOF", masterIP, "6379").Err())
	require.Eventually(t, func() bool {
		info, err := replica.Info(ctx, "replication").Result()
		return err == nil && strings.Contains(info, "master_link_status:up")
	}, 20*time.Second, 100*time.Millisecond)
	_, err = NewPersistentRefreshTokenStore(db).AdoptLegacyRefreshTokens(ctx, refreshTransitionOptions(t, master))
	require.ErrorIs(t, err, ErrRefreshTransitionUnsafe)
	options := refreshTransitionOptions(t, replica)
	_, err = NewPersistentRefreshTokenStore(db).AdoptLegacyRefreshTokens(ctx, options)
	require.ErrorIs(t, err, ErrRefreshTransitionUnsafe)
	require.NoError(t, replica.Do(ctx, "REPLICAOF", "NO", "ONE").Err())
	_, err = NewPersistentRefreshTokenStore(db).AdoptLegacyRefreshTokens(ctx, options)
	require.ErrorIs(t, err, ErrRefreshTransitionUnsafe, "promotion does not turn a replica into an admissible source")
	require.NoError(t, master.Ping(ctx).Err())
	require.NoError(t, replica.Ping(ctx).Err())
}

func TestPersistentRefreshTransitionProductionProviderRedisPromotion(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	db := refreshTransitionPG(t)
	primaryContainer, primary := refreshTransitionRedis(t)
	options := refreshTransitionOptions(t, primary)
	user := mustCreateUser(t, testEntClient(t), &service.User{Balance: 17.25})
	userRepo := NewUserRepository(testEntClient(t), integrationDB)
	cfg := &config.Config{JWT: config.JWTConfig{Secret: "transition-recovery-test-only-signing-secret", ExpireHour: 168, RefreshTokenExpireDays: 7, RefreshTokenStore: "redis"}}
	newAuth := func(cache service.RefreshTokenCache) *service.AuthService {
		return service.NewAuthService(testEntClient(t), userRepo, nil, cache, cfg, nil, nil, nil, nil, nil, nil, nil, nil)
	}
	legacy, err := NewRefreshTokenStore(db, primary, cfg)
	require.NoError(t, err)
	oldPair, err := newAuth(legacy).GenerateTokenPair(ctx, user, "")
	require.NoError(t, err)
	controlPair, err := newAuth(legacy).GenerateTokenPair(ctx, user, "")
	require.NoError(t, err)
	oldHash, controlHash := persistentRefreshTestHash(oldPair.RefreshToken), persistentRefreshTestHash(controlPair.RefreshToken)
	original, err := legacy.GetRefreshToken(ctx, controlHash)
	require.NoError(t, err)
	result, err := NewPersistentRefreshTokenStore(db).AdoptLegacyRefreshTokens(ctx, options)
	require.NoError(t, err)
	require.EqualValues(t, 2, result.Imported)
	_, err = legacy.GetRefreshToken(ctx, controlHash)
	require.ErrorIs(t, err, ErrRefreshTokenAuthority)
	_, err = NewRefreshTokenStore(db, primary, cfg)
	require.ErrorIs(t, err, ErrRefreshTokenAuthority)
	cfg.JWT.RefreshTokenStore = "postgres"
	persistent, err := NewRefreshTokenStore(db, primary, cfg)
	require.NoError(t, err)
	_, ok := persistent.(service.RefreshTokenIssuancePreparer)
	require.True(t, ok)
	got, err := persistent.GetRefreshToken(ctx, controlHash)
	require.NoError(t, err)
	require.Equal(t, original.FamilyExpiresAt.Truncate(time.Microsecond), got.FamilyExpiresAt)
	// A new disposable stale replica is introduced AFTER the supported standalone
	// adoption. This is recovery fault injection, not an HA-source adoption claim.
	fenced := refreshTransitionFenceClient(t, options, result.TransitionID)
	replicaContainer, replica := refreshTransitionRedis(t)
	primaryIP, err := primaryContainer.ContainerIP(ctx)
	require.NoError(t, err)
	require.NoError(t, replica.ConfigSet(ctx, "masteruser", "xiass-refresh-transition-"+result.TransitionID).Err())
	require.NoError(t, replica.ConfigSet(ctx, "masterauth", hex.EncodeToString(options.RecoverySecret)).Err())
	require.NoError(t, replica.Do(ctx, "REPLICAOF", primaryIP, "6379").Err())
	require.Eventually(t, func() bool {
		for _, hash := range []string{oldHash, controlHash} {
			if _, err := NewRefreshTokenCache(replica).GetRefreshToken(ctx, hash); err != nil {
				return false
			}
		}
		return true
	}, 20*time.Second, 100*time.Millisecond)
	docker := func(args ...string) {
		t.Helper()
		output, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
		require.NoError(t, err, "test container operation: %s", output)
	}
	paused := false
	t.Cleanup(func() {
		if paused {
			cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = exec.CommandContext(cleanup, "docker", "unpause", replicaContainer.GetContainerID()).Run()
		}
	})
	docker("pause", replicaContainer.GetContainerID())
	paused = true
	killed, err := fenced.Do(ctx, "CLIENT", "KILL", "TYPE", "replica").Int()
	require.NoError(t, err)
	require.GreaterOrEqual(t, killed, 1)
	auth := newAuth(persistent)
	require.NoError(t, auth.RevokeRefreshToken(ctx, oldPair.RefreshToken))
	require.NoError(t, fenced.Del(ctx, refreshTokenKey(oldHash)).Err(), "test-only acknowledged shadow DEL is lost by the paused replica")
	docker("kill", "--signal", "KILL", primaryContainer.GetContainerID())
	docker("unpause", replicaContainer.GetContainerID())
	paused = false
	require.NoError(t, replica.Do(ctx, "REPLICAOF", "NO", "ONE").Err())
	_, err = NewRefreshTokenCache(replica).GetRefreshToken(ctx, oldHash)
	require.NoError(t, err, "stale promoted Redis actually contains revoked metadata")
	selected, err := NewRefreshTokenStore(db, replica, cfg)
	require.NoError(t, err)
	invalid, err := newAuth(selected).RefreshTokenPair(ctx, oldPair.RefreshToken)
	require.Nil(t, invalid)
	require.ErrorIs(t, err, service.ErrRefreshTokenInvalid)
	valid, err := newAuth(selected).RefreshTokenPair(ctx, controlPair.RefreshToken)
	require.NoError(t, err)
	require.NotEmpty(t, valid.AccessToken)
	rotated, err := selected.GetRefreshToken(ctx, persistentRefreshTestHash(valid.RefreshToken))
	require.NoError(t, err)
	require.Equal(t, original.FamilyExpiresAt.Truncate(time.Microsecond), rotated.FamilyExpiresAt, "rotation must not restart seven days")
	// A credential appearing only on the promoted source is not a GET-miss import.
	unknown := refreshTransitionTestData()
	unknownHash := persistentRefreshTestHash("untrusted-promoted-only")
	refreshTransitionSeed(t, replica, unknownHash, unknown, time.Hour)
	_, err = selected.GetRefreshToken(ctx, unknownHash)
	require.ErrorIs(t, err, service.ErrRefreshTokenNotFound)
	before, _ := json.Marshal(result)
	retry, err := NewPersistentRefreshTokenStore(db).AdoptLegacyRefreshTokens(ctx, options)
	require.NoError(t, err)
	after, _ := json.Marshal(retry)
	require.Equal(t, string(before), string(after))
}

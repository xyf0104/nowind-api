package repository

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestUserGroupAccountAllowlistRepositoryCachesAndClonesAllowedAccountIDs(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	repo := NewUserGroupAccountAllowlistRepository(db)
	expectUserGroupAccountAllowlistQuery(mock, 4, 8,
		sqlmock.NewRows([]string{"account_id"}).AddRow(int64(3)).AddRow(int64(9)))

	accountIDs, restricted, err := repo.GetAllowedAccountIDs(context.Background(), 4, 8)
	require.NoError(t, err)
	require.True(t, restricted)
	require.Equal(t, []int64{3, 9}, accountIDs)
	accountIDs[0] = 99

	cachedIDs, cachedRestricted, err := repo.GetAllowedAccountIDs(context.Background(), 4, 8)
	require.NoError(t, err)
	require.True(t, cachedRestricted)
	require.Equal(t, []int64{3, 9}, cachedIDs, "callers must not mutate the cached slice")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUserGroupAccountAllowlistRepositoryScopeWithoutDetailsRemainsRestricted(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	repo := NewUserGroupAccountAllowlistRepository(db)
	expectUserGroupAccountAllowlistQuery(mock, 4, 8,
		sqlmock.NewRows([]string{"account_id"}).AddRow(nil))

	accountIDs, restricted, err := repo.GetAllowedAccountIDs(context.Background(), 4, 8)
	require.NoError(t, err)
	require.True(t, restricted)
	require.Empty(t, accountIDs)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUserGroupAccountAllowlistRepositoryMissingScopeIsUnrestricted(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	repo := NewUserGroupAccountAllowlistRepository(db)
	expectUserGroupAccountAllowlistQuery(mock, 4, 8, sqlmock.NewRows([]string{"account_id"}))

	accountIDs, restricted, err := repo.GetAllowedAccountIDs(context.Background(), 4, 8)
	require.NoError(t, err)
	require.False(t, restricted)
	require.Empty(t, accountIDs)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUserGroupAccountAllowlistRepositoryReplaceInvalidatesLocalCache(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	repo := NewUserGroupAccountAllowlistRepository(db)
	expectUserGroupAccountAllowlistQuery(mock, 4, 8,
		sqlmock.NewRows([]string{"account_id"}).AddRow(int64(3)))
	_, _, err = repo.GetAllowedAccountIDs(context.Background(), 4, 8)
	require.NoError(t, err)

	expectUserGroupAccountAllowlistReplace(mock, 4, 8, true)
	err = repo.ReplaceAllowedAccountIDs(context.Background(), 4, 8, []int64{9, 3, 9})
	require.NoError(t, err)

	expectUserGroupAccountAllowlistQuery(mock, 4, 8,
		sqlmock.NewRows([]string{"account_id"}).AddRow(int64(3)).AddRow(int64(9)))
	accountIDs, restricted, err := repo.GetAllowedAccountIDs(context.Background(), 4, 8)
	require.NoError(t, err)
	require.True(t, restricted)
	require.Equal(t, []int64{3, 9}, accountIDs)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUserGroupAccountAllowlistRepositoryReplaceEmptyPersistsRestrictedScope(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	repo := NewUserGroupAccountAllowlistRepository(db)
	expectUserGroupAccountAllowlistReplace(mock, 4, 8, false)
	err = repo.ReplaceAllowedAccountIDs(context.Background(), 4, 8, nil)
	require.NoError(t, err)

	expectUserGroupAccountAllowlistQuery(mock, 4, 8,
		sqlmock.NewRows([]string{"account_id"}).AddRow(nil))
	accountIDs, restricted, err := repo.GetAllowedAccountIDs(context.Background(), 4, 8)
	require.NoError(t, err)
	require.True(t, restricted)
	require.Empty(t, accountIDs)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUserGroupAccountAllowlistRepositoryRestoreDeletesScopeAndInvalidatesCache(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	repo := NewUserGroupAccountAllowlistRepository(db)
	expectUserGroupAccountAllowlistQuery(mock, 4, 8,
		sqlmock.NewRows([]string{"account_id"}).AddRow(int64(3)))
	_, _, err = repo.GetAllowedAccountIDs(context.Background(), 4, 8)
	require.NoError(t, err)

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("SELECT pg_advisory_xact_lock($1)")).
		WithArgs(sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("DELETE FROM user_group_account_allowlist_scopes").
		WithArgs(int64(4), int64(8)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	require.NoError(t, repo.RestoreAllowedAccountIDs(context.Background(), 4, 8))

	expectUserGroupAccountAllowlistQuery(mock, 4, 8, sqlmock.NewRows([]string{"account_id"}))
	accountIDs, restricted, err := repo.GetAllowedAccountIDs(context.Background(), 4, 8)
	require.NoError(t, err)
	require.False(t, restricted)
	require.Empty(t, accountIDs)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUserGroupAccountAllowlistRepositoryCoalescesConcurrentCacheMisses(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	repo := NewUserGroupAccountAllowlistRepository(db)
	mock.ExpectQuery("SELECT a.account_id").
		WithArgs(int64(4), int64(8)).
		WillDelayFor(40 * time.Millisecond).
		WillReturnRows(sqlmock.NewRows([]string{"account_id"}).AddRow(int64(3)))

	const callers = 12
	var wg sync.WaitGroup
	errs := make(chan error, callers)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ids, restricted, loadErr := repo.GetAllowedAccountIDs(context.Background(), 4, 8)
			if loadErr == nil && (!restricted || len(ids) != 1 || ids[0] != 3) {
				loadErr = errors.New("unexpected allowlist state")
			}
			errs <- loadErr
		}()
	}
	wg.Wait()
	close(errs)
	for loadErr := range errs {
		require.NoError(t, loadErr)
	}
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUserGroupAccountAllowlistRepositoryNotificationInvalidatesOnlyTargetScope(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	contract := NewUserGroupAccountAllowlistRepository(db)
	repo := contract.(*userGroupAccountAllowlistRepository)
	expectUserGroupAccountAllowlistQuery(mock, 4, 8,
		sqlmock.NewRows([]string{"account_id"}).AddRow(int64(3)))
	expectUserGroupAccountAllowlistQuery(mock, 5, 8,
		sqlmock.NewRows([]string{"account_id"}).AddRow(int64(7)))
	_, _, err = contract.GetAllowedAccountIDs(context.Background(), 4, 8)
	require.NoError(t, err)
	_, _, err = contract.GetAllowedAccountIDs(context.Background(), 5, 8)
	require.NoError(t, err)

	repo.handleInvalidationNotification(&pq.Notification{Extra: "4:8"})
	expectUserGroupAccountAllowlistQuery(mock, 4, 8,
		sqlmock.NewRows([]string{"account_id"}).AddRow(int64(9)))
	ids, _, err := contract.GetAllowedAccountIDs(context.Background(), 4, 8)
	require.NoError(t, err)
	require.Equal(t, []int64{9}, ids)

	ids, _, err = contract.GetAllowedAccountIDs(context.Background(), 5, 8)
	require.NoError(t, err)
	require.Equal(t, []int64{7}, ids)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUserGroupAccountAllowlistRepositoryUnrelatedScopeNotificationDoesNotInterruptConcurrentLoad(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	contract := NewUserGroupAccountAllowlistRepository(db)
	repo := contract.(*userGroupAccountAllowlistRepository)
	mock.ExpectQuery("SELECT a.account_id").
		WithArgs(int64(4), int64(8)).
		WillDelayFor(100 * time.Millisecond).
		WillReturnRows(sqlmock.NewRows([]string{"account_id"}).AddRow(int64(3)))

	type loadResult struct {
		accountIDs []int64
		restricted bool
		err        error
	}
	resultCh := make(chan loadResult, 1)
	go func() {
		accountIDs, restricted, loadErr := contract.GetAllowedAccountIDs(context.Background(), 4, 8)
		resultCh <- loadResult{accountIDs: accountIDs, restricted: restricted, err: loadErr}
	}()

	require.Eventually(t, func() bool {
		return db.Stats().InUse > 0
	}, time.Second, 5*time.Millisecond, "allowlist query did not start")
	repo.handleInvalidationNotification(&pq.Notification{Extra: "5:8"})

	result := <-resultCh
	require.NoError(t, result.err)
	require.True(t, result.restricted)
	require.Equal(t, []int64{3}, result.accountIDs)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUserGroupAccountAllowlistRepositoryTargetScopeInvalidationRejectsStaleCacheWrite(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	repo := NewUserGroupAccountAllowlistRepository(db).(*userGroupAccountAllowlistRepository)
	key := userGroupAccountAllowlistScopeKey{userID: 4, groupID: 8}
	version := repo.cache.version(key)
	repo.handleInvalidationNotification(&pq.Notification{Extra: "4:8"})

	written := repo.cache.setIfVersion(
		key,
		userGroupAccountAllowlistState{accountIDs: []int64{3}, restricted: true},
		time.Minute,
		version,
	)
	require.False(t, written)
	_, cached := repo.cache.get(key, time.Now())
	require.False(t, cached)
}

func TestUserGroupAccountAllowlistRepositorySingleConnectionPoolDisablesListener(t *testing.T) {
	db, err := sql.Open("postgres", "host=127.0.0.1 port=1 user=xiass_listener_test sslmode=disable connect_timeout=1")
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })

	repo := NewUserGroupAccountAllowlistRepository(db).(*userGroupAccountAllowlistRepository)
	require.False(t, repo.listenerEnabled)
	require.False(t, repo.listenerHealthy.Load())
	require.Equal(t, userGroupAccountAllowlistFallbackCacheTTL, repo.cacheTTL())
	require.Zero(t, db.Stats().InUse, "disabled listener must not reserve the only connection")
}

func TestUserGroupAccountAllowlistRepositoryListenerFailureClearsCacheAndFallsBackToShortTTL(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	repo := NewUserGroupAccountAllowlistRepository(db).(*userGroupAccountAllowlistRepository)
	key := userGroupAccountAllowlistScopeKey{userID: 4, groupID: 8}
	require.True(t, repo.cache.setIfVersion(
		key,
		userGroupAccountAllowlistState{accountIDs: []int64{3}, restricted: true},
		time.Minute,
		repo.cache.version(key),
	))
	repo.listenerHealthy.Store(true)

	repo.markInvalidationListenerUnavailable()

	require.False(t, repo.listenerHealthy.Load())
	require.Equal(t, userGroupAccountAllowlistFallbackCacheTTL, repo.cacheTTL())
	_, cached := repo.cache.get(key, time.Now())
	require.False(t, cached)
}

func TestUserGroupAccountAllowlistRepositoryRollsBackOnInsertError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	repo := NewUserGroupAccountAllowlistRepository(db)
	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock").
		WithArgs(sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("INSERT INTO user_group_account_allowlist_scopes").
		WithArgs(int64(4), int64(8)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("DELETE FROM user_group_account_allowlists").
		WithArgs(int64(4), int64(8)).
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec("INSERT INTO user_group_account_allowlists").
		WithArgs(int64(4), int64(8), sqlmock.AnyArg()).
		WillReturnError(errors.New("insert unavailable"))
	mock.ExpectRollback()

	err = repo.ReplaceAllowedAccountIDs(context.Background(), 4, 8, []int64{3})
	require.ErrorContains(t, err, "insert unavailable")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUserGroupAccountAllowlistRepositoryRejectsInvalidAccountIDs(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	repo := NewUserGroupAccountAllowlistRepository(db)
	err = repo.ReplaceAllowedAccountIDs(context.Background(), 4, 8, []int64{1, 0})
	require.ErrorContains(t, err, "account id must be positive")
	require.NoError(t, mock.ExpectationsWereMet())
}

func expectUserGroupAccountAllowlistQuery(mock sqlmock.Sqlmock, userID, groupID int64, rows *sqlmock.Rows) {
	mock.ExpectQuery("SELECT a.account_id").
		WithArgs(userID, groupID).
		WillReturnRows(rows)
}

func expectUserGroupAccountAllowlistReplace(mock sqlmock.Sqlmock, userID, groupID int64, withAccounts bool) {
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("SELECT pg_advisory_xact_lock($1)")).
		WithArgs(sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("INSERT INTO user_group_account_allowlist_scopes").
		WithArgs(userID, groupID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("DELETE FROM user_group_account_allowlists").
		WithArgs(userID, groupID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	if withAccounts {
		mock.ExpectExec("INSERT INTO user_group_account_allowlists").
			WithArgs(userID, groupID, sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(0, 2))
	}
	mock.ExpectCommit()
}

package repository

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"math/bits"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"golang.org/x/sync/singleflight"
)

const (
	userGroupAccountAllowlistCacheLimit       = 16_384
	userGroupAccountAllowlistFallbackCacheTTL = 2 * time.Second
	userGroupAccountAllowlistListenerCacheTTL = 30 * time.Second
	userGroupAccountAllowlistListenerPoll     = 2 * time.Second
	userGroupAccountAllowlistListenerRetry    = time.Second
	userGroupAccountAllowlistNotifyChannel    = "xiass_user_group_account_allowlist"
)

type userGroupAccountAllowlistScopeKey struct {
	userID  int64
	groupID int64
}

type userGroupAccountAllowlistState struct {
	accountIDs []int64
	restricted bool
}

type userGroupAccountAllowlistCacheEntry struct {
	state     userGroupAccountAllowlistState
	expiresAt time.Time
}

type userGroupAccountAllowlistCacheVersion struct {
	epoch uint64
	scope uint64
}

type userGroupAccountAllowlistCache struct {
	mu             sync.RWMutex
	entries        map[userGroupAccountAllowlistScopeKey]userGroupAccountAllowlistCacheEntry
	scopeRevisions map[userGroupAccountAllowlistScopeKey]uint64
	epoch          uint64
	limit          int
}

func newUserGroupAccountAllowlistCache(limit int) *userGroupAccountAllowlistCache {
	if limit <= 0 {
		limit = userGroupAccountAllowlistCacheLimit
	}
	return &userGroupAccountAllowlistCache{
		entries:        make(map[userGroupAccountAllowlistScopeKey]userGroupAccountAllowlistCacheEntry),
		scopeRevisions: make(map[userGroupAccountAllowlistScopeKey]uint64),
		limit:          limit,
	}
}

func (c *userGroupAccountAllowlistCache) get(key userGroupAccountAllowlistScopeKey, now time.Time) (userGroupAccountAllowlistState, bool) {
	if c == nil {
		return userGroupAccountAllowlistState{}, false
	}
	c.mu.RLock()
	entry, ok := c.entries[key]
	c.mu.RUnlock()
	if !ok {
		return userGroupAccountAllowlistState{}, false
	}
	if !now.Before(entry.expiresAt) {
		c.delete(key)
		return userGroupAccountAllowlistState{}, false
	}
	return cloneUserGroupAccountAllowlistState(entry.state), true
}

func (c *userGroupAccountAllowlistCache) version(key userGroupAccountAllowlistScopeKey) userGroupAccountAllowlistCacheVersion {
	if c == nil {
		return userGroupAccountAllowlistCacheVersion{}
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return userGroupAccountAllowlistCacheVersion{
		epoch: c.epoch,
		scope: c.scopeRevisions[key],
	}
}

func (c *userGroupAccountAllowlistCache) setIfVersion(
	key userGroupAccountAllowlistScopeKey,
	state userGroupAccountAllowlistState,
	ttl time.Duration,
	version userGroupAccountAllowlistCacheVersion,
) bool {
	if c == nil || ttl <= 0 {
		return false
	}
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.epoch != version.epoch || c.scopeRevisions[key] != version.scope {
		return false
	}
	if _, exists := c.entries[key]; !exists && len(c.entries) >= c.limit {
		for existingKey, entry := range c.entries {
			if !now.Before(entry.expiresAt) {
				delete(c.entries, existingKey)
			}
		}
		if len(c.entries) >= c.limit {
			for existingKey := range c.entries {
				delete(c.entries, existingKey)
				break
			}
		}
	}
	c.entries[key] = userGroupAccountAllowlistCacheEntry{
		state:     cloneUserGroupAccountAllowlistState(state),
		expiresAt: now.Add(ttl),
	}
	return true
}

func (c *userGroupAccountAllowlistCache) delete(key userGroupAccountAllowlistScopeKey) {
	if c == nil {
		return
	}
	c.mu.Lock()
	delete(c.entries, key)
	c.mu.Unlock()
}

func (c *userGroupAccountAllowlistCache) invalidate(key userGroupAccountAllowlistScopeKey) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.scopeRevisions[key]++
	delete(c.entries, key)
	c.mu.Unlock()
}

func (c *userGroupAccountAllowlistCache) clear() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.epoch++
	c.entries = make(map[userGroupAccountAllowlistScopeKey]userGroupAccountAllowlistCacheEntry)
	c.scopeRevisions = make(map[userGroupAccountAllowlistScopeKey]uint64)
	c.mu.Unlock()
}

type userGroupAccountAllowlistRepository struct {
	db *sql.DB

	cache           *userGroupAccountAllowlistCache
	loads           singleflight.Group
	listenerHealthy atomic.Bool
	listenerEnabled bool
}

func NewUserGroupAccountAllowlistRepository(db *sql.DB) service.UserGroupAccountAllowlistRepository {
	repository := &userGroupAccountAllowlistRepository{
		db:              db,
		cache:           newUserGroupAccountAllowlistCache(userGroupAccountAllowlistCacheLimit),
		listenerEnabled: shouldStartUserGroupAccountAllowlistListener(db),
	}
	if repository.listenerEnabled {
		go repository.runInvalidationListener()
	}
	return repository
}

func (r *userGroupAccountAllowlistRepository) GetAllowedAccountIDs(
	ctx context.Context,
	userID, groupID int64,
) ([]int64, bool, error) {
	if r == nil || r.db == nil {
		return nil, false, errors.New("nil user group account allowlist database")
	}
	if userID <= 0 || groupID <= 0 {
		return nil, false, errors.New("user id and group id must be positive")
	}

	key := userGroupAccountAllowlistScopeKey{userID: userID, groupID: groupID}
	if cached, ok := r.cache.get(key, time.Now()); ok {
		return cached.accountIDs, cached.restricted, nil
	}

	loaded, err, _ := r.loads.Do(userGroupAccountAllowlistSingleflightKey(key), func() (any, error) {
		if cached, ok := r.cache.get(key, time.Now()); ok {
			return cached, nil
		}
		for {
			version := r.cache.version(key)
			state, loadErr := r.loadAllowedAccountIDs(ctx, key)
			if loadErr != nil {
				return nil, loadErr
			}
			if r.cache.setIfVersion(key, state, r.cacheTTL(), version) {
				return state, nil
			}
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
	})
	if err != nil {
		return nil, false, err
	}
	state, ok := loaded.(userGroupAccountAllowlistState)
	if !ok {
		return nil, false, errors.New("invalid user group account allowlist cache state")
	}
	state = cloneUserGroupAccountAllowlistState(state)
	return state.accountIDs, state.restricted, nil
}

func (r *userGroupAccountAllowlistRepository) loadAllowedAccountIDs(
	ctx context.Context,
	key userGroupAccountAllowlistScopeKey,
) (userGroupAccountAllowlistState, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT a.account_id
		FROM user_group_account_allowlist_scopes AS s
		LEFT JOIN user_group_account_allowlists AS a
		  ON a.user_id = s.user_id AND a.group_id = s.group_id
		WHERE s.user_id = $1 AND s.group_id = $2
		ORDER BY a.account_id ASC
	`, key.userID, key.groupID)
	if err != nil {
		return userGroupAccountAllowlistState{}, fmt.Errorf("get user group account allowlist: %w", err)
	}
	defer func() { _ = rows.Close() }()

	state := userGroupAccountAllowlistState{accountIDs: make([]int64, 0)}
	for rows.Next() {
		state.restricted = true
		var accountID sql.NullInt64
		if err := rows.Scan(&accountID); err != nil {
			return userGroupAccountAllowlistState{}, fmt.Errorf("scan user group account allowlist: %w", err)
		}
		if accountID.Valid && accountID.Int64 > 0 {
			state.accountIDs = append(state.accountIDs, accountID.Int64)
		}
	}
	if err := rows.Err(); err != nil {
		return userGroupAccountAllowlistState{}, fmt.Errorf("iterate user group account allowlist: %w", err)
	}
	return state, nil
}

// ReplaceAllowedAccountIDs atomically stores a restricted user x group scope
// and its selected accounts. An empty set remains restricted with no candidates;
// only RestoreAllowedAccountIDs removes the scope.
func (r *userGroupAccountAllowlistRepository) ReplaceAllowedAccountIDs(
	ctx context.Context,
	userID, groupID int64,
	accountIDs []int64,
) (err error) {
	if r == nil || r.db == nil {
		return errors.New("nil user group account allowlist database")
	}
	if userID <= 0 || groupID <= 0 {
		return errors.New("user id and group id must be positive")
	}
	normalized, err := normalizeAllowedAccountIDs(accountIDs)
	if err != nil {
		return err
	}

	key := userGroupAccountAllowlistScopeKey{userID: userID, groupID: groupID}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin user group account allowlist replacement: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	if _, err = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock($1)", allowlistLockKey(userID, groupID)); err != nil {
		return fmt.Errorf("lock user group account allowlist replacement: %w", err)
	}
	if _, err = tx.ExecContext(ctx, `
		INSERT INTO user_group_account_allowlist_scopes (user_id, group_id, created_at, updated_at)
		VALUES ($1, $2, NOW(), NOW())
		ON CONFLICT (user_id, group_id)
		DO UPDATE SET updated_at = NOW()
	`, userID, groupID); err != nil {
		return fmt.Errorf("upsert user group account allowlist scope: %w", err)
	}
	if _, err = tx.ExecContext(ctx, `
		DELETE FROM user_group_account_allowlists
		WHERE user_id = $1 AND group_id = $2
	`, userID, groupID); err != nil {
		return fmt.Errorf("delete user group account allowlist: %w", err)
	}
	if len(normalized) > 0 {
		if _, err = tx.ExecContext(ctx, `
			INSERT INTO user_group_account_allowlists (user_id, group_id, account_id)
			SELECT $1, $2, account_id
			FROM UNNEST($3::BIGINT[]) AS account_id
		`, userID, groupID, pq.Array(normalized)); err != nil {
			return fmt.Errorf("insert user group account allowlist: %w", err)
		}
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit user group account allowlist replacement: %w", err)
	}
	r.invalidateCacheKey(key)
	return nil
}

func (r *userGroupAccountAllowlistRepository) RestoreAllowedAccountIDs(
	ctx context.Context,
	userID, groupID int64,
) (err error) {
	if r == nil || r.db == nil {
		return errors.New("nil user group account allowlist database")
	}
	if userID <= 0 || groupID <= 0 {
		return errors.New("user id and group id must be positive")
	}

	key := userGroupAccountAllowlistScopeKey{userID: userID, groupID: groupID}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin user group account allowlist restore: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if _, err = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock($1)", allowlistLockKey(userID, groupID)); err != nil {
		return fmt.Errorf("lock user group account allowlist restore: %w", err)
	}
	if _, err = tx.ExecContext(ctx, `
		DELETE FROM user_group_account_allowlist_scopes
		WHERE user_id = $1 AND group_id = $2
	`, userID, groupID); err != nil {
		return fmt.Errorf("restore user group account allowlist: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit user group account allowlist restore: %w", err)
	}
	r.invalidateCacheKey(key)
	return nil
}

func (r *userGroupAccountAllowlistRepository) cacheTTL() time.Duration {
	if r != nil && r.listenerHealthy.Load() {
		return userGroupAccountAllowlistListenerCacheTTL
	}
	return userGroupAccountAllowlistFallbackCacheTTL
}

func (r *userGroupAccountAllowlistRepository) invalidateCacheKey(key userGroupAccountAllowlistScopeKey) {
	if r == nil {
		return
	}
	r.cache.invalidate(key)
}

func (r *userGroupAccountAllowlistRepository) invalidateAllCache() {
	if r == nil {
		return
	}
	r.cache.clear()
}

func (r *userGroupAccountAllowlistRepository) runInvalidationListener() {
	if r == nil || !r.listenerEnabled {
		return
	}
	for r.db != nil {
		err := r.listenForInvalidations()
		r.markInvalidationListenerUnavailable()
		if isClosedDatabaseError(err) {
			return
		}
		timer := time.NewTimer(userGroupAccountAllowlistListenerRetry)
		<-timer.C
	}
}

func (r *userGroupAccountAllowlistRepository) listenForInvalidations() error {
	if r == nil || !r.listenerEnabled {
		return errors.New("user group account allowlist listener is disabled")
	}
	connectCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	conn, err := r.db.Conn(connectCtx)
	cancel()
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	if err := installPQNotificationHandler(conn, r.handleInvalidationNotification); err != nil {
		return err
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Second)
		defer cleanupCancel()
		_, _ = conn.ExecContext(cleanupCtx, "UNLISTEN "+userGroupAccountAllowlistNotifyChannel)
		_ = installPQNotificationHandler(conn, nil)
	}()

	listenCtx, listenCancel := context.WithTimeout(context.Background(), 5*time.Second)
	_, err = conn.ExecContext(listenCtx, "LISTEN "+userGroupAccountAllowlistNotifyChannel)
	listenCancel()
	if err != nil {
		return err
	}
	r.invalidateAllCache()
	r.listenerHealthy.Store(true)
	defer r.listenerHealthy.Store(false)

	ticker := time.NewTicker(userGroupAccountAllowlistListenerPoll)
	defer ticker.Stop()
	for range ticker.C {
		pollCtx, pollCancel := context.WithTimeout(context.Background(), time.Second)
		_, err = conn.ExecContext(pollCtx, "SELECT 1")
		pollCancel()
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *userGroupAccountAllowlistRepository) markInvalidationListenerUnavailable() {
	if r == nil {
		return
	}
	r.listenerHealthy.Store(false)
	r.invalidateAllCache()
}

func shouldStartUserGroupAccountAllowlistListener(db *sql.DB) bool {
	if !isPostgresDriver(db) {
		return false
	}
	maxOpenConnections := db.Stats().MaxOpenConnections
	return maxOpenConnections == 0 || maxOpenConnections > 1
}

func (r *userGroupAccountAllowlistRepository) handleInvalidationNotification(notification *pq.Notification) {
	if r == nil || notification == nil {
		return
	}
	userText, groupText, ok := strings.Cut(strings.TrimSpace(notification.Extra), ":")
	if !ok {
		r.invalidateAllCache()
		return
	}
	userID, userErr := strconv.ParseInt(userText, 10, 64)
	groupID, groupErr := strconv.ParseInt(groupText, 10, 64)
	if userErr != nil || groupErr != nil || userID <= 0 || groupID <= 0 {
		r.invalidateAllCache()
		return
	}
	r.invalidateCacheKey(userGroupAccountAllowlistScopeKey{userID: userID, groupID: groupID})
}

func installPQNotificationHandler(conn *sql.Conn, handler func(*pq.Notification)) error {
	if conn == nil {
		return errors.New("nil postgres notification connection")
	}
	return conn.Raw(func(raw any) error {
		var driverConn driver.Conn
		switch value := raw.(type) {
		case *serverTimingConn:
			driverConn = value.Conn
		case driver.Conn:
			driverConn = value
		default:
			return errors.New("unsupported postgres notification connection")
		}
		return setPQNotificationHandler(driverConn, handler)
	})
}

func setPQNotificationHandler(conn driver.Conn, handler func(*pq.Notification)) (err error) {
	defer func() {
		if recover() != nil {
			err = errors.New("unsupported postgres notification driver")
		}
	}()
	pq.SetNotificationHandler(conn, handler)
	return nil
}

func isClosedDatabaseError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, sql.ErrConnDone) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "database is closed") || strings.Contains(message, "connection is already closed")
}

func normalizeAllowedAccountIDs(accountIDs []int64) ([]int64, error) {
	unique := make(map[int64]struct{}, len(accountIDs))
	for _, accountID := range accountIDs {
		if accountID <= 0 {
			return nil, fmt.Errorf("account id must be positive: %d", accountID)
		}
		unique[accountID] = struct{}{}
	}
	normalized := make([]int64, 0, len(unique))
	for accountID := range unique {
		normalized = append(normalized, accountID)
	}
	sort.Slice(normalized, func(i, j int) bool { return normalized[i] < normalized[j] })
	return normalized, nil
}

func cloneUserGroupAccountAllowlistState(state userGroupAccountAllowlistState) userGroupAccountAllowlistState {
	state.accountIDs = append([]int64(nil), state.accountIDs...)
	return state
}

func userGroupAccountAllowlistSingleflightKey(key userGroupAccountAllowlistScopeKey) string {
	return strconv.FormatInt(key.userID, 10) + ":" + strconv.FormatInt(key.groupID, 10)
}

func allowlistLockKey(userID, groupID int64) int64 {
	return int64(uint64(userID)*0x9e3779b97f4a7c15 ^ bits.RotateLeft64(uint64(groupID), 29))
}

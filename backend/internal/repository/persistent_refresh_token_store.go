package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
)

var (
	ErrPersistentRefreshTokenMetadata = errors.New("invalid persistent refresh token metadata")
	ErrPersistentRefreshTokenRejected = errors.New("persistent refresh token store rejected stale or conflicting credential")
)

// PersistentRefreshTokenStore is PostgreSQL-authoritative; runtime cache methods
// never read Redis. Explicit offline adoption is a separate transition API.
// Ordinary Store accepts only trusted, freshly issued metadata, not an unverified legacy
// snapshot. Every Store requires a DB-issued ticket acquired before generation.
// CreatedAt is never a revocation fence. Missing family deadlines are not inferred.
// All tombstones/fences are retained indefinitely; cleanup requires a separate
// protocol proving that no old writer, backup, or import can reintroduce them.
type PersistentRefreshTokenStore struct {
	db *sql.DB
}

var _ service.RefreshTokenCache = (*PersistentRefreshTokenStore)(nil)
var _ service.RefreshTokenIssuancePreparer = (*PersistentRefreshTokenStore)(nil)

// NewPersistentRefreshTokenStore requires the writable PostgreSQL authority,
// with migration 238 applied. It does not activate production wiring or migrate
// existing sessions. A Redis miss must never be used as permission to import.
func NewPersistentRefreshTokenStore(db *sql.DB) *PersistentRefreshTokenStore {
	return &PersistentRefreshTokenStore{db: db}
}

// Lock order is global -> user -> family -> issuance -> hash. The shared lock permits
// independent users to mutate concurrently; global revocation takes it exclusive.
func (s *PersistentRefreshTokenStore) transaction(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return fmt.Errorf("begin refresh token transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	// Do not acknowledge a write with only asynchronous local WAL durability.
	if _, err = tx.ExecContext(ctx, `SET LOCAL synchronous_commit = on`); err != nil {
		return fmt.Errorf("set refresh token durability: %w", err)
	}
	if err = fn(tx); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit refresh token transaction: %w", err)
	}
	return nil
}

func persistentRefreshGlobalLock(ctx context.Context, tx *sql.Tx) (int64, error) {
	var generation int64
	err := tx.QueryRowContext(ctx, `SELECT generation FROM refresh_token_revocation_state
		WHERE singleton = TRUE FOR SHARE`).Scan(&generation)
	return generation, err
}

func persistentRefreshUserLock(ctx context.Context, tx *sql.Tx, userID int64) (int64, error) {
	if _, err := tx.ExecContext(ctx, `INSERT INTO refresh_token_users (user_id) VALUES ($1)
		ON CONFLICT DO NOTHING`, userID); err != nil {
		return 0, err
	}
	var generation int64
	err := tx.QueryRowContext(ctx, `SELECT generation FROM refresh_token_users
		WHERE user_id = $1 FOR UPDATE`, userID).Scan(&generation)
	return generation, err
}

// PrepareRefreshTokenIssuance must commit before the caller generates any
// credential. A failed/ambiguous commit returns no ticket. Redis-only callers
// remain unaffected because this is a separate optional service interface.
func (s *PersistentRefreshTokenStore) PrepareRefreshTokenIssuance(ctx context.Context, userID int64) (*service.RefreshTokenIssuance, error) {
	if userID <= 0 {
		return nil, ErrPersistentRefreshTokenMetadata
	}
	ticket := &service.RefreshTokenIssuance{UserID: userID}
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		var err error
		ticket.GlobalGeneration, err = persistentRefreshGlobalLock(ctx, tx)
		if err != nil {
			return err
		}
		ticket.UserGeneration, err = persistentRefreshUserLock(ctx, tx, userID)
		if err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, `INSERT INTO refresh_token_issuances (user_id, user_generation, global_generation)
			VALUES ($1, $2, $3) RETURNING ticket_id`, userID, ticket.UserGeneration, ticket.GlobalGeneration).Scan(&ticket.ID)
	})
	if err != nil {
		return nil, err
	}
	return ticket, nil
}

func persistentRefreshHex(value string, bytes int) bool {
	if len(value) != bytes*2 {
		return false
	}
	for _, c := range value {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func persistentRefreshBindingValid(value string) bool {
	// HTTP SessionBinding.Hash is 128-bit hex; retain the previously accepted
	// full SHA-256 representation without changing either stored identity.
	return value == "" || persistentRefreshHex(value, 16) || persistentRefreshHex(value, 32)
}

func (s *PersistentRefreshTokenStore) StoreRefreshToken(ctx context.Context, tokenHash string, data *service.RefreshTokenData, ttl time.Duration) error {
	if !persistentRefreshHex(tokenHash, 32) || data == nil || data.UserID <= 0 ||
		!persistentRefreshHex(data.FamilyID, 16) ||
		!persistentRefreshBindingValid(data.BindingHash) || ttl <= 0 ||
		data.CreatedAt.IsZero() || data.ExpiresAt.IsZero() || data.FamilyExpiresAt.IsZero() {
		return ErrPersistentRefreshTokenMetadata
	}
	if data.Issuance == nil || data.Issuance.UserID != data.UserID {
		return ErrPersistentRefreshTokenMetadata
	}
	if _, err := uuid.Parse(data.Issuance.ID); err != nil {
		return ErrPersistentRefreshTokenMetadata
	}
	// PostgreSQL timestamps have microsecond precision. Never round a deadline up
	// or mutate the caller's metadata. A delayed Store cannot restart its TTL.
	d := *data
	ticket := *data.Issuance
	d.CreatedAt = d.CreatedAt.UTC().Truncate(time.Microsecond)
	d.ExpiresAt = d.ExpiresAt.UTC().Truncate(time.Microsecond)
	d.FamilyExpiresAt = d.FamilyExpiresAt.UTC().Truncate(time.Microsecond)
	validUntil := d.CreatedAt.Add(ttl).Truncate(time.Microsecond)
	if d.ExpiresAt.Before(validUntil) {
		validUntil = d.ExpiresAt
	}
	if d.FamilyExpiresAt.Before(validUntil) {
		validUntil = d.FamilyExpiresAt
	}
	if !validUntil.After(d.CreatedAt) {
		return ErrPersistentRefreshTokenMetadata
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		globalGeneration, err := persistentRefreshGlobalLock(ctx, tx)
		if err != nil {
			return err
		}
		userGeneration, err := persistentRefreshUserLock(ctx, tx, d.UserID)
		if err != nil {
			return err
		}
		if ticket.GlobalGeneration != globalGeneration || ticket.UserGeneration != userGeneration {
			return fmt.Errorf("%w: stale issuance generation", ErrPersistentRefreshTokenRejected)
		}
		var now time.Time
		if err = tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
			return err
		}
		if !validUntil.After(now) {
			return fmt.Errorf("%w: expired credential", ErrPersistentRefreshTokenRejected)
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO refresh_token_families (family_id, user_id, family_expires_at)
			VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`, d.FamilyID, d.UserID, d.FamilyExpiresAt); err != nil {
			return err
		}
		var owner sql.NullInt64
		var deadline, revoked sql.NullTime
		if err = tx.QueryRowContext(ctx, `SELECT user_id, family_expires_at, revoked_at
			FROM refresh_token_families WHERE family_id = $1 FOR UPDATE`, d.FamilyID).
			Scan(&owner, &deadline, &revoked); err != nil {
			return err
		}
		if revoked.Valid || !owner.Valid || owner.Int64 != d.UserID || !deadline.Valid || !deadline.Time.Equal(d.FamilyExpiresAt) {
			return fmt.Errorf("%w: revoked or conflicting family", ErrPersistentRefreshTokenRejected)
		}
		var persisted service.RefreshTokenIssuance
		err = tx.QueryRowContext(ctx, `SELECT user_id, user_generation, global_generation FROM refresh_token_issuances
			WHERE ticket_id = $1 AND used_at IS NULL AND expires_at > clock_timestamp() FOR UPDATE`, ticket.ID).
			Scan(&persisted.UserID, &persisted.UserGeneration, &persisted.GlobalGeneration)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: missing, used, or expired issuance ticket", ErrPersistentRefreshTokenRejected)
		}
		if err != nil {
			return err
		}
		if persisted.UserID != d.UserID || persisted.UserGeneration != userGeneration || persisted.GlobalGeneration != globalGeneration {
			return fmt.Errorf("%w: conflicting issuance ticket", ErrPersistentRefreshTokenRejected)
		}
		result, err := tx.ExecContext(ctx, `INSERT INTO refresh_tokens
			(token_hash, user_id, family_id, token_version, binding_hash, created_at, expires_at, valid_until,
			issuance_id, user_generation, global_generation)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11) ON CONFLICT DO NOTHING`,
			tokenHash, d.UserID, d.FamilyID, d.TokenVersion, d.BindingHash, d.CreatedAt, d.ExpiresAt, validUntil,
			ticket.ID, userGeneration, globalGeneration)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return fmt.Errorf("%w: existing token hash", ErrPersistentRefreshTokenRejected)
		}
		result, err = tx.ExecContext(ctx, `UPDATE refresh_token_issuances SET used_at = clock_timestamp() WHERE ticket_id = $1`, ticket.ID)
		if err != nil {
			return err
		}
		n, err = result.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return fmt.Errorf("%w: issuance ticket was not consumed", ErrPersistentRefreshTokenRejected)
		}
		return nil
	})
}

const persistentRefreshActiveFrom = ` FROM refresh_tokens t
	JOIN refresh_token_families f ON f.family_id = t.family_id AND f.user_id = t.user_id
	JOIN refresh_token_users u ON u.user_id = t.user_id
	JOIN refresh_token_revocation_state g ON g.singleton = TRUE
	WHERE t.consumed_at IS NULL AND t.revoked_at IS NULL AND f.revoked_at IS NULL
	AND t.valid_until > clock_timestamp() AND t.expires_at > clock_timestamp()
	AND f.family_expires_at > clock_timestamp()
	AND t.user_generation = u.generation AND t.global_generation = g.generation`

const persistentRefreshDataSelect = `SELECT t.user_id, t.token_version, t.family_id,
	t.binding_hash, t.created_at, t.expires_at, f.family_expires_at` + persistentRefreshActiveFrom

func scanPersistentRefreshData(row *sql.Row) (*service.RefreshTokenData, error) {
	var d service.RefreshTokenData
	err := row.Scan(&d.UserID, &d.TokenVersion, &d.FamilyID, &d.BindingHash, &d.CreatedAt, &d.ExpiresAt, &d.FamilyExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrRefreshTokenNotFound
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}

func (s *PersistentRefreshTokenStore) GetRefreshToken(ctx context.Context, tokenHash string) (*service.RefreshTokenData, error) {
	if !persistentRefreshHex(tokenHash, 32) {
		return nil, ErrPersistentRefreshTokenMetadata
	}
	return scanPersistentRefreshData(s.db.QueryRowContext(ctx, persistentRefreshDataSelect+` AND t.token_hash = $1`, tokenHash))
}

func (s *PersistentRefreshTokenStore) ConsumeRefreshToken(ctx context.Context, tokenHash string) (*service.RefreshTokenData, error) {
	if !persistentRefreshHex(tokenHash, 32) {
		return nil, ErrPersistentRefreshTokenMetadata
	}
	var data *service.RefreshTokenData
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		if _, err := persistentRefreshGlobalLock(ctx, tx); err != nil {
			return err
		}
		// Locate immutable scope IDs first, then lock in the same order as Store.
		initial, err := scanPersistentRefreshData(tx.QueryRowContext(ctx, persistentRefreshDataSelect+` AND t.token_hash = $1`, tokenHash))
		if err != nil {
			return err
		}
		if _, err = persistentRefreshUserLock(ctx, tx, initial.UserID); err != nil {
			return err
		}
		var family string
		if err = tx.QueryRowContext(ctx, `SELECT family_id FROM refresh_token_families
			WHERE family_id = $1 FOR UPDATE`, initial.FamilyID).Scan(&family); err != nil {
			return err
		}
		data, err = scanPersistentRefreshData(tx.QueryRowContext(ctx, persistentRefreshDataSelect+`
			AND t.token_hash = $1 FOR UPDATE OF t`, tokenHash))
		if err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE refresh_tokens SET consumed_at = clock_timestamp() WHERE token_hash = $1`, tokenHash)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return service.ErrRefreshTokenNotFound
		}
		return nil
	})
	if err != nil {
		return nil, err // In particular, never return usable metadata on COMMIT failure.
	}
	return data, nil
}

func (s *PersistentRefreshTokenStore) DeleteRefreshToken(ctx context.Context, tokenHash string) error {
	if !persistentRefreshHex(tokenHash, 32) {
		return ErrPersistentRefreshTokenMetadata
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		if _, err := persistentRefreshGlobalLock(ctx, tx); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO refresh_tokens (token_hash, revoked_at)
			VALUES ($1, clock_timestamp()) ON CONFLICT (token_hash) DO UPDATE
			SET revoked_at = COALESCE(refresh_tokens.revoked_at, EXCLUDED.revoked_at)`, tokenHash)
		return err
	})
}

func (s *PersistentRefreshTokenStore) DeleteTokenFamily(ctx context.Context, familyID string) error {
	if !persistentRefreshHex(familyID, 16) {
		return ErrPersistentRefreshTokenMetadata
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		if _, err := persistentRefreshGlobalLock(ctx, tx); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO refresh_token_families (family_id, revoked_at)
			VALUES ($1, clock_timestamp()) ON CONFLICT (family_id) DO UPDATE
			SET revoked_at = COALESCE(refresh_token_families.revoked_at, EXCLUDED.revoked_at)`, familyID)
		return err
	})
}

func (s *PersistentRefreshTokenStore) DeleteUserRefreshTokens(ctx context.Context, userID int64) error {
	if userID <= 0 {
		return ErrPersistentRefreshTokenMetadata
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		if _, err := persistentRefreshGlobalLock(ctx, tx); err != nil {
			return err
		}
		if _, err := persistentRefreshUserLock(ctx, tx, userID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE refresh_token_users
			SET generation = generation + 1, revoked_at = clock_timestamp()
			WHERE user_id = $1`, userID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE refresh_token_families
			SET revoked_at = COALESCE(revoked_at, clock_timestamp()) WHERE user_id = $1`, userID)
		return err
	})
}

// DeleteAllRefreshTokens revokes every refresh session, not stateless access
// JWTs. It is an explicit repository API, deliberately absent from production
// wiring. New independent families prepared after revocation remain possible.
func (s *PersistentRefreshTokenStore) DeleteAllRefreshTokens(ctx context.Context) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		var generation int64
		if err := tx.QueryRowContext(ctx, `SELECT generation FROM refresh_token_revocation_state
			WHERE singleton = TRUE FOR UPDATE`).Scan(&generation); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE refresh_token_revocation_state
			SET generation = generation + 1, revoked_at = clock_timestamp()
			WHERE singleton = TRUE`); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE refresh_token_families SET revoked_at = COALESCE(revoked_at, clock_timestamp())`)
		return err
	})
}

// Compatibility hooks only verify the indexes already committed by Store.
// They never create membership, revive a tombstone, or extend any deadline.
func (s *PersistentRefreshTokenStore) AddToUserTokenSet(ctx context.Context, userID int64, tokenHash string, _ time.Duration) error {
	d, err := s.GetRefreshToken(ctx, tokenHash)
	if err != nil {
		return err
	}
	if d.UserID != userID {
		return ErrPersistentRefreshTokenMetadata
	}
	return nil
}

func (s *PersistentRefreshTokenStore) AddToFamilyTokenSet(ctx context.Context, familyID string, tokenHash string, _ time.Duration) error {
	d, err := s.GetRefreshToken(ctx, tokenHash)
	if err != nil {
		return err
	}
	if d.FamilyID != familyID {
		return ErrPersistentRefreshTokenMetadata
	}
	return nil
}

func (s *PersistentRefreshTokenStore) tokenHashes(ctx context.Context, predicate string, value any) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT t.token_hash`+persistentRefreshActiveFrom+predicate+` ORDER BY t.token_hash`, value)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	hashes := make([]string, 0)
	for rows.Next() {
		var hash string
		if err = rows.Scan(&hash); err != nil {
			return nil, err
		}
		hashes = append(hashes, hash)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return hashes, nil
}

func (s *PersistentRefreshTokenStore) GetUserTokenHashes(ctx context.Context, userID int64) ([]string, error) {
	if userID <= 0 {
		return nil, ErrPersistentRefreshTokenMetadata
	}
	return s.tokenHashes(ctx, ` AND t.user_id = $1`, userID)
}

func (s *PersistentRefreshTokenStore) GetFamilyTokenHashes(ctx context.Context, familyID string) ([]string, error) {
	if !persistentRefreshHex(familyID, 16) {
		return nil, ErrPersistentRefreshTokenMetadata
	}
	return s.tokenHashes(ctx, ` AND t.family_id = $1`, familyID)
}

func (s *PersistentRefreshTokenStore) IsTokenInFamily(ctx context.Context, familyID string, tokenHash string) (bool, error) {
	if !persistentRefreshHex(familyID, 16) || !persistentRefreshHex(tokenHash, 32) {
		return false, ErrPersistentRefreshTokenMetadata
	}
	var exists bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1`+persistentRefreshActiveFrom+
		` AND t.family_id = $1 AND t.token_hash = $2)`, familyID, tokenHash).Scan(&exists)
	if err != nil {
		return false, err
	}
	return exists, nil
}

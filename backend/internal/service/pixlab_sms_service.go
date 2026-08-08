package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
	"github.com/lib/pq"
)

const pixlabSMSAPIBaseURL = "https://sms.pixlab.cc/api"

const (
	// PixlabSMSMemberFee is the first-installation default. Runtime charges are
	// read from the administrator-managed database setting for every new claim.
	PixlabSMSMemberFee = 2.00
	// PixlabSMSMemberMutationDelay protects a newly issued number from being
	// churned immediately. It applies to member changes and cancellations only.
	PixlabSMSMemberMutationDelay = time.Minute
	// PixlabSMSCardKeyMaxClaims matches the provider's per-card authorization
	// limit. A card is rotated after every claim and is never claimed a sixth
	// time, even if its previous sessions were cancelled without a code.
	PixlabSMSCardKeyMaxClaims = 5
)

var (
	ErrPixlabSMSNoCardKey = infraerrors.NotFound("SMS_CARD_KEY_UNAVAILABLE", "暂无待用接码卡密")
	ErrPixlabSMSSession   = infraerrors.NotFound("SMS_SESSION_NOT_FOUND", "接码会话不存在或已结束")
	ErrPixlabSMSBalance   = infraerrors.BadRequest("SMS_BALANCE_INSUFFICIENT", "余额不足，无法领取授权接码号码")
	ErrPixlabSMSActive    = infraerrors.Conflict("SMS_ACTIVE_SESSION_EXISTS", "当前已有进行中的接码会话，请先完成、换号或取消")
	ErrPixlabSMSLocked    = infraerrors.Conflict("SMS_SESSION_ACTION_LOCKED", "领取号码后需等待 1 分钟才可换号或取消")
)

// PixlabSMSQueueStatus intentionally contains counters only. Plaintext card
// keys are available exclusively from the administrator-only management API.
type PixlabSMSQueueStatus struct {
	QueuedCount int64 `json:"queued_count"`
	ActiveCount int64 `json:"active_count"`
}

// PixlabSMSCardKey is returned only from the administrator's settings API.
// CardKey remains encrypted at rest and is decrypted only for that explicitly
// authenticated management view; member and OAuth endpoints never use it.
type PixlabSMSCardKey struct {
	ID            int64      `json:"id"`
	CardKey       string     `json:"card_key"`
	Status        string     `json:"status"`
	ClaimCount    int        `json:"claim_count"`
	QueueRank     int64      `json:"queue_rank"`
	LastClaimedAt *time.Time `json:"last_claimed_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

// PixlabSMSResult is the browser-safe representation of a provider session.
// SessionID is opaque and is the only locally persisted value used for polling.
type PixlabSMSResult struct {
	SessionID   string `json:"session_id,omitempty"`
	Status      string `json:"status"`
	Number      string `json:"number,omitempty"`
	Country     string `json:"country,omitempty"`
	Code        string `json:"code,omitempty"`
	QueuedCount int64  `json:"queued_count"`
}

// PixlabSMSMemberStatus intentionally omits global card-key queue totals so
// members cannot inspect operator inventory. The price and own balance are
// returned so the client can give an accurate pre-charge explanation.
type PixlabSMSMemberStatus struct {
	QueuedCount int64   `json:"queued_count"`
	ActiveCount int64   `json:"active_count"`
	Available   bool    `json:"available"`
	FeeAmount   float64 `json:"fee_amount"`
	Balance     float64 `json:"balance"`
}

// PixlabSMSMemberResult is the member-safe session response. It contains no
// card key or global queue information and reports how this session's charge
// is currently accounted for: held, captured, or released.
type PixlabSMSMemberResult struct {
	SessionID         string     `json:"session_id,omitempty"`
	Status            string     `json:"status"`
	Number            string     `json:"number,omitempty"`
	Country           string     `json:"country,omitempty"`
	Code              string     `json:"code,omitempty"`
	QueuedCount       int64      `json:"queued_count"`
	FeeAmount         float64    `json:"fee_amount"`
	ChargeState       string     `json:"charge_state"`
	Balance           float64    `json:"balance"`
	ActionAvailableAt *time.Time `json:"action_available_at,omitempty"`
}

// PixlabSMSService owns server-side card-key persistence and provider proxying.
// Card keys remain encrypted at rest and are only decrypted for the explicit
// administrator-only queue-management API.
type PixlabSMSService struct {
	db           *sql.DB
	encryptor    SecretEncryptor
	billingCache *BillingCacheService
	client       *http.Client
	baseURL      string
}

func NewPixlabSMSService(db *sql.DB, encryptor SecretEncryptor, billingCache *BillingCacheService) *PixlabSMSService {
	svc := newPixlabSMSService(db, encryptor, &http.Client{Timeout: 20 * time.Second}, pixlabSMSAPIBaseURL)
	svc.billingCache = billingCache
	return svc
}

func newPixlabSMSService(db *sql.DB, encryptor SecretEncryptor, client *http.Client, baseURL string) *PixlabSMSService {
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	return &PixlabSMSService{
		db:        db,
		encryptor: encryptor,
		client:    client,
		baseURL:   strings.TrimRight(baseURL, "/"),
	}
}

func normalizePixlabCardKeys(raw string) []string {
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		switch r {
		case ',', '，', ';', '；', '\n', '\r', '\t', ' ':
			return true
		default:
			return false
		}
	})
	keys := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		key := strings.TrimSpace(part)
		if key == "" || len(key) > 2048 {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	return keys
}

func pixlabCardKeyFingerprint(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// AddCardKeys stores new keys encrypted at rest. Existing fingerprints are
// ignored, so accidentally pasting the same batch does not duplicate charges.
func (s *PixlabSMSService) AddCardKeys(ctx context.Context, raw string) (int, *PixlabSMSQueueStatus, error) {
	if s == nil || s.db == nil || s.encryptor == nil {
		return 0, nil, infraerrors.InternalServer("SMS_RECEIVER_UNAVAILABLE", "接码服务尚未初始化")
	}
	keys := normalizePixlabCardKeys(raw)
	if len(keys) == 0 {
		return 0, nil, infraerrors.BadRequest("SMS_CARD_KEY_REQUIRED", "请至少输入一个有效接码卡密")
	}
	if len(keys) > 500 {
		return 0, nil, infraerrors.BadRequest("SMS_CARD_KEY_LIMIT", "单次最多添加 500 个接码卡密")
	}

	inserted := 0
	for _, key := range keys {
		ciphertext, err := s.encryptor.Encrypt(key)
		if err != nil {
			return 0, nil, fmt.Errorf("encrypt sms card key: %w", err)
		}
		var rowID int64
		err = s.db.QueryRowContext(ctx, `
			INSERT INTO xiass_sms_card_keys (encrypted_key, key_fingerprint, queue_rank)
			VALUES (
				$1,
				$2,
				(SELECT COALESCE(MAX(queue_rank), 0) + 1 FROM xiass_sms_card_keys)
			)
			ON CONFLICT (key_fingerprint) DO NOTHING
			RETURNING id`, ciphertext, pixlabCardKeyFingerprint(key)).Scan(&rowID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return 0, nil, fmt.Errorf("store sms card key: %w", err)
		}
		inserted++
	}
	status, err := s.QueueStatus(ctx, 0)
	if err != nil {
		return 0, nil, err
	}
	return inserted, status, nil
}

func (s *PixlabSMSService) QueueStatus(ctx context.Context, ownerUserID int64) (*PixlabSMSQueueStatus, error) {
	if s == nil || s.db == nil {
		return nil, infraerrors.InternalServer("SMS_RECEIVER_UNAVAILABLE", "接码服务尚未初始化")
	}
	status := &PixlabSMSQueueStatus{}
	err := s.db.QueryRowContext(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE status = 'queued'),
			COUNT(*) FILTER (WHERE status = 'active' AND ($1 = 0 OR owner_user_id = $1))
		FROM xiass_sms_card_keys`, ownerUserID).Scan(&status.QueuedCount, &status.ActiveCount)
	if err != nil {
		return nil, fmt.Errorf("count sms card keys: %w", err)
	}
	return status, nil
}

// ClearQueuedCardKeys does not touch an in-progress SMS number. Claimed cards
// remain usable until a code is received, changed, or explicitly cancelled.
func (s *PixlabSMSService) ClearQueuedCardKeys(ctx context.Context) (int64, *PixlabSMSQueueStatus, error) {
	if s == nil || s.db == nil {
		return 0, nil, infraerrors.InternalServer("SMS_RECEIVER_UNAVAILABLE", "接码服务尚未初始化")
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM xiass_sms_card_keys WHERE status = 'queued'`)
	if err != nil {
		return 0, nil, fmt.Errorf("clear queued sms card keys: %w", err)
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return 0, nil, fmt.Errorf("read cleared sms card keys: %w", err)
	}
	status, err := s.QueueStatus(ctx, 0)
	if err != nil {
		return 0, nil, err
	}
	return deleted, status, nil
}

// ListCardKeys returns plaintext card keys only for the administrator's
// management screen. Keys remain encrypted in PostgreSQL at all times.
func (s *PixlabSMSService) ListCardKeys(ctx context.Context) ([]PixlabSMSCardKey, *PixlabSMSQueueStatus, error) {
	if s == nil || s.db == nil || s.encryptor == nil {
		return nil, nil, infraerrors.InternalServer("SMS_RECEIVER_UNAVAILABLE", "接码服务尚未初始化")
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, encrypted_key, status, claim_count, queue_rank, last_claimed_at, created_at
		FROM xiass_sms_card_keys
		ORDER BY
			CASE status WHEN 'queued' THEN 0 WHEN 'active' THEN 1 ELSE 2 END,
			claim_count ASC,
			queue_rank ASC,
			last_claimed_at NULLS FIRST,
			id ASC`)
	if err != nil {
		return nil, nil, fmt.Errorf("list sms card keys: %w", err)
	}
	defer func() { _ = rows.Close() }()

	keys := make([]PixlabSMSCardKey, 0)
	for rows.Next() {
		var (
			entry       PixlabSMSCardKey
			encrypted   string
			lastClaimed sql.NullTime
		)
		if err := rows.Scan(
			&entry.ID,
			&encrypted,
			&entry.Status,
			&entry.ClaimCount,
			&entry.QueueRank,
			&lastClaimed,
			&entry.CreatedAt,
		); err != nil {
			return nil, nil, fmt.Errorf("scan sms card key: %w", err)
		}
		plainKey, err := s.encryptor.Decrypt(encrypted)
		if err != nil {
			return nil, nil, fmt.Errorf("decrypt sms card key %d: %w", entry.ID, err)
		}
		entry.CardKey = plainKey
		if lastClaimed.Valid {
			claimedAt := lastClaimed.Time
			entry.LastClaimedAt = &claimedAt
		}
		keys = append(keys, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate sms card keys: %w", err)
	}
	status, err := s.QueueStatus(ctx, 0)
	if err != nil {
		return nil, nil, err
	}
	return keys, status, nil
}

// DeleteCardKey permanently removes one operator-selected card. When an active
// member session still has a held charge, it releases that balance in the same
// transaction before removing the card. This lets an operator remove a broken
// active credential without leaving the member's balance in a held state.
func (s *PixlabSMSService) DeleteCardKey(ctx context.Context, cardKeyID int64) (*PixlabSMSQueueStatus, error) {
	if s == nil || s.db == nil {
		return nil, infraerrors.InternalServer("SMS_RECEIVER_UNAVAILABLE", "接码服务尚未初始化")
	}
	if cardKeyID <= 0 {
		return nil, infraerrors.BadRequest("SMS_CARD_KEY_ID_INVALID", "卡密编号无效")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin delete sms card key: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var (
		status      string
		ownerUserID sql.NullInt64
		sessionID   sql.NullString
	)
	err = tx.QueryRowContext(ctx, `
		SELECT status, owner_user_id, session_id
		FROM xiass_sms_card_keys
		WHERE id = $1
		FOR UPDATE`, cardKeyID).Scan(&status, &ownerUserID, &sessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, infraerrors.NotFound("SMS_CARD_KEY_NOT_FOUND", "接码卡密不存在或已被删除")
	}
	if err != nil {
		return nil, fmt.Errorf("lock sms card key for deletion: %w", err)
	}

	refundedUserID := int64(0)
	if status == "active" && ownerUserID.Valid && sessionID.Valid && strings.TrimSpace(sessionID.String) != "" {
		var amount float64
		err = tx.QueryRowContext(ctx, `
			SELECT amount
			FROM xiass_sms_member_charges
			WHERE session_id = $1 AND user_id = $2 AND status = 'held'
			FOR UPDATE`, sessionID.String, ownerUserID.Int64).Scan(&amount)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			// Administrator sessions have no member charge. A captured member
			// charge also remains final and is never refunded here.
		case err != nil:
			return nil, fmt.Errorf("lock member sms charge for card deletion: %w", err)
		default:
			if _, err := tx.ExecContext(ctx, `
				UPDATE users
				SET balance = balance + $1, updated_at = NOW()
				WHERE id = $2 AND deleted_at IS NULL`, amount, ownerUserID.Int64); err != nil {
				return nil, fmt.Errorf("refund member balance for card deletion: %w", err)
			}
			if _, err := tx.ExecContext(ctx, `
				UPDATE xiass_sms_member_charges
				SET status = 'released', released_at = NOW(), updated_at = NOW()
				WHERE session_id = $1 AND user_id = $2 AND status = 'held'`, sessionID.String, ownerUserID.Int64); err != nil {
				return nil, fmt.Errorf("release member sms charge for card deletion: %w", err)
			}
			refundedUserID = ownerUserID.Int64
		}
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM xiass_sms_card_keys WHERE id = $1`, cardKeyID); err != nil {
		return nil, fmt.Errorf("delete sms card key: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit delete sms card key: %w", err)
	}
	if refundedUserID > 0 {
		s.invalidateBalance(ctx, refundedUserID)
	}
	return s.QueueStatus(ctx, 0)
}

// ReorderCardKeys updates only queued cards. The scheduler still distributes
// claims by usage count first, then uses this operator-defined rank to break
// ties between equally used cards.
func (s *PixlabSMSService) ReorderCardKeys(ctx context.Context, cardKeyIDs []int64) (*PixlabSMSQueueStatus, error) {
	if s == nil || s.db == nil {
		return nil, infraerrors.InternalServer("SMS_RECEIVER_UNAVAILABLE", "接码服务尚未初始化")
	}
	if len(cardKeyIDs) == 0 || len(cardKeyIDs) > 500 {
		return nil, infraerrors.BadRequest("SMS_CARD_KEY_ORDER_INVALID", "请提交 1 到 500 个待用卡密编号")
	}
	seen := make(map[int64]struct{}, len(cardKeyIDs))
	for _, cardKeyID := range cardKeyIDs {
		if cardKeyID <= 0 {
			return nil, infraerrors.BadRequest("SMS_CARD_KEY_ID_INVALID", "卡密编号无效")
		}
		if _, exists := seen[cardKeyID]; exists {
			return nil, infraerrors.BadRequest("SMS_CARD_KEY_ORDER_INVALID", "排序中包含重复卡密")
		}
		seen[cardKeyID] = struct{}{}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin sms card key reorder: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for index, cardKeyID := range cardKeyIDs {
		result, err := tx.ExecContext(ctx, `
			UPDATE xiass_sms_card_keys
			SET queue_rank = $1, updated_at = NOW()
			WHERE id = $2 AND status = 'queued'`, int64(index+1), cardKeyID)
		if err != nil {
			return nil, fmt.Errorf("reorder sms card key: %w", err)
		}
		updated, err := result.RowsAffected()
		if err != nil {
			return nil, fmt.Errorf("read reordered sms card key count: %w", err)
		}
		if updated == 0 {
			return nil, infraerrors.Conflict("SMS_CARD_KEY_REORDER_CONFLICT", "待用卡密状态已变化，请刷新后重试")
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit sms card key reorder: %w", err)
	}
	return s.QueueStatus(ctx, 0)
}

// MemberFee returns the current administrator-configured member price. Each
// member claim records this value on its own charge row, so later price edits
// never alter an in-progress session's reserve, capture, or refund amount.
func (s *PixlabSMSService) MemberFee(ctx context.Context) (float64, error) {
	if s == nil || s.db == nil {
		return 0, infraerrors.InternalServer("SMS_RECEIVER_UNAVAILABLE", "接码服务尚未初始化")
	}
	var fee float64
	if err := s.db.QueryRowContext(ctx, `
		SELECT member_fee
		FROM xiass_sms_receiver_settings
		WHERE id = 1`).Scan(&fee); err != nil {
		return 0, fmt.Errorf("load sms member fee: %w", err)
	}
	if fee <= 0 || fee > 10000 {
		return 0, infraerrors.InternalServer("SMS_MEMBER_FEE_INVALID", "接码会员价格配置无效")
	}
	return fee, nil
}

// UpdateMemberFee changes the price used by future member claims. Existing
// charge rows retain their originally reserved amount and are never repriced.
func (s *PixlabSMSService) UpdateMemberFee(ctx context.Context, fee float64) (float64, error) {
	if s == nil || s.db == nil {
		return 0, infraerrors.InternalServer("SMS_RECEIVER_UNAVAILABLE", "接码服务尚未初始化")
	}
	if fee <= 0 || fee > 10000 {
		return 0, infraerrors.BadRequest("SMS_MEMBER_FEE_INVALID", "会员接码价格必须在 ¥0.01 到 ¥10000.00 之间")
	}
	fee = float64(int64(fee*100+0.5)) / 100
	if _, err := s.db.ExecContext(ctx, `
		UPDATE xiass_sms_receiver_settings
		SET member_fee = $1, updated_at = NOW()
		WHERE id = 1`, fee); err != nil {
		return 0, fmt.Errorf("update sms member fee: %w", err)
	}
	return fee, nil
}

// MemberStatus exposes only a member's own active-session count and balance.
// Global card-key totals are operator-only data.
func (s *PixlabSMSService) MemberStatus(ctx context.Context, userID int64) (*PixlabSMSMemberStatus, error) {
	if s == nil || s.db == nil {
		return nil, infraerrors.InternalServer("SMS_RECEIVER_UNAVAILABLE", "接码服务尚未初始化")
	}
	if userID <= 0 {
		return nil, infraerrors.Unauthorized("SMS_OWNER_REQUIRED", "无法识别当前用户")
	}

	fee, err := s.MemberFee(ctx)
	if err != nil {
		return nil, err
	}
	status := &PixlabSMSMemberStatus{FeeAmount: fee}
	if err := s.db.QueryRowContext(ctx, `
		SELECT balance
		FROM users
		WHERE id = $1 AND deleted_at IS NULL`, userID).Scan(&status.Balance); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, infraerrors.NotFound("USER_NOT_FOUND", "用户不存在")
		}
		return nil, fmt.Errorf("load sms member balance: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM xiass_sms_card_keys
		WHERE owner_user_id = $1 AND status = 'active'`, userID).Scan(&status.ActiveCount); err != nil {
		return nil, fmt.Errorf("count member sms sessions: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM xiass_sms_card_keys
			WHERE status = 'queued' AND claim_count < $1
		)`, PixlabSMSCardKeyMaxClaims).Scan(&status.Available); err != nil {
		return nil, fmt.Errorf("check sms card key availability: %w", err)
	}
	return status, nil
}

// Redeem atomically claims the next queued card before sending it to Pixlab.
// A card is consumed only after the provider returns a verification code. If
// this initial request cannot get a number, release it for a later retry.
func (s *PixlabSMSService) Redeem(ctx context.Context, ownerUserID int64) (*PixlabSMSResult, error) {
	if ownerUserID <= 0 {
		return nil, infraerrors.Unauthorized("SMS_OWNER_REQUIRED", "无法识别当前管理员")
	}
	for {
		sessionID := uuid.NewString()
		var encryptedKey string
		err := s.db.QueryRowContext(ctx, `
			WITH next_key AS (
				SELECT id
				FROM xiass_sms_card_keys
				WHERE status = 'queued' AND claim_count < $3
				ORDER BY claim_count ASC, queue_rank ASC, last_claimed_at NULLS FIRST, id
				FOR UPDATE SKIP LOCKED
				LIMIT 1
			)
			UPDATE xiass_sms_card_keys AS card
			SET status = 'active', owner_user_id = $1, session_id = $2,
				consumed_at = NOW(), last_claimed_at = NOW(),
				claim_count = card.claim_count + 1, updated_at = NOW()
			FROM next_key
			WHERE card.id = next_key.id
			RETURNING card.encrypted_key`, ownerUserID, sessionID, PixlabSMSCardKeyMaxClaims).Scan(&encryptedKey)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrPixlabSMSNoCardKey
		}
		if err != nil {
			// The database keeps one live session per user as a final guard
			// against concurrent browser tabs. OAuth can be opened from a new
			// tab after the browser-local session ID has been lost, so recover
			// the existing session instead of surfacing a misleading outage.
			if isPixlabActiveOwnerConflict(err) {
				existingSessionID, lookupErr := s.activeSessionID(ctx, ownerUserID)
				if lookupErr != nil {
					return nil, lookupErr
				}
				if existingSessionID != "" {
					result, resumeErr := s.Resume(ctx, ownerUserID, existingSessionID)
					if resumeErr == nil {
						return result, nil
					}
					// A Pixlab HTTP rejection is distinct from a network outage. The
					// former means this provider-side session cannot be resumed, so
					// release the stale local claim and continue the normal card
					// rotation. Connection failures remain retryable and keep the
					// current session intact.
					if isPixlabProviderHTTPError(resumeErr) {
						if releaseErr := s.releaseSession(ctx, existingSessionID, ownerUserID); releaseErr != nil {
							return nil, releaseErr
						}
						continue
					}
					return nil, resumeErr
				}
			}
			return nil, fmt.Errorf("claim sms card key: %w", err)
		}

		key, err := s.encryptor.Decrypt(encryptedKey)
		if err != nil {
			_ = s.releaseSession(ctx, sessionID, ownerUserID)
			return nil, fmt.Errorf("decrypt claimed sms card key: %w", err)
		}
		provider, err := s.callProvider(ctx, "redeem", key)
		if err != nil {
			if isPixlabCardUsageLimitError(err) {
				if markErr := s.exhaustSession(ctx, sessionID, ownerUserID); markErr != nil {
					return nil, markErr
				}
				continue
			}
			_ = s.releaseSession(ctx, sessionID, ownerUserID)
			return nil, err
		}
		return s.finishProviderResponse(ctx, sessionID, ownerUserID, provider, false)
	}
}

func (s *PixlabSMSService) activeSessionID(ctx context.Context, ownerUserID int64) (string, error) {
	var sessionID string
	err := s.db.QueryRowContext(ctx, `
		SELECT session_id
		FROM xiass_sms_card_keys
		WHERE owner_user_id = $1 AND status = 'active'
		ORDER BY consumed_at DESC NULLS LAST, id DESC
		LIMIT 1`, ownerUserID).Scan(&sessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("load active sms session: %w", err)
	}
	return strings.TrimSpace(sessionID), nil
}

func isPixlabActiveOwnerConflict(err error) bool {
	var pqErr *pq.Error
	return errors.As(err, &pqErr) &&
		pqErr.Code == "23505" &&
		pqErr.Constraint == "uq_xiass_sms_card_keys_active_owner"
}

func isPixlabProviderHTTPError(err error) bool {
	return infraerrors.Reason(err) == "SMS_PROVIDER_HTTP_ERROR"
}

// RedeemForMember reserves ¥2.00 and claims one card in the same database
// transaction. The money remains reversible until a real verification code is
// reported by the provider; that final event captures the charge permanently.
func (s *PixlabSMSService) RedeemForMember(ctx context.Context, userID int64) (*PixlabSMSMemberResult, error) {
	if userID <= 0 {
		return nil, infraerrors.Unauthorized("SMS_OWNER_REQUIRED", "无法识别当前用户")
	}

	for {
		sessionID, encryptedKey, err := s.claimMemberSession(ctx, userID)
		if err != nil {
			return nil, err
		}
		key, err := s.encryptor.Decrypt(encryptedKey)
		if err != nil {
			_ = s.releaseSession(ctx, sessionID, userID)
			_ = s.releaseMemberCharge(ctx, sessionID, userID)
			return nil, fmt.Errorf("decrypt claimed member sms card key: %w", err)
		}
		provider, err := s.callProvider(ctx, "redeem", key)
		if err != nil {
			if isPixlabCardUsageLimitError(err) {
				if markErr := s.exhaustSession(ctx, sessionID, userID); markErr != nil {
					return nil, markErr
				}
				if releaseErr := s.releaseMemberCharge(ctx, sessionID, userID); releaseErr != nil {
					return nil, releaseErr
				}
				continue
			}
			_ = s.releaseSession(ctx, sessionID, userID)
			_ = s.releaseMemberCharge(ctx, sessionID, userID)
			return nil, err
		}
		result, err := s.finishProviderResponse(ctx, sessionID, userID, provider, true)
		if err != nil {
			return nil, err
		}
		return s.memberResult(ctx, userID, sessionID, result)
	}
}

func (s *PixlabSMSService) ResumeForMember(ctx context.Context, userID int64, sessionID string) (*PixlabSMSMemberResult, error) {
	result, err := s.withActiveSession(ctx, userID, sessionID, "resume", true)
	if err != nil {
		return nil, err
	}
	return s.memberResult(ctx, userID, sessionID, result)
}

func (s *PixlabSMSService) CheckForMember(ctx context.Context, userID int64, sessionID string) (*PixlabSMSMemberResult, error) {
	result, err := s.withActiveSession(ctx, userID, sessionID, "check", true)
	if err != nil {
		return nil, err
	}
	return s.memberResult(ctx, userID, sessionID, result)
}

func (s *PixlabSMSService) ChangeNumberForMember(ctx context.Context, userID int64, sessionID string) (*PixlabSMSMemberResult, error) {
	if err := s.requireMemberMutationDelay(ctx, userID, sessionID); err != nil {
		return nil, err
	}
	// Check before releasing the number. A code that arrives between browser
	// polls is captured and returned instead of being lost to a change request.
	current, err := s.withActiveSession(ctx, userID, sessionID, "check", true)
	if err != nil {
		return nil, err
	}
	if current.SessionID == "" {
		if current.Status == "EXHAUSTED" {
			return s.RedeemForMember(ctx, userID)
		}
		return s.memberResult(ctx, userID, sessionID, current)
	}
	if released, err := s.cancel(ctx, userID, sessionID, true); err != nil {
		return nil, err
	} else if released.Status == "RECEIVED" {
		return s.memberResult(ctx, userID, sessionID, released)
	}
	return s.RedeemForMember(ctx, userID)
}

func (s *PixlabSMSService) CancelForMember(ctx context.Context, userID int64, sessionID string) (*PixlabSMSMemberResult, error) {
	if err := s.requireMemberMutationDelay(ctx, userID, sessionID); err != nil {
		return nil, err
	}
	// A pending provider-side code must win over a cancellation. This prevents
	// a late delivery from being discarded when the member clicks Cancel.
	current, err := s.withActiveSession(ctx, userID, sessionID, "check", true)
	if err != nil {
		return nil, err
	}
	if current.SessionID == "" {
		return s.memberResult(ctx, userID, sessionID, current)
	}
	result, err := s.cancel(ctx, userID, sessionID, true)
	if err != nil {
		return nil, err
	}
	return s.memberResult(ctx, userID, sessionID, result)
}

func (s *PixlabSMSService) Resume(ctx context.Context, ownerUserID int64, sessionID string) (*PixlabSMSResult, error) {
	return s.withActiveSession(ctx, ownerUserID, sessionID, "resume", false)
}

func (s *PixlabSMSService) Check(ctx context.Context, ownerUserID int64, sessionID string) (*PixlabSMSResult, error) {
	return s.withActiveSession(ctx, ownerUserID, sessionID, "check", false)
}

func (s *PixlabSMSService) Cancel(ctx context.Context, ownerUserID int64, sessionID string) (*PixlabSMSResult, error) {
	return s.cancel(ctx, ownerUserID, sessionID, false)
}

func (s *PixlabSMSService) cancel(ctx context.Context, ownerUserID int64, sessionID string, settleMemberFee bool) (*PixlabSMSResult, error) {
	result, err := s.withActiveSession(ctx, ownerUserID, sessionID, "cancel", settleMemberFee)
	if err != nil {
		return nil, err
	}
	// A received code always wins over cancellation. The provider may deliver
	// it just as the member clicks the button; preserve it for the caller and
	// do not overwrite it with a cancelled result.
	if pixlabHasVerificationCode(result.Code) {
		result.SessionID = ""
		result.Status = "RECEIVED"
		return result, nil
	}
	// Cancellation does not invalidate a card key. A terminal response is
	// released by finishProviderResponse; a non-terminal response needs an
	// explicit release before the next redemption.
	if !pixlabHasVerificationCode(result.Code) && pixlabTerminalStatus(result.Status, "") == "" {
		if settleMemberFee {
			if err := s.releaseMemberCharge(ctx, sessionID, ownerUserID); err != nil {
				return nil, err
			}
		}
		if err := s.releaseSession(ctx, sessionID, ownerUserID); err != nil {
			return nil, err
		}
		queue, err := s.QueueStatus(ctx, ownerUserID)
		if err != nil {
			return nil, err
		}
		result.QueuedCount = queue.QueuedCount
	}
	result.SessionID = ""
	result.Status = "CANCELLED"
	result.Code = ""
	return result, nil
}

func (s *PixlabSMSService) requireMemberMutationDelay(ctx context.Context, userID int64, sessionID string) error {
	if s == nil || s.db == nil {
		return infraerrors.InternalServer("SMS_RECEIVER_UNAVAILABLE", "接码服务尚未初始化")
	}
	sessionID = strings.TrimSpace(sessionID)
	if userID <= 0 || sessionID == "" || len(sessionID) > 64 {
		return ErrPixlabSMSSession
	}
	var claimedAt time.Time
	err := s.db.QueryRowContext(ctx, `
		SELECT consumed_at
		FROM xiass_sms_card_keys
		WHERE session_id = $1 AND owner_user_id = $2 AND status = 'active'`, sessionID, userID).Scan(&claimedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrPixlabSMSSession
	}
	if err != nil {
		return fmt.Errorf("load member sms claim time: %w", err)
	}
	if time.Now().Before(claimedAt.Add(PixlabSMSMemberMutationDelay)) {
		return ErrPixlabSMSLocked
	}
	return nil
}

// ChangeNumber cancels and releases the active number, then redeems again.
// The released card remains in the rotation and the least-recently-used card
// is selected next unless the current card has reached its five-claim limit.
func (s *PixlabSMSService) ChangeNumber(ctx context.Context, ownerUserID int64, sessionID string) (*PixlabSMSResult, error) {
	if _, err := s.Cancel(ctx, ownerUserID, sessionID); err != nil {
		return nil, err
	}
	return s.Redeem(ctx, ownerUserID)
}

func (s *PixlabSMSService) withActiveSession(ctx context.Context, ownerUserID int64, sessionID, action string, settleMemberFee bool) (*PixlabSMSResult, error) {
	if ownerUserID <= 0 {
		return nil, infraerrors.Unauthorized("SMS_OWNER_REQUIRED", "无法识别当前管理员")
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || len(sessionID) > 64 {
		return nil, ErrPixlabSMSSession
	}
	var encryptedKey string
	err := s.db.QueryRowContext(ctx, `
		SELECT encrypted_key
		FROM xiass_sms_card_keys
		WHERE session_id = $1 AND owner_user_id = $2 AND status = 'active'`, sessionID, ownerUserID).Scan(&encryptedKey)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrPixlabSMSSession
	}
	if err != nil {
		return nil, fmt.Errorf("load sms session: %w", err)
	}
	key, err := s.encryptor.Decrypt(encryptedKey)
	if err != nil {
		return nil, fmt.Errorf("decrypt sms session: %w", err)
	}
	provider, err := s.callProvider(ctx, action, key)
	if err != nil {
		if isPixlabCardUsageLimitError(err) {
			if settleMemberFee {
				if releaseErr := s.releaseMemberCharge(ctx, sessionID, ownerUserID); releaseErr != nil {
					return nil, releaseErr
				}
			}
			if exhaustErr := s.exhaustSession(ctx, sessionID, ownerUserID); exhaustErr != nil {
				return nil, exhaustErr
			}
			queue, queueErr := s.QueueStatus(ctx, ownerUserID)
			if queueErr != nil {
				return nil, queueErr
			}
			return &PixlabSMSResult{Status: "EXHAUSTED", QueuedCount: queue.QueuedCount}, nil
		}
		// A transient provider failure must not invalidate a card key. Keep the
		// session active so the operator can refresh or cancel it later.
		return nil, err
	}
	return s.finishProviderResponse(ctx, sessionID, ownerUserID, provider, settleMemberFee)
}

func (s *PixlabSMSService) finishProviderResponse(ctx context.Context, sessionID string, ownerUserID int64, provider *pixlabSMSProviderResponse, settleMemberFee bool) (*PixlabSMSResult, error) {
	if provider == nil {
		return nil, infraerrors.ServiceUnavailable("SMS_PROVIDER_INVALID_RESPONSE", "接码服务返回了无效数据")
	}
	responseSessionID := sessionID
	if pixlabHasVerificationCode(provider.Code) {
		if settleMemberFee {
			if err := s.captureMemberCharge(ctx, sessionID, ownerUserID); err != nil {
				return nil, err
			}
		}
		if err := s.finishSession(ctx, sessionID, ownerUserID); err != nil {
			return nil, err
		}
		responseSessionID = ""
	} else if pixlabTerminalStatus(provider.Status, "") != "" {
		// Expiry, cancellation, and provider-side failures return a card to
		// rotation until it has used all five permitted claims.
		if settleMemberFee {
			if err := s.releaseMemberCharge(ctx, sessionID, ownerUserID); err != nil {
				return nil, err
			}
		}
		if err := s.releaseSession(ctx, sessionID, ownerUserID); err != nil {
			return nil, err
		}
		responseSessionID = ""
	}
	queue, err := s.QueueStatus(ctx, ownerUserID)
	if err != nil {
		return nil, err
	}
	return &PixlabSMSResult{
		SessionID:   responseSessionID,
		Status:      provider.Status,
		Number:      provider.Number,
		Country:     provider.Country,
		Code:        provider.Code,
		QueuedCount: queue.QueuedCount,
	}, nil
}

func (s *PixlabSMSService) finishSession(ctx context.Context, sessionID string, ownerUserID int64) error {
	// A returned verification code is the only event that makes a card invalid.
	// Erase the encrypted key instead of retaining an unusable credential.
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM xiass_sms_card_keys
		WHERE session_id = $1 AND owner_user_id = $2 AND status = 'active'`, sessionID, ownerUserID)
	if err != nil {
		return fmt.Errorf("finish sms session: %w", err)
	}
	return nil
}

// releaseSession returns a card that has not received a verification code to
// the rotation queue. The fifth claim permanently removes it from future
// claims, matching the provider's five-use card limit.
func (s *PixlabSMSService) releaseSession(ctx context.Context, sessionID string, ownerUserID int64) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE xiass_sms_card_keys
		SET status = CASE WHEN claim_count >= $3 THEN 'exhausted' ELSE 'queued' END,
			owner_user_id = NULL, session_id = NULL,
			consumed_at = NULL, updated_at = NOW()
		WHERE session_id = $1 AND owner_user_id = $2 AND status = 'active'`, sessionID, ownerUserID, PixlabSMSCardKeyMaxClaims)
	if err != nil {
		return fmt.Errorf("release sms session: %w", err)
	}
	return nil
}

// exhaustSession permanently removes a card from rotation after Pixlab
// explicitly confirms that it has reached its per-card use limit.
func (s *PixlabSMSService) exhaustSession(ctx context.Context, sessionID string, ownerUserID int64) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE xiass_sms_card_keys
		SET status = 'exhausted', owner_user_id = NULL, session_id = NULL,
			consumed_at = NULL, updated_at = NOW()
		WHERE session_id = $1 AND owner_user_id = $2 AND status = 'active'`, sessionID, ownerUserID)
	if err != nil {
		return fmt.Errorf("exhaust sms card key: %w", err)
	}
	return nil
}

func (s *PixlabSMSService) claimMemberSession(ctx context.Context, userID int64) (string, string, error) {
	if s == nil || s.db == nil || s.encryptor == nil {
		return "", "", infraerrors.InternalServer("SMS_RECEIVER_UNAVAILABLE", "接码服务尚未初始化")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", "", fmt.Errorf("begin member sms claim: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Serialize claims per member. This prevents two browser tabs from each
	// reserving a card and balance before either request has committed.
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, userID); err != nil {
		return "", "", fmt.Errorf("lock member sms claim: %w", err)
	}
	var active bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM xiass_sms_card_keys
			WHERE owner_user_id = $1 AND status = 'active'
		)`, userID).Scan(&active); err != nil {
		return "", "", fmt.Errorf("check member active sms session: %w", err)
	}
	if active {
		return "", "", ErrPixlabSMSActive
	}
	var fee float64
	if err := tx.QueryRowContext(ctx, `
		SELECT member_fee
		FROM xiass_sms_receiver_settings
		WHERE id = 1
		FOR UPDATE`).Scan(&fee); err != nil {
		return "", "", fmt.Errorf("load member sms fee: %w", err)
	}
	if fee <= 0 || fee > 10000 {
		return "", "", infraerrors.InternalServer("SMS_MEMBER_FEE_INVALID", "接码会员价格配置无效")
	}

	sessionID := uuid.NewString()
	var encryptedKey string
	err = tx.QueryRowContext(ctx, `
		WITH next_key AS (
			SELECT id
			FROM xiass_sms_card_keys
			WHERE status = 'queued' AND claim_count < $3
			ORDER BY claim_count ASC, queue_rank ASC, last_claimed_at NULLS FIRST, id
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		UPDATE xiass_sms_card_keys AS card
		SET status = 'active', owner_user_id = $1, session_id = $2,
			consumed_at = NOW(), last_claimed_at = NOW(),
			claim_count = card.claim_count + 1, updated_at = NOW()
		FROM next_key
		WHERE card.id = next_key.id
		RETURNING card.encrypted_key`, userID, sessionID, PixlabSMSCardKeyMaxClaims).Scan(&encryptedKey)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", ErrPixlabSMSNoCardKey
	}
	if err != nil {
		return "", "", fmt.Errorf("claim member sms card key: %w", err)
	}

	var balance float64
	err = tx.QueryRowContext(ctx, `
		UPDATE users
		SET balance = balance - $1, updated_at = NOW()
		WHERE id = $2 AND deleted_at IS NULL AND balance >= $1
		RETURNING balance`, fee, userID).Scan(&balance)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", ErrPixlabSMSBalance
	}
	if err != nil {
		return "", "", fmt.Errorf("reserve member sms balance: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO xiass_sms_member_charges (session_id, user_id, amount, status)
		VALUES ($1, $2, $3, 'held')`, sessionID, userID, fee); err != nil {
		return "", "", fmt.Errorf("record member sms charge: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return "", "", fmt.Errorf("commit member sms claim: %w", err)
	}
	s.invalidateBalance(ctx, userID)
	return sessionID, encryptedKey, nil
}

func (s *PixlabSMSService) memberResult(ctx context.Context, userID int64, sourceSessionID string, result *PixlabSMSResult) (*PixlabSMSMemberResult, error) {
	if result == nil {
		return nil, infraerrors.ServiceUnavailable("SMS_PROVIDER_INVALID_RESPONSE", "接码服务返回了无效数据")
	}
	var balance float64
	if err := s.db.QueryRowContext(ctx, `
		SELECT balance FROM users WHERE id = $1 AND deleted_at IS NULL`, userID).Scan(&balance); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, infraerrors.NotFound("USER_NOT_FOUND", "用户不存在")
		}
		return nil, fmt.Errorf("load member sms balance: %w", err)
	}
	chargeState := ""
	feeAmount := 0.0
	if err := s.db.QueryRowContext(ctx, `
		SELECT status, amount
		FROM xiass_sms_member_charges
		WHERE session_id = $1 AND user_id = $2`, sourceSessionID, userID).Scan(&chargeState, &feeAmount); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("load member sms charge: %w", err)
	}
	var actionAvailableAt *time.Time
	if result.SessionID != "" {
		var claimedAt time.Time
		err := s.db.QueryRowContext(ctx, `
			SELECT consumed_at
			FROM xiass_sms_card_keys
			WHERE session_id = $1 AND owner_user_id = $2 AND status = 'active'`, result.SessionID, userID).Scan(&claimedAt)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("load member sms action time: %w", err)
		}
		if err == nil {
			availableAt := claimedAt.Add(PixlabSMSMemberMutationDelay)
			actionAvailableAt = &availableAt
		}
	}
	return &PixlabSMSMemberResult{
		SessionID:         result.SessionID,
		Status:            result.Status,
		Number:            result.Number,
		Country:           result.Country,
		Code:              result.Code,
		QueuedCount:       0,
		FeeAmount:         feeAmount,
		ChargeState:       chargeState,
		Balance:           balance,
		ActionAvailableAt: actionAvailableAt,
	}, nil
}

func (s *PixlabSMSService) captureMemberCharge(ctx context.Context, sessionID string, userID int64) error {
	if s == nil || s.db == nil {
		return infraerrors.InternalServer("SMS_RECEIVER_UNAVAILABLE", "接码服务尚未初始化")
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE xiass_sms_member_charges
		SET status = 'captured', captured_at = NOW(), updated_at = NOW()
		WHERE session_id = $1 AND user_id = $2 AND status = 'held'`, sessionID, userID)
	if err != nil {
		return fmt.Errorf("capture member sms charge: %w", err)
	}
	return nil
}

func (s *PixlabSMSService) releaseMemberCharge(ctx context.Context, sessionID string, userID int64) error {
	if s == nil || s.db == nil {
		return infraerrors.InternalServer("SMS_RECEIVER_UNAVAILABLE", "接码服务尚未初始化")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin member sms refund: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var amount float64
	err = tx.QueryRowContext(ctx, `
		SELECT amount
		FROM xiass_sms_member_charges
		WHERE session_id = $1 AND user_id = $2 AND status = 'held'
		FOR UPDATE`, sessionID, userID).Scan(&amount)
	if errors.Is(err, sql.ErrNoRows) {
		return tx.Commit()
	}
	if err != nil {
		return fmt.Errorf("lock member sms charge: %w", err)
	}
	var balance float64
	if err := tx.QueryRowContext(ctx, `
		UPDATE users
		SET balance = balance + $1, updated_at = NOW()
		WHERE id = $2 AND deleted_at IS NULL
		RETURNING balance`, amount, userID).Scan(&balance); err != nil {
		return fmt.Errorf("release member sms balance: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE xiass_sms_member_charges
		SET status = 'released', released_at = NOW(), updated_at = NOW()
		WHERE session_id = $1 AND user_id = $2 AND status = 'held'`, sessionID, userID); err != nil {
		return fmt.Errorf("mark member sms charge released: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit member sms refund: %w", err)
	}
	s.invalidateBalance(ctx, userID)
	return nil
}

func (s *PixlabSMSService) invalidateBalance(ctx context.Context, userID int64) {
	if s == nil || s.billingCache == nil || userID <= 0 {
		return
	}
	if err := s.billingCache.InvalidateUserBalance(ctx, userID); err != nil {
		// Cache invalidation must not undo a committed payment operation. The
		// next database-backed read still observes the correct balance.
		return
	}
}

type pixlabSMSProviderResponse struct {
	Success json.RawMessage `json:"success"`
	Message string          `json:"message"`
	Error   string          `json:"error"`
	Status  string          `json:"status"`
	Number  string          `json:"number"`
	Country string          `json:"country"`
	Code    string          `json:"code"`
}

// Pixlab's payload fields have been observed as both JSON strings and JSON
// numbers. Decode all provider values as raw JSON, then normalize locally so a
// numeric phone number or verification code cannot discard the whole payload.
type pixlabSMSProviderPayload struct {
	Success json.RawMessage `json:"success"`
	Message json.RawMessage `json:"message"`
	Error   json.RawMessage `json:"error"`
	Status  json.RawMessage `json:"status"`
	Number  json.RawMessage `json:"number"`
	Country json.RawMessage `json:"country"`
	Code    json.RawMessage `json:"code"`
}

func (s *PixlabSMSService) callProvider(ctx context.Context, action, cardKey string) (*pixlabSMSProviderResponse, error) {
	body, err := json.Marshal(map[string]string{"cardKey": cardKey})
	if err != nil {
		return nil, fmt.Errorf("encode sms provider request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+"/"+action, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create sms provider request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, infraerrors.ServiceUnavailable("SMS_PROVIDER_UNAVAILABLE", "接码服务暂时无法连接，请稍后重试")
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, infraerrors.ServiceUnavailable("SMS_PROVIDER_UNAVAILABLE", "读取接码服务响应失败，请稍后重试")
	}
	payload := &pixlabSMSProviderPayload{}
	if len(data) > 0 {
		if err := json.Unmarshal(data, payload); err != nil {
			return nil, infraerrors.ServiceUnavailable("SMS_PROVIDER_INVALID_RESPONSE", "接码服务返回了无效数据")
		}
	}
	provider := &pixlabSMSProviderResponse{
		Success: payload.Success,
		Message: pixlabRawText(payload.Message),
		Error:   pixlabRawText(payload.Error),
		Status:  pixlabRawText(payload.Status),
		Number:  pixlabRawText(payload.Number),
		Country: pixlabRawText(payload.Country),
		Code:    pixlabRawText(payload.Code),
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		// Pixlab returns the per-card sixth-use circuit-breaker as a non-2xx
		// response. Preserve that signal so Redeem can retire the spent card and
		// continue with the next card instead of surfacing a generic outage.
		if message := pixlabProviderMessage(provider); isPixlabCardUsageLimitMessage(message) {
			return nil, infraerrors.BadRequest("SMS_PROVIDER_REJECTED", message)
		}
		return nil, infraerrors.ServiceUnavailable("SMS_PROVIDER_HTTP_ERROR", "接码服务请求失败，请稍后重试")
	}
	if !pixlabSuccess(provider.Success) {
		message := pixlabProviderMessage(provider)
		if message == "" {
			message = "接码服务未能完成该操作"
		}
		return nil, infraerrors.BadRequest("SMS_PROVIDER_REJECTED", message)
	}
	provider.Status = strings.ToUpper(strings.TrimSpace(provider.Status))
	if provider.Status == "" {
		provider.Status = "WAITING"
	}
	return provider, nil
}

func pixlabSuccess(raw json.RawMessage) bool {
	value := strings.ToLower(strings.TrimSpace(pixlabRawText(raw)))
	return value == "true" || value == "1" || value == "ok" || value == "success"
}

func isPixlabCardUsageLimitError(err error) bool {
	if infraerrors.Reason(err) != "SMS_PROVIDER_REJECTED" {
		return false
	}
	return isPixlabCardUsageLimitMessage(infraerrors.Message(err))
}

func isPixlabCardUsageLimitMessage(raw string) bool {
	message := strings.ToLower(strings.TrimSpace(raw))
	compact := strings.NewReplacer(" ", "", "\t", "", "\n", "").Replace(message)
	return strings.Contains(message, "连续换号上限") ||
		strings.Contains(message, "单卡连续换号") ||
		strings.Contains(message, "触发熔断") ||
		strings.Contains(compact, "6次")
}

func pixlabProviderMessage(provider *pixlabSMSProviderResponse) string {
	if provider == nil {
		return ""
	}
	if message := strings.TrimSpace(provider.Message); message != "" {
		return message
	}
	return strings.TrimSpace(provider.Error)
}

func pixlabRawText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	var number json.Number
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&number); err == nil {
		return number.String()
	}
	var flag bool
	if err := json.Unmarshal(raw, &flag); err == nil {
		if flag {
			return "true"
		}
		return "false"
	}
	return ""
}

func pixlabTerminalStatus(status, code string) string {
	if pixlabHasVerificationCode(code) {
		return "completed"
	}
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "RECEIVED", "COMPLETED", "USED":
		return "completed"
	case "EXPIRED", "TIMEOUT":
		return "expired"
	case "CANCELLED", "CANCELED":
		return "cancelled"
	case "FAILED", "ERROR":
		return "failed"
	case "EXHAUSTED":
		return "exhausted"
	default:
		return ""
	}
}

func pixlabHasVerificationCode(code string) bool {
	trimmed := strings.TrimSpace(code)
	return trimmed != "" && trimmed != "--"
}

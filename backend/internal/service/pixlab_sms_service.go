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
)

const pixlabSMSAPIBaseURL = "https://sms.pixlab.cc/api"

const (
	// PixlabSMSMemberFee is the first-installation default. Runtime charges are
	// read from the administrator-managed database setting for every new claim.
	PixlabSMSMemberFee = 2.00
	// PixlabSMSMemberMutationDelay protects a newly issued number from being
	// churned immediately. It applies to member changes and cancellations only.
	PixlabSMSMemberMutationDelay = time.Minute
)

var (
	ErrPixlabSMSNoCardKey = infraerrors.NotFound("SMS_CARD_KEY_UNAVAILABLE", "暂无待用接码卡密")
	ErrPixlabSMSSession   = infraerrors.NotFound("SMS_SESSION_NOT_FOUND", "接码会话不存在或已结束")
	ErrPixlabSMSBalance   = infraerrors.BadRequest("SMS_BALANCE_INSUFFICIENT", "余额不足，无法领取授权接码号码")
	ErrPixlabSMSActive    = infraerrors.Conflict("SMS_ACTIVE_SESSION_EXISTS", "当前已有进行中的接码会话，请先完成、换号或取消")
	ErrPixlabSMSLocked    = infraerrors.Conflict("SMS_SESSION_ACTION_LOCKED", "领取号码后需等待 1 分钟才可换号或取消")
)

// PixlabSMSQueueStatus intentionally contains counters only. Card keys never
// leave the server after they have been submitted once.
type PixlabSMSQueueStatus struct {
	QueuedCount int64 `json:"queued_count"`
	ActiveCount int64 `json:"active_count"`
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
// It deliberately has no API that returns decrypted card keys.
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
			INSERT INTO xiass_sms_card_keys (encrypted_key, key_fingerprint)
			VALUES ($1, $2)
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
		SELECT EXISTS(SELECT 1 FROM xiass_sms_card_keys WHERE status = 'queued')`).Scan(&status.Available); err != nil {
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
	sessionID := uuid.NewString()
	var encryptedKey string
	err := s.db.QueryRowContext(ctx, `
		WITH next_key AS (
			SELECT id
			FROM xiass_sms_card_keys
			WHERE status = 'queued'
			ORDER BY id
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		UPDATE xiass_sms_card_keys AS card
		SET status = 'active', owner_user_id = $1, session_id = $2,
			consumed_at = NOW(), updated_at = NOW()
		FROM next_key
		WHERE card.id = next_key.id
		RETURNING card.encrypted_key`, ownerUserID, sessionID).Scan(&encryptedKey)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrPixlabSMSNoCardKey
	}
	if err != nil {
		return nil, fmt.Errorf("claim sms card key: %w", err)
	}

	key, err := s.encryptor.Decrypt(encryptedKey)
	if err != nil {
		_ = s.releaseSession(ctx, sessionID, ownerUserID)
		return nil, fmt.Errorf("decrypt claimed sms card key: %w", err)
	}
	provider, err := s.callProvider(ctx, "redeem", key)
	if err != nil {
		_ = s.releaseSession(ctx, sessionID, ownerUserID)
		return nil, err
	}
	return s.finishProviderResponse(ctx, sessionID, ownerUserID, provider, false)
}

// RedeemForMember reserves ¥2.00 and claims one card in the same database
// transaction. The money remains reversible until a real verification code is
// reported by the provider; that final event captures the charge permanently.
func (s *PixlabSMSService) RedeemForMember(ctx context.Context, userID int64) (*PixlabSMSMemberResult, error) {
	if userID <= 0 {
		return nil, infraerrors.Unauthorized("SMS_OWNER_REQUIRED", "无法识别当前用户")
	}

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

// ChangeNumber cancels and releases the active number, then redeems again. The
// released card returns to the head of the FIFO queue unless it has already
// received a verification code.
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
		// Expiry, cancellation, and provider-side failures do not consume a
		// card. Return it to the FIFO queue for another authorization attempt.
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
// the FIFO queue. Session ownership is cleared so a later flow can claim it.
func (s *PixlabSMSService) releaseSession(ctx context.Context, sessionID string, ownerUserID int64) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE xiass_sms_card_keys
		SET status = 'queued', owner_user_id = NULL, session_id = NULL,
			consumed_at = NULL, updated_at = NOW()
		WHERE session_id = $1 AND owner_user_id = $2 AND status = 'active'`, sessionID, ownerUserID)
	if err != nil {
		return fmt.Errorf("release sms session: %w", err)
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
			WHERE status = 'queued'
			ORDER BY id
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		UPDATE xiass_sms_card_keys AS card
		SET status = 'active', owner_user_id = $1, session_id = $2,
			consumed_at = NOW(), updated_at = NOW()
		FROM next_key
		WHERE card.id = next_key.id
		RETURNING card.encrypted_key`, userID, sessionID).Scan(&encryptedKey)
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
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, infraerrors.ServiceUnavailable("SMS_PROVIDER_HTTP_ERROR", "接码服务请求失败，请稍后重试")
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
	if !pixlabSuccess(provider.Success) {
		message := strings.TrimSpace(provider.Message)
		if message == "" {
			message = strings.TrimSpace(provider.Error)
		}
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
	default:
		return ""
	}
}

func pixlabHasVerificationCode(code string) bool {
	trimmed := strings.TrimSpace(code)
	return trimmed != "" && trimmed != "--"
}

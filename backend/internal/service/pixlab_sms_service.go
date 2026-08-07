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

var (
	ErrPixlabSMSNoCardKey = infraerrors.NotFound("SMS_CARD_KEY_UNAVAILABLE", "暂无待用接码卡密")
	ErrPixlabSMSSession   = infraerrors.NotFound("SMS_SESSION_NOT_FOUND", "接码会话不存在或已结束")
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

// PixlabSMSService owns server-side card-key persistence and provider proxying.
// It deliberately has no API that returns decrypted card keys.
type PixlabSMSService struct {
	db        *sql.DB
	encryptor SecretEncryptor
	client    *http.Client
	baseURL   string
}

func NewPixlabSMSService(db *sql.DB, encryptor SecretEncryptor) *PixlabSMSService {
	return newPixlabSMSService(db, encryptor, &http.Client{Timeout: 20 * time.Second}, pixlabSMSAPIBaseURL)
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
	return s.finishProviderResponse(ctx, sessionID, ownerUserID, provider)
}

func (s *PixlabSMSService) Resume(ctx context.Context, ownerUserID int64, sessionID string) (*PixlabSMSResult, error) {
	return s.withActiveSession(ctx, ownerUserID, sessionID, "resume")
}

func (s *PixlabSMSService) Check(ctx context.Context, ownerUserID int64, sessionID string) (*PixlabSMSResult, error) {
	return s.withActiveSession(ctx, ownerUserID, sessionID, "check")
}

func (s *PixlabSMSService) Cancel(ctx context.Context, ownerUserID int64, sessionID string) (*PixlabSMSResult, error) {
	result, err := s.withActiveSession(ctx, ownerUserID, sessionID, "cancel")
	if err != nil {
		return nil, err
	}
	// Cancellation does not invalidate a card key. A terminal response is
	// released by finishProviderResponse; a non-terminal response needs an
	// explicit release before the next redemption.
	if !pixlabHasVerificationCode(result.Code) && pixlabTerminalStatus(result.Status, "") == "" {
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

// ChangeNumber cancels and releases the active number, then redeems again. The
// released card returns to the head of the FIFO queue unless it has already
// received a verification code.
func (s *PixlabSMSService) ChangeNumber(ctx context.Context, ownerUserID int64, sessionID string) (*PixlabSMSResult, error) {
	if _, err := s.Cancel(ctx, ownerUserID, sessionID); err != nil {
		return nil, err
	}
	return s.Redeem(ctx, ownerUserID)
}

func (s *PixlabSMSService) withActiveSession(ctx context.Context, ownerUserID int64, sessionID, action string) (*PixlabSMSResult, error) {
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
	return s.finishProviderResponse(ctx, sessionID, ownerUserID, provider)
}

func (s *PixlabSMSService) finishProviderResponse(ctx context.Context, sessionID string, ownerUserID int64, provider *pixlabSMSProviderResponse) (*PixlabSMSResult, error) {
	if provider == nil {
		return nil, infraerrors.ServiceUnavailable("SMS_PROVIDER_INVALID_RESPONSE", "接码服务返回了无效数据")
	}
	responseSessionID := sessionID
	if pixlabHasVerificationCode(provider.Code) {
		if err := s.finishSession(ctx, sessionID, ownerUserID); err != nil {
			return nil, err
		}
		responseSessionID = ""
	} else if pixlabTerminalStatus(provider.Status, "") != "" {
		// Expiry, cancellation, and provider-side failures do not consume a
		// card. Return it to the FIFO queue for another authorization attempt.
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

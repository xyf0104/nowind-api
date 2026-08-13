package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

type pixlabSMSPrefixEncryptor struct{}

func (pixlabSMSPrefixEncryptor) Encrypt(value string) (string, error) { return "enc:" + value, nil }
func (pixlabSMSPrefixEncryptor) Decrypt(value string) (string, error) {
	return value[len("enc:"):], nil
}

func expectExpiredPixlabSMSSessions(mock sqlmock.Sqlmock, ownerUserID int64, rows *sqlmock.Rows) {
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT card.session_id, card.owner_user_id
		FROM xiass_sms_card_keys AS card
		WHERE card.status = 'active'
			AND card.session_id IS NOT NULL
			AND card.owner_user_id IS NOT NULL
			AND card.consumed_at + ($1 * INTERVAL '1 second') <= NOW()
			AND ($2 = 0 OR card.owner_user_id = $2)
		ORDER BY card.id`)).
		WithArgs(int64(PixlabSMSSessionValidity/time.Second), ownerUserID).
		WillReturnRows(rows)
}

func noExpiredPixlabSMSSessions() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"session_id", "owner_user_id"})
}

func expectPixlabSMSExpire(mock sqlmock.Sqlmock, sessionID string, ownerUserID int64, cardID int64, settleMemberFee bool) {
	mock.ExpectBegin()
	if settleMemberFee {
		mock.ExpectExec(regexp.QuoteMeta(`SELECT pg_advisory_xact_lock($1)`)).
			WithArgs(ownerUserID).
			WillReturnResult(sqlmock.NewResult(0, 1))
	}
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT id
		FROM xiass_sms_card_keys
		WHERE session_id = $1 AND owner_user_id = $2 AND status = 'active'
			AND consumed_at + ($3 * INTERVAL '1 second') <= NOW()
		FOR UPDATE`)).
		WithArgs(sessionID, ownerUserID, int64(PixlabSMSSessionValidity/time.Second)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(cardID))
	if settleMemberFee {
		mock.ExpectQuery(regexp.QuoteMeta(`
			SELECT amount
			FROM xiass_sms_member_charges
			WHERE session_id = $1 AND user_id = $2 AND status = 'held'
			FOR UPDATE`)).
			WithArgs(sessionID, ownerUserID).
			WillReturnRows(sqlmock.NewRows([]string{"amount"}))
	}
	mock.ExpectExec(regexp.QuoteMeta(`
		UPDATE xiass_sms_card_keys
		SET status = CASE WHEN claim_count >= $3 THEN 'exhausted' ELSE 'queued' END,
			owner_user_id = NULL, session_id = NULL,
			consumed_at = NULL, updated_at = NOW()
		WHERE id = $1 AND owner_user_id = $2 AND status = 'active'`)).
		WithArgs(cardID, ownerUserID, PixlabSMSCardKeyMaxClaims).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
}

func expectPixlabSMSRedeemStartFallback(mock sqlmock.Sqlmock, ownerUserID int64) {
	mock.ExpectExec(regexp.QuoteMeta(`
		UPDATE xiass_sms_card_keys
		SET consumed_at = NOW(), updated_at = NOW()
		WHERE session_id = $1 AND owner_user_id = $2 AND status = 'active'`)).
		WithArgs(sqlmock.AnyArg(), ownerUserID).
		WillReturnResult(sqlmock.NewResult(0, 1))
}

func expectPixlabSMSSettlement(mock sqlmock.Sqlmock, ownerUserID, cardID int64, member bool, settlement pixlabSettlement) {
	mock.ExpectBegin()
	if member {
		mock.ExpectExec(regexp.QuoteMeta(`SELECT pg_advisory_xact_lock($1)`)).
			WithArgs(ownerUserID).
			WillReturnResult(sqlmock.NewResult(0, 1))
	}
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT id
		FROM xiass_sms_card_keys
		WHERE session_id = $1 AND owner_user_id = $2 AND status = 'active'
		FOR UPDATE`)).
		WithArgs(sqlmock.AnyArg(), ownerUserID).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(cardID))
	if member {
		mock.ExpectQuery(regexp.QuoteMeta(`
			SELECT amount
			FROM xiass_sms_member_charges
			WHERE session_id = $1 AND user_id = $2 AND status = 'held'
			FOR UPDATE`)).
			WithArgs(sqlmock.AnyArg(), ownerUserID).
			WillReturnRows(sqlmock.NewRows([]string{"amount"}).AddRow(PixlabSMSMemberFee))
		if settlement == pixlabSettlementCapture {
			mock.ExpectExec(regexp.QuoteMeta(`
				UPDATE xiass_sms_member_charges
				SET status = 'captured', captured_at = NOW(), updated_at = NOW()
				WHERE session_id = $1 AND user_id = $2 AND status = 'held'`)).
				WithArgs(sqlmock.AnyArg(), ownerUserID).
				WillReturnResult(sqlmock.NewResult(0, 1))
		} else {
			mock.ExpectExec(regexp.QuoteMeta(`
				UPDATE users
				SET balance = balance + $1, updated_at = NOW()
				WHERE id = $2 AND deleted_at IS NULL`)).
				WithArgs(PixlabSMSMemberFee, ownerUserID).
				WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectExec(regexp.QuoteMeta(`
				UPDATE xiass_sms_member_charges
				SET status = 'released', released_at = NOW(), updated_at = NOW()
				WHERE session_id = $1 AND user_id = $2 AND status = 'held'`)).
				WithArgs(sqlmock.AnyArg(), ownerUserID).
				WillReturnResult(sqlmock.NewResult(0, 1))
		}
	}
	switch settlement {
	case pixlabSettlementCapture:
		mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM xiass_sms_card_keys WHERE id = $1 AND status = 'active'`)).
			WithArgs(cardID).
			WillReturnResult(sqlmock.NewResult(0, 1))
	case pixlabSettlementExhaust:
		mock.ExpectExec(regexp.QuoteMeta(`
			UPDATE xiass_sms_card_keys
			SET status = 'exhausted', owner_user_id = NULL, session_id = NULL,
				consumed_at = NULL, updated_at = NOW()
			WHERE id = $1 AND status = 'active'`)).
			WithArgs(cardID).
			WillReturnResult(sqlmock.NewResult(0, 1))
	case pixlabSettlementRelease:
		mock.ExpectExec(regexp.QuoteMeta(`
			UPDATE xiass_sms_card_keys
			SET status = CASE WHEN claim_count >= $2 THEN 'exhausted' ELSE 'queued' END,
				owner_user_id = NULL, session_id = NULL,
				consumed_at = NULL, updated_at = NOW()
			WHERE id = $1 AND status = 'active'`)).
			WithArgs(cardID, PixlabSMSCardKeyMaxClaims).
			WillReturnResult(sqlmock.NewResult(0, 1))
	}
	mock.ExpectCommit()
}

func expectPixlabSMSSessionExpiry(mock sqlmock.Sqlmock, sessionID any, ownerUserID int64, consumedAt time.Time) {
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT consumed_at
		FROM xiass_sms_card_keys
		WHERE session_id = $1 AND owner_user_id = $2 AND status = 'active'`)).
		WithArgs(sessionID, ownerUserID).
		WillReturnRows(sqlmock.NewRows([]string{"consumed_at"}).AddRow(consumedAt))
}

func TestPixlabSMSServiceAddCardKeysEncryptsAndDeduplicates(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	insert := regexp.QuoteMeta(`
			INSERT INTO xiass_sms_card_keys (encrypted_key, key_fingerprint, queue_rank)
			VALUES (
				$1,
				$2,
				(SELECT COALESCE(MAX(queue_rank), 0) + 1 FROM xiass_sms_card_keys)
			)
			ON CONFLICT (key_fingerprint) DO NOTHING
			RETURNING id`)
	mock.ExpectQuery(insert).
		WithArgs("enc:CARD-A", pixlabCardKeyFingerprint("CARD-A")).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT
			COUNT(*) FILTER (WHERE status = 'queued'),
			COUNT(*) FILTER (WHERE status = 'active' AND ($1 = 0 OR owner_user_id = $1))
		FROM xiass_sms_card_keys`)).
		WithArgs(int64(0)).
		WillReturnRows(sqlmock.NewRows([]string{"queued", "active"}).AddRow(1, 0))

	svc := newPixlabSMSService(db, pixlabSMSPrefixEncryptor{}, nil, "http://example.invalid")
	added, status, err := svc.AddCardKeys(context.Background(), "CARD-A\nCARD-A")
	require.NoError(t, err)
	require.Equal(t, 1, added)
	require.Equal(t, int64(1), status.QueuedCount)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPixlabSMSServiceListCardKeysDecryptsForAdminManagement(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT id, encrypted_key, status, claim_count, queue_rank, last_claimed_at, created_at
		FROM xiass_sms_card_keys
		ORDER BY
			CASE status WHEN 'queued' THEN 0 WHEN 'active' THEN 1 ELSE 2 END,
			claim_count ASC,
			queue_rank ASC,
			last_claimed_at NULLS FIRST,
			id ASC`)).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "encrypted_key", "status", "claim_count", "queue_rank", "last_claimed_at", "created_at",
		}).
			AddRow(int64(4), "enc:SXXL-CURRENT", "queued", 1, int64(2), nil, time.Unix(0, 0)))
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT
			COUNT(*) FILTER (WHERE status = 'queued'),
			COUNT(*) FILTER (WHERE status = 'active' AND ($1 = 0 OR owner_user_id = $1))
		FROM xiass_sms_card_keys`)).
		WithArgs(int64(0)).
		WillReturnRows(sqlmock.NewRows([]string{"queued", "active"}).AddRow(1, 0))

	svc := newPixlabSMSService(db, pixlabSMSPrefixEncryptor{}, nil, "http://example.invalid")
	keys, status, err := svc.ListCardKeys(context.Background())
	require.NoError(t, err)
	require.Len(t, keys, 1)
	require.Equal(t, int64(4), keys[0].ID)
	require.Equal(t, "SXXL-CURRENT", keys[0].CardKey)
	require.Equal(t, "queued", keys[0].Status)
	require.Equal(t, 1, keys[0].ClaimCount)
	require.Equal(t, int64(2), keys[0].QueueRank)
	require.Equal(t, int64(1), status.QueuedCount)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPixlabSMSServiceDeleteCardKeyRemovesOnlyRequestedKey(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT status, session_id
		FROM xiass_sms_card_keys
		WHERE id = $1`)).
		WithArgs(int64(12)).
		WillReturnRows(sqlmock.NewRows([]string{"status", "session_id"}).AddRow("queued", nil))
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT status, owner_user_id, session_id
		FROM xiass_sms_card_keys
		WHERE id = $1
		FOR UPDATE`)).
		WithArgs(int64(12)).
		WillReturnRows(sqlmock.NewRows([]string{"status", "owner_user_id", "session_id"}).AddRow("queued", nil, nil))
	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM xiass_sms_card_keys WHERE id = $1`)).
		WithArgs(int64(12)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT
			COUNT(*) FILTER (WHERE status = 'queued'),
			COUNT(*) FILTER (WHERE status = 'active' AND ($1 = 0 OR owner_user_id = $1))
		FROM xiass_sms_card_keys`)).
		WithArgs(int64(0)).
		WillReturnRows(sqlmock.NewRows([]string{"queued", "active"}).AddRow(3, 1))

	svc := newPixlabSMSService(db, pixlabSMSPrefixEncryptor{}, nil, "http://example.invalid")
	status, err := svc.DeleteCardKey(context.Background(), 12)
	require.NoError(t, err)
	require.Equal(t, int64(3), status.QueuedCount)
	require.Equal(t, int64(1), status.ActiveCount)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPixlabSMSServiceDeleteActiveMemberCardReleasesHeldBalance(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT status, session_id
		FROM xiass_sms_card_keys
		WHERE id = $1`)).
		WithArgs(int64(12)).
		WillReturnRows(sqlmock.NewRows([]string{"status", "session_id"}).AddRow("active", "member-session"))
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT status, owner_user_id, session_id
		FROM xiass_sms_card_keys
		WHERE id = $1
		FOR UPDATE`)).
		WithArgs(int64(12)).
		WillReturnRows(sqlmock.NewRows([]string{"status", "owner_user_id", "session_id"}).AddRow("active", int64(7), "member-session"))
	mock.ExpectQuery(regexp.QuoteMeta(`
			SELECT amount
			FROM xiass_sms_member_charges
			WHERE session_id = $1 AND user_id = $2 AND status = 'held'
			FOR UPDATE`)).
		WithArgs("member-session", int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"amount"}).AddRow(2.0))
	mock.ExpectExec(regexp.QuoteMeta(`
				UPDATE users
				SET balance = balance + $1, updated_at = NOW()
				WHERE id = $2 AND deleted_at IS NULL`)).
		WithArgs(2.0, int64(7)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta(`
				UPDATE xiass_sms_member_charges
				SET status = 'released', released_at = NOW(), updated_at = NOW()
				WHERE session_id = $1 AND user_id = $2 AND status = 'held'`)).
		WithArgs("member-session", int64(7)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM xiass_sms_card_keys WHERE id = $1`)).
		WithArgs(int64(12)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT
			COUNT(*) FILTER (WHERE status = 'queued'),
			COUNT(*) FILTER (WHERE status = 'active' AND ($1 = 0 OR owner_user_id = $1))
		FROM xiass_sms_card_keys`)).
		WithArgs(int64(0)).
		WillReturnRows(sqlmock.NewRows([]string{"queued", "active"}).AddRow(3, 0))

	svc := newPixlabSMSService(db, pixlabSMSPrefixEncryptor{}, nil, "http://example.invalid")
	status, err := svc.DeleteCardKey(context.Background(), 12)
	require.NoError(t, err)
	require.Equal(t, int64(3), status.QueuedCount)
	require.Equal(t, int64(0), status.ActiveCount)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPixlabSMSServiceReorderCardKeysUpdatesQueuedCards(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	mock.ExpectBegin()
	for rank, cardKeyID := range []int64{8, 3, 11} {
		mock.ExpectExec(regexp.QuoteMeta(`
			UPDATE xiass_sms_card_keys
			SET queue_rank = $1, updated_at = NOW()
			WHERE id = $2 AND status = 'queued'`)).
			WithArgs(int64(rank+1), cardKeyID).
			WillReturnResult(sqlmock.NewResult(0, 1))
	}
	mock.ExpectCommit()
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT
			COUNT(*) FILTER (WHERE status = 'queued'),
			COUNT(*) FILTER (WHERE status = 'active' AND ($1 = 0 OR owner_user_id = $1))
		FROM xiass_sms_card_keys`)).
		WithArgs(int64(0)).
		WillReturnRows(sqlmock.NewRows([]string{"queued", "active"}).AddRow(3, 1))

	svc := newPixlabSMSService(db, pixlabSMSPrefixEncryptor{}, nil, "http://example.invalid")
	status, err := svc.ReorderCardKeys(context.Background(), []int64{8, 3, 11})
	require.NoError(t, err)
	require.Equal(t, int64(3), status.QueuedCount)
	require.Equal(t, int64(1), status.ActiveCount)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPixlabSMSServiceRedeemHandlesNumericProviderFieldsWithoutLeakingCardKey(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	var receivedCardKey string
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/redeem", r.URL.Path)
		var request map[string]string
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		receivedCardKey = request["cardKey"]
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"number":12025550123,"country":"美国","status":"WAITING","code":654321}`))
	}))
	t.Cleanup(provider.Close)

	claim := regexp.QuoteMeta(`
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
		RETURNING card.encrypted_key`)
	mock.ExpectQuery(claim).
		WithArgs(int64(42), sqlmock.AnyArg(), PixlabSMSCardKeyMaxClaims).
		WillReturnRows(sqlmock.NewRows([]string{"encrypted_key"}).AddRow("enc:SERVER-ONLY-CARD"))
	mock.ExpectExec(regexp.QuoteMeta(`
		DELETE FROM xiass_sms_card_keys
		WHERE session_id = $1 AND owner_user_id = $2 AND status = 'active'`)).
		WithArgs(sqlmock.AnyArg(), int64(42)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT
			COUNT(*) FILTER (WHERE status = 'queued'),
			COUNT(*) FILTER (WHERE status = 'active' AND ($1 = 0 OR owner_user_id = $1))
		FROM xiass_sms_card_keys`)).
		WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"queued", "active"}).AddRow(3, 0))

	svc := newPixlabSMSService(db, pixlabSMSPrefixEncryptor{}, provider.Client(), provider.URL)
	result, err := svc.Redeem(context.Background(), 42)
	require.NoError(t, err)
	require.Equal(t, "SERVER-ONLY-CARD", receivedCardKey)
	require.Empty(t, result.SessionID)
	require.Equal(t, "12025550123", result.Number)
	require.Equal(t, "654321", result.Code)
	require.Equal(t, int64(3), result.QueuedCount)
	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "SERVER-ONLY-CARD")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPixlabSMSServiceRedeemUsesProviderCreatedAtForExpiry(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/redeem", r.URL.Path)
		_, _ = w.Write([]byte(`{"success":true,"number":"27749433060","country":"南非","status":"WAITING","created_at":"2026-08-14 01:09:43"}`))
	}))
	t.Cleanup(provider.Close)

	mock.ExpectQuery("UPDATE xiass_sms_card_keys AS card").
		WithArgs(int64(42), sqlmock.AnyArg(), PixlabSMSCardKeyMaxClaims).
		WillReturnRows(sqlmock.NewRows([]string{"encrypted_key"}).AddRow("enc:SERVER-ONLY-CARD"))
	createdAt := time.Date(2026, time.August, 14, 1, 9, 43, 0, time.UTC)
	mock.ExpectExec(regexp.QuoteMeta(`
		UPDATE xiass_sms_card_keys
		SET consumed_at = $3, updated_at = NOW()
		WHERE session_id = $1 AND owner_user_id = $2 AND status = 'active'`)).
		WithArgs(sqlmock.AnyArg(), int64(42), createdAt).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT
			COUNT(*) FILTER (WHERE status = 'queued'),
			COUNT(*) FILTER (WHERE status = 'active' AND ($1 = 0 OR owner_user_id = $1))
		FROM xiass_sms_card_keys`)).
		WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"queued", "active"}).AddRow(11, 1))
	expectPixlabSMSSessionExpiry(mock, sqlmock.AnyArg(), 42, createdAt)

	svc := newPixlabSMSService(db, pixlabSMSPrefixEncryptor{}, provider.Client(), provider.URL)
	result, err := svc.Redeem(context.Background(), 42)
	require.NoError(t, err)
	require.Equal(t, createdAt.Add(PixlabSMSSessionValidity), *result.ExpiresAt)
	require.False(t, result.ServerTime.IsZero())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPixlabSMSServiceMemberClaimReleasesExpiredSessionBeforeActiveCheck(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	expectExpiredPixlabSMSSessions(mock, 7, sqlmock.NewRows([]string{"session_id", "owner_user_id"}).
		AddRow("expired-member", int64(7)))
	expectPixlabSMSExpire(mock, "expired-member", 7, 9, true)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`SELECT pg_advisory_xact_lock($1)`)).
		WithArgs(int64(7)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT EXISTS").
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectQuery("SELECT member_fee").
		WillReturnRows(sqlmock.NewRows([]string{"member_fee"}).AddRow(PixlabSMSMemberFee))
	mock.ExpectQuery("UPDATE xiass_sms_card_keys AS card").
		WithArgs(int64(7), sqlmock.AnyArg(), PixlabSMSCardKeyMaxClaims).
		WillReturnRows(sqlmock.NewRows([]string{"encrypted_key"}).AddRow("enc:NEXT-CARD"))
	mock.ExpectQuery("UPDATE users").
		WithArgs(PixlabSMSMemberFee, int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow(8.0))
	mock.ExpectExec("INSERT INTO xiass_sms_member_charges").
		WithArgs(sqlmock.AnyArg(), int64(7), PixlabSMSMemberFee).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"status":"RECEIVED","code":"123456"}`))
	}))
	t.Cleanup(provider.Close)
	expectPixlabSMSSettlement(mock, 7, 25, true, pixlabSettlementCapture)
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT
			COUNT(*) FILTER (WHERE status = 'queued'),
			COUNT(*) FILTER (WHERE status = 'active' AND ($1 = 0 OR owner_user_id = $1))
		FROM xiass_sms_card_keys`)).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"queued", "active"}).AddRow(1, 0))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT balance FROM users WHERE id = $1 AND deleted_at IS NULL`)).
		WithArgs(int64(7)).WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow(8.0))
	mock.ExpectQuery("SELECT status, amount").
		WithArgs(sqlmock.AnyArg(), int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"status", "amount"}).AddRow("captured", PixlabSMSMemberFee))
	svc := newPixlabSMSService(db, pixlabSMSPrefixEncryptor{}, provider.Client(), provider.URL)
	result, err := svc.RedeemForMember(context.Background(), 7)
	require.NoError(t, err)
	require.Equal(t, "123456", result.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPixlabSMSServiceSettlementRollsBackRefundWhenCardReleaseFails(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`SELECT pg_advisory_xact_lock($1)`)).
		WithArgs(int64(7)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT id").
		WithArgs("member-session", int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(31)))
	mock.ExpectQuery("SELECT amount").
		WithArgs("member-session", int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"amount"}).AddRow(PixlabSMSMemberFee))
	mock.ExpectExec("UPDATE users").
		WithArgs(PixlabSMSMemberFee, int64(7)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE xiass_sms_member_charges").
		WithArgs("member-session", int64(7)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE xiass_sms_card_keys").
		WithArgs(int64(31), PixlabSMSCardKeyMaxClaims).
		WillReturnError(errors.New("write failed"))
	mock.ExpectRollback()

	svc := newPixlabSMSService(db, pixlabSMSPrefixEncryptor{}, nil, "http://example.invalid")
	err = svc.settleSession(context.Background(), "member-session", 7, true, pixlabSettlementRelease)
	require.ErrorContains(t, err, "write failed")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPixlabSMSServiceReleaseAndExhaustRejectMissingActiveSession(t *testing.T) {
	t.Run("release", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })

		mock.ExpectExec(regexp.QuoteMeta(`
				UPDATE xiass_sms_card_keys
				SET status = CASE WHEN claim_count >= $3 THEN 'exhausted' ELSE 'queued' END,
					owner_user_id = NULL, session_id = NULL,
					consumed_at = NULL, updated_at = NOW()
				WHERE session_id = $1 AND owner_user_id = $2 AND status = 'active'`)).
			WithArgs("missing-session", int64(42), PixlabSMSCardKeyMaxClaims).
			WillReturnResult(sqlmock.NewResult(0, 0))

		svc := newPixlabSMSService(db, pixlabSMSPrefixEncryptor{}, nil, "http://example.invalid")
		err = svc.releaseSession(context.Background(), "missing-session", 42)
		require.ErrorIs(t, err, ErrPixlabSMSSession)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("exhaust", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })

		mock.ExpectExec(regexp.QuoteMeta(`
				UPDATE xiass_sms_card_keys
				SET status = 'exhausted', owner_user_id = NULL, session_id = NULL,
					consumed_at = NULL, updated_at = NOW()
				WHERE session_id = $1 AND owner_user_id = $2 AND status = 'active'`)).
			WithArgs("missing-session", int64(42)).
			WillReturnResult(sqlmock.NewResult(0, 0))

		svc := newPixlabSMSService(db, pixlabSMSPrefixEncryptor{}, nil, "http://example.invalid")
		err = svc.exhaustSession(context.Background(), "missing-session", 42)
		require.ErrorIs(t, err, ErrPixlabSMSSession)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestPixlabSMSServiceExpireRollsBackWhenBalanceRefundUpdatesNoUser(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`SELECT pg_advisory_xact_lock($1)`)).
		WithArgs(int64(7)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT id
		FROM xiass_sms_card_keys
		WHERE session_id = $1 AND owner_user_id = $2 AND status = 'active'
			AND consumed_at + ($3 * INTERVAL '1 second') <= NOW()
		FOR UPDATE`)).
		WithArgs("expired-member", int64(7), int64(PixlabSMSSessionValidity/time.Second)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(31)))
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT amount
		FROM xiass_sms_member_charges
		WHERE session_id = $1 AND user_id = $2 AND status = 'held'
		FOR UPDATE`)).
		WithArgs("expired-member", int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"amount"}).AddRow(PixlabSMSMemberFee))
	mock.ExpectExec(regexp.QuoteMeta(`
		UPDATE users
		SET balance = balance + $1, updated_at = NOW()
		WHERE id = $2 AND deleted_at IS NULL`)).
		WithArgs(PixlabSMSMemberFee, int64(7)).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectRollback()

	svc := newPixlabSMSService(db, pixlabSMSPrefixEncryptor{}, nil, "http://example.invalid")
	expired, err := svc.expireSession(context.Background(), "expired-member", 7, true)
	require.False(t, expired)
	require.ErrorContains(t, err, "expected one affected row")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPixlabSMSServiceRedeemResumesExistingAdminSessionAfterOwnerConflict(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/resume", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"number":"5255510123","country":"墨西哥","status":"WAITING"}`))
	}))
	t.Cleanup(provider.Close)

	claim := regexp.QuoteMeta(`
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
			RETURNING card.encrypted_key`)
	mock.ExpectQuery(claim).
		WithArgs(int64(42), sqlmock.AnyArg(), PixlabSMSCardKeyMaxClaims).
		WillReturnError(&pq.Error{Code: "23505", Constraint: "uq_xiass_sms_card_keys_active_owner"})
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT session_id
		FROM xiass_sms_card_keys
		WHERE owner_user_id = $1 AND status = 'active'
		ORDER BY consumed_at DESC NULLS LAST, id DESC
		LIMIT 1`)).
		WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"session_id"}).AddRow("existing-session"))
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT encrypted_key, consumed_at
		FROM xiass_sms_card_keys
		WHERE session_id = $1 AND owner_user_id = $2 AND status = 'active'`)).
		WithArgs("existing-session", int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"encrypted_key", "consumed_at"}).AddRow("enc:SERVER-ONLY-CARD", time.Now()))
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT
			COUNT(*) FILTER (WHERE status = 'queued'),
			COUNT(*) FILTER (WHERE status = 'active' AND ($1 = 0 OR owner_user_id = $1))
		FROM xiass_sms_card_keys`)).
		WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"queued", "active"}).AddRow(4, 1))
	expectPixlabSMSSessionExpiry(mock, "existing-session", 42, time.Now())

	svc := newPixlabSMSService(db, pixlabSMSPrefixEncryptor{}, provider.Client(), provider.URL)
	result, err := svc.Redeem(context.Background(), 42)
	require.NoError(t, err)
	require.Equal(t, "existing-session", result.SessionID)
	require.Equal(t, "5255510123", result.Number)
	require.Equal(t, "WAITING", result.Status)
	require.Equal(t, int64(4), result.QueuedCount)
	encoded, marshalErr := json.Marshal(result)
	require.NoError(t, marshalErr)
	require.NotContains(t, string(encoded), "SERVER-ONLY-CARD")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPixlabSMSServiceCheckExpiresStaleAdminSessionWithoutProviderCall(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	providerCalls := 0
	provider := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		providerCalls++
	}))
	t.Cleanup(provider.Close)

	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT encrypted_key, consumed_at
		FROM xiass_sms_card_keys
		WHERE session_id = $1 AND owner_user_id = $2 AND status = 'active'`)).
		WithArgs("expired-session", int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"encrypted_key", "consumed_at"}).
			AddRow("enc:STALE-CARD", time.Now().Add(-PixlabSMSSessionValidity-time.Minute)))
	expectPixlabSMSExpire(mock, "expired-session", 42, 11, false)
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT
			COUNT(*) FILTER (WHERE status = 'queued'),
			COUNT(*) FILTER (WHERE status = 'active' AND ($1 = 0 OR owner_user_id = $1))
		FROM xiass_sms_card_keys`)).
		WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"queued", "active"}).AddRow(4, 0))

	svc := newPixlabSMSService(db, pixlabSMSPrefixEncryptor{}, provider.Client(), provider.URL)
	result, err := svc.Check(context.Background(), 42, "expired-session")
	require.NoError(t, err)
	require.Equal(t, "EXPIRED", result.Status)
	require.Empty(t, result.SessionID)
	require.Equal(t, int64(4), result.QueuedCount)
	require.Zero(t, providerCalls)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPixlabSMSServiceQueueStatusReleasesExpiredSession(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	expectExpiredPixlabSMSSessions(mock, 0, sqlmock.NewRows([]string{"session_id", "owner_user_id"}).
		AddRow("expired-session", int64(42)))
	expectPixlabSMSExpire(mock, "expired-session", 42, 11, true)
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT
			COUNT(*) FILTER (WHERE status = 'queued'),
			COUNT(*) FILTER (WHERE status = 'active' AND ($1 = 0 OR owner_user_id = $1))
		FROM xiass_sms_card_keys`)).
		WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"queued", "active"}).AddRow(12, 0))

	svc := newPixlabSMSService(db, pixlabSMSPrefixEncryptor{}, nil, "http://example.invalid")
	status, err := svc.QueueStatus(context.Background(), 42)
	require.NoError(t, err)
	require.Equal(t, int64(12), status.QueuedCount)
	require.Zero(t, status.ActiveCount)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPixlabSMSServiceRedeemReplacesExpiredAdminSession(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/redeem", r.URL.Path)
		_, _ = w.Write([]byte(`{"success":true,"number":"12025550126","country":"美国","status":"WAITING"}`))
	}))
	t.Cleanup(provider.Close)

	claim := regexp.QuoteMeta(`
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
			RETURNING card.encrypted_key`)
	mock.ExpectQuery(claim).
		WithArgs(int64(42), sqlmock.AnyArg(), PixlabSMSCardKeyMaxClaims).
		WillReturnError(&pq.Error{Code: "23505", Constraint: "uq_xiass_sms_card_keys_active_owner"})
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT session_id
		FROM xiass_sms_card_keys
		WHERE owner_user_id = $1 AND status = 'active'
		ORDER BY consumed_at DESC NULLS LAST, id DESC
		LIMIT 1`)).
		WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"session_id"}).AddRow("expired-session"))
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT encrypted_key, consumed_at
		FROM xiass_sms_card_keys
		WHERE session_id = $1 AND owner_user_id = $2 AND status = 'active'`)).
		WithArgs("expired-session", int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"encrypted_key", "consumed_at"}).
			AddRow("enc:STALE-CARD", time.Now().Add(-PixlabSMSSessionValidity-time.Minute)))
	expectPixlabSMSExpire(mock, "expired-session", 42, 11, false)
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT
			COUNT(*) FILTER (WHERE status = 'queued'),
			COUNT(*) FILTER (WHERE status = 'active' AND ($1 = 0 OR owner_user_id = $1))
		FROM xiass_sms_card_keys`)).
		WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"queued", "active"}).AddRow(4, 0))
	mock.ExpectQuery(claim).
		WithArgs(int64(42), sqlmock.AnyArg(), PixlabSMSCardKeyMaxClaims).
		WillReturnRows(sqlmock.NewRows([]string{"encrypted_key"}).AddRow("enc:NEXT-CARD"))
	expectPixlabSMSRedeemStartFallback(mock, 42)
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT
			COUNT(*) FILTER (WHERE status = 'queued'),
			COUNT(*) FILTER (WHERE status = 'active' AND ($1 = 0 OR owner_user_id = $1))
		FROM xiass_sms_card_keys`)).
		WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"queued", "active"}).AddRow(3, 1))
	expectPixlabSMSSessionExpiry(mock, sqlmock.AnyArg(), 42, time.Now())

	svc := newPixlabSMSService(db, pixlabSMSPrefixEncryptor{}, provider.Client(), provider.URL)
	result, err := svc.Redeem(context.Background(), 42)
	require.NoError(t, err)
	require.Equal(t, "WAITING", result.Status)
	require.Equal(t, "12025550126", result.Number)
	require.NotEqual(t, "expired-session", result.SessionID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPixlabSMSServiceRedeemReplacesRejectedAdminSessionAfterOwnerConflict(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/resume":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"success":false,"message":"session is no longer available"}`))
		case "/redeem":
			_, _ = w.Write([]byte(`{"success":true,"number":"12025550125","country":"美国","status":"WAITING"}`))
		default:
			t.Fatalf("unexpected provider path: %s", r.URL.Path)
		}
	}))
	t.Cleanup(provider.Close)

	claim := regexp.QuoteMeta(`
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
			RETURNING card.encrypted_key`)
	mock.ExpectQuery(claim).
		WithArgs(int64(42), sqlmock.AnyArg(), PixlabSMSCardKeyMaxClaims).
		WillReturnError(&pq.Error{Code: "23505", Constraint: "uq_xiass_sms_card_keys_active_owner"})
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT session_id
		FROM xiass_sms_card_keys
		WHERE owner_user_id = $1 AND status = 'active'
		ORDER BY consumed_at DESC NULLS LAST, id DESC
		LIMIT 1`)).
		WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"session_id"}).AddRow("rejected-session"))
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT encrypted_key, consumed_at
		FROM xiass_sms_card_keys
		WHERE session_id = $1 AND owner_user_id = $2 AND status = 'active'`)).
		WithArgs("rejected-session", int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"encrypted_key", "consumed_at"}).AddRow("enc:REJECTED-CARD", time.Now()))
	mock.ExpectExec(regexp.QuoteMeta(`
		UPDATE xiass_sms_card_keys
		SET status = CASE WHEN claim_count >= $3 THEN 'exhausted' ELSE 'queued' END,
			owner_user_id = NULL, session_id = NULL,
			consumed_at = NULL, updated_at = NOW()
		WHERE session_id = $1 AND owner_user_id = $2 AND status = 'active'`)).
		WithArgs("rejected-session", int64(42), PixlabSMSCardKeyMaxClaims).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(claim).
		WithArgs(int64(42), sqlmock.AnyArg(), PixlabSMSCardKeyMaxClaims).
		WillReturnRows(sqlmock.NewRows([]string{"encrypted_key"}).AddRow("enc:NEXT-CARD"))
	expectPixlabSMSRedeemStartFallback(mock, 42)
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT
			COUNT(*) FILTER (WHERE status = 'queued'),
			COUNT(*) FILTER (WHERE status = 'active' AND ($1 = 0 OR owner_user_id = $1))
		FROM xiass_sms_card_keys`)).
		WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"queued", "active"}).AddRow(3, 1))
	expectPixlabSMSSessionExpiry(mock, sqlmock.AnyArg(), 42, time.Now())

	svc := newPixlabSMSService(db, pixlabSMSPrefixEncryptor{}, provider.Client(), provider.URL)
	result, err := svc.Redeem(context.Background(), 42)
	require.NoError(t, err)
	require.NotEqual(t, "rejected-session", result.SessionID)
	require.Equal(t, "12025550125", result.Number)
	require.Equal(t, "WAITING", result.Status)
	require.Equal(t, int64(3), result.QueuedCount)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPixlabSMSServiceRedeemSkipsCardAtProviderUsageLimit(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	var receivedCardKeys []string
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]string
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		receivedCardKeys = append(receivedCardKeys, request["cardKey"])
		w.Header().Set("Content-Type", "application/json")
		if request["cardKey"] == "AT-LIMIT" {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"success":false,"message":"单卡连续换号上限（6次）触发熔断"}`))
			return
		}
		_, _ = w.Write([]byte(`{"success":true,"number":"12025550124","country":"美国","status":"RECEIVED","code":"654322"}`))
	}))
	t.Cleanup(provider.Close)

	claim := regexp.QuoteMeta(`
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
		RETURNING card.encrypted_key`)
	exhaust := regexp.QuoteMeta(`
		UPDATE xiass_sms_card_keys
		SET status = 'exhausted', owner_user_id = NULL, session_id = NULL,
			consumed_at = NULL, updated_at = NOW()
		WHERE session_id = $1 AND owner_user_id = $2 AND status = 'active'`)
	mock.ExpectQuery(claim).
		WithArgs(int64(42), sqlmock.AnyArg(), PixlabSMSCardKeyMaxClaims).
		WillReturnRows(sqlmock.NewRows([]string{"encrypted_key"}).AddRow("enc:AT-LIMIT"))
	mock.ExpectExec(exhaust).
		WithArgs(sqlmock.AnyArg(), int64(42)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(claim).
		WithArgs(int64(42), sqlmock.AnyArg(), PixlabSMSCardKeyMaxClaims).
		WillReturnRows(sqlmock.NewRows([]string{"encrypted_key"}).AddRow("enc:NEXT-CARD"))
	mock.ExpectExec(regexp.QuoteMeta(`
		DELETE FROM xiass_sms_card_keys
		WHERE session_id = $1 AND owner_user_id = $2 AND status = 'active'`)).
		WithArgs(sqlmock.AnyArg(), int64(42)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT
			COUNT(*) FILTER (WHERE status = 'queued'),
			COUNT(*) FILTER (WHERE status = 'active' AND ($1 = 0 OR owner_user_id = $1))
		FROM xiass_sms_card_keys`)).
		WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"queued", "active"}).AddRow(2, 0))

	svc := newPixlabSMSService(db, pixlabSMSPrefixEncryptor{}, provider.Client(), provider.URL)
	result, err := svc.Redeem(context.Background(), 42)
	require.NoError(t, err)
	require.Equal(t, []string{"AT-LIMIT", "NEXT-CARD"}, receivedCardKeys)
	require.Equal(t, "12025550124", result.Number)
	require.Equal(t, "654322", result.Code)
	require.Equal(t, int64(2), result.QueuedCount)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPixlabSMSServiceCheckKeepsActiveCardAfterProviderFailure(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/check", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":false,"message":"card already invalid"}`))
	}))
	t.Cleanup(provider.Close)

	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT encrypted_key, consumed_at
		FROM xiass_sms_card_keys
		WHERE session_id = $1 AND owner_user_id = $2 AND status = 'active'`)).
		WithArgs("session-1", int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"encrypted_key", "consumed_at"}).AddRow("enc:CONSUMED-CARD", time.Now()))
	svc := newPixlabSMSService(db, pixlabSMSPrefixEncryptor{}, provider.Client(), provider.URL)
	_, err = svc.Check(context.Background(), 42, "session-1")
	require.Error(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPixlabSMSServiceCheckRetiresCardAtProviderUsageLimit(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/check", r.URL.Path)
		_, _ = w.Write([]byte(`{"success":false,"message":"单卡连续换号上限（6次）触发熔断"}`))
	}))
	t.Cleanup(provider.Close)

	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT encrypted_key, consumed_at
		FROM xiass_sms_card_keys
		WHERE session_id = $1 AND owner_user_id = $2 AND status = 'active'`)).
		WithArgs("session-limit", int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"encrypted_key", "consumed_at"}).AddRow("enc:AT-LIMIT", time.Now()))
	mock.ExpectExec(regexp.QuoteMeta(`
		UPDATE xiass_sms_card_keys
		SET status = 'exhausted', owner_user_id = NULL, session_id = NULL,
			consumed_at = NULL, updated_at = NOW()
		WHERE session_id = $1 AND owner_user_id = $2 AND status = 'active'`)).
		WithArgs("session-limit", int64(42)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT
			COUNT(*) FILTER (WHERE status = 'queued'),
			COUNT(*) FILTER (WHERE status = 'active' AND ($1 = 0 OR owner_user_id = $1))
		FROM xiass_sms_card_keys`)).
		WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"queued", "active"}).AddRow(3, 0))

	svc := newPixlabSMSService(db, pixlabSMSPrefixEncryptor{}, provider.Client(), provider.URL)
	result, err := svc.Check(context.Background(), 42, "session-limit")
	require.NoError(t, err)
	require.Equal(t, "EXHAUSTED", result.Status)
	require.Empty(t, result.SessionID)
	require.Equal(t, int64(3), result.QueuedCount)
	encoded, marshalErr := json.Marshal(result)
	require.NoError(t, marshalErr)
	require.NotContains(t, string(encoded), "AT-LIMIT")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPixlabSMSServiceCancelRequeuesCardWithoutVerificationCode(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/cancel", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"status":"WAITING"}`))
	}))
	t.Cleanup(provider.Close)

	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT encrypted_key, consumed_at
		FROM xiass_sms_card_keys
		WHERE session_id = $1 AND owner_user_id = $2 AND status = 'active'`)).
		WithArgs("session-1", int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"encrypted_key", "consumed_at"}).AddRow("enc:REUSABLE-CARD", time.Now()))
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT
			COUNT(*) FILTER (WHERE status = 'queued'),
			COUNT(*) FILTER (WHERE status = 'active' AND ($1 = 0 OR owner_user_id = $1))
		FROM xiass_sms_card_keys`)).
		WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"queued", "active"}).AddRow(2, 1))
	expectPixlabSMSSessionExpiry(mock, "session-1", 42, time.Now())
	mock.ExpectExec(regexp.QuoteMeta(`
		UPDATE xiass_sms_card_keys
		SET status = CASE WHEN claim_count >= $3 THEN 'exhausted' ELSE 'queued' END,
			owner_user_id = NULL, session_id = NULL,
			consumed_at = NULL, updated_at = NOW()
		WHERE session_id = $1 AND owner_user_id = $2 AND status = 'active'`)).
		WithArgs("session-1", int64(42), PixlabSMSCardKeyMaxClaims).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT
			COUNT(*) FILTER (WHERE status = 'queued'),
			COUNT(*) FILTER (WHERE status = 'active' AND ($1 = 0 OR owner_user_id = $1))
		FROM xiass_sms_card_keys`)).
		WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"queued", "active"}).AddRow(3, 0))

	svc := newPixlabSMSService(db, pixlabSMSPrefixEncryptor{}, provider.Client(), provider.URL)
	result, err := svc.Cancel(context.Background(), 42, "session-1")
	require.NoError(t, err)
	require.Equal(t, "CANCELLED", result.Status)
	require.Empty(t, result.SessionID)
	require.Equal(t, int64(3), result.QueuedCount)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPixlabSMSServiceRedeemForMemberCapturesFeeOnlyAfterCode(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/redeem", r.URL.Path)
		_, _ = w.Write([]byte(`{"success":true,"number":"12025550123","country":"美国","status":"RECEIVED","code":"654321"}`))
	}))
	t.Cleanup(provider.Close)

	expectExpiredPixlabSMSSessions(mock, 7, noExpiredPixlabSMSSessions())
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`SELECT pg_advisory_xact_lock($1)`)).
		WithArgs(int64(7)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT EXISTS(
			SELECT 1 FROM xiass_sms_card_keys
			WHERE owner_user_id = $1 AND status = 'active'
		)`)).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT member_fee
		FROM xiass_sms_receiver_settings
		WHERE id = 1
		FOR UPDATE`)).
		WillReturnRows(sqlmock.NewRows([]string{"member_fee"}).AddRow(PixlabSMSMemberFee))
	mock.ExpectQuery(regexp.QuoteMeta(`
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
		RETURNING card.encrypted_key`)).
		WithArgs(int64(7), sqlmock.AnyArg(), PixlabSMSCardKeyMaxClaims).
		WillReturnRows(sqlmock.NewRows([]string{"encrypted_key"}).AddRow("enc:MEMBER-CARD"))
	mock.ExpectQuery(regexp.QuoteMeta(`
		UPDATE users
		SET balance = balance - $1, updated_at = NOW()
		WHERE id = $2 AND deleted_at IS NULL AND balance >= $1
		RETURNING balance`)).
		WithArgs(PixlabSMSMemberFee, int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow(8.0))
	mock.ExpectExec(regexp.QuoteMeta(`
		INSERT INTO xiass_sms_member_charges (session_id, user_id, amount, status)
		VALUES ($1, $2, $3, 'held')`)).
		WithArgs(sqlmock.AnyArg(), int64(7), PixlabSMSMemberFee).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	expectPixlabSMSSettlement(mock, 7, 21, true, pixlabSettlementCapture)
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT
			COUNT(*) FILTER (WHERE status = 'queued'),
			COUNT(*) FILTER (WHERE status = 'active' AND ($1 = 0 OR owner_user_id = $1))
		FROM xiass_sms_card_keys`)).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"queued", "active"}).AddRow(0, 0))
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT balance FROM users WHERE id = $1 AND deleted_at IS NULL`)).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow(8.0))
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT status, amount
		FROM xiass_sms_member_charges
		WHERE session_id = $1 AND user_id = $2`)).
		WithArgs(sqlmock.AnyArg(), int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"status", "amount"}).AddRow("captured", PixlabSMSMemberFee))

	svc := newPixlabSMSService(db, pixlabSMSPrefixEncryptor{}, provider.Client(), provider.URL)
	result, err := svc.RedeemForMember(context.Background(), 7)
	require.NoError(t, err)
	require.Equal(t, "654321", result.Code)
	require.Equal(t, "captured", result.ChargeState)
	require.Equal(t, PixlabSMSMemberFee, result.FeeAmount)
	require.Equal(t, 8.0, result.Balance)
	require.Empty(t, result.SessionID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPixlabSMSServiceRedeemForMemberRotatesLimitedCardWithoutDoubleCharge(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]string
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		if request["cardKey"] == "AT-LIMIT" {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"success":false,"error":"单卡连续换号上限（6次）触发熔断"}`))
			return
		}
		_, _ = w.Write([]byte(`{"success":true,"number":"12025550124","country":"美国","status":"RECEIVED","code":"654322"}`))
	}))
	t.Cleanup(provider.Close)

	claim := regexp.QuoteMeta(`
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
		RETURNING card.encrypted_key`)
	exhaust := regexp.QuoteMeta(`
		UPDATE xiass_sms_card_keys
		SET status = 'exhausted', owner_user_id = NULL, session_id = NULL,
			consumed_at = NULL, updated_at = NOW()
		WHERE session_id = $1 AND owner_user_id = $2 AND status = 'active'`)
	expectMemberClaim := func(card string) {
		mock.ExpectBegin()
		mock.ExpectExec(regexp.QuoteMeta(`SELECT pg_advisory_xact_lock($1)`)).
			WithArgs(int64(7)).
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectQuery(regexp.QuoteMeta(`
			SELECT EXISTS(
				SELECT 1 FROM xiass_sms_card_keys
				WHERE owner_user_id = $1 AND status = 'active'
			)`)).
			WithArgs(int64(7)).
			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
		mock.ExpectQuery(regexp.QuoteMeta(`
			SELECT member_fee
			FROM xiass_sms_receiver_settings
			WHERE id = 1
			FOR UPDATE`)).
			WillReturnRows(sqlmock.NewRows([]string{"member_fee"}).AddRow(PixlabSMSMemberFee))
		mock.ExpectQuery(claim).
			WithArgs(int64(7), sqlmock.AnyArg(), PixlabSMSCardKeyMaxClaims).
			WillReturnRows(sqlmock.NewRows([]string{"encrypted_key"}).AddRow("enc:" + card))
		mock.ExpectQuery(regexp.QuoteMeta(`
			UPDATE users
			SET balance = balance - $1, updated_at = NOW()
			WHERE id = $2 AND deleted_at IS NULL AND balance >= $1
			RETURNING balance`)).
			WithArgs(PixlabSMSMemberFee, int64(7)).
			WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow(8.0))
		mock.ExpectExec(regexp.QuoteMeta(`
			INSERT INTO xiass_sms_member_charges (session_id, user_id, amount, status)
			VALUES ($1, $2, $3, 'held')`)).
			WithArgs(sqlmock.AnyArg(), int64(7), PixlabSMSMemberFee).
			WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectCommit()
	}
	expectExpiredPixlabSMSSessions(mock, 7, noExpiredPixlabSMSSessions())
	expectMemberClaim("AT-LIMIT")
	_ = exhaust
	expectPixlabSMSSettlement(mock, 7, 22, true, pixlabSettlementExhaust)
	expectMemberClaim("NEXT-CARD")
	expectPixlabSMSSettlement(mock, 7, 23, true, pixlabSettlementCapture)
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT
			COUNT(*) FILTER (WHERE status = 'queued'),
			COUNT(*) FILTER (WHERE status = 'active' AND ($1 = 0 OR owner_user_id = $1))
		FROM xiass_sms_card_keys`)).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"queued", "active"}).AddRow(1, 0))
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT balance FROM users WHERE id = $1 AND deleted_at IS NULL`)).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow(8.0))
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT status, amount
		FROM xiass_sms_member_charges
		WHERE session_id = $1 AND user_id = $2`)).
		WithArgs(sqlmock.AnyArg(), int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"status", "amount"}).AddRow("captured", PixlabSMSMemberFee))

	svc := newPixlabSMSService(db, pixlabSMSPrefixEncryptor{}, provider.Client(), provider.URL)
	result, err := svc.RedeemForMember(context.Background(), 7)
	require.NoError(t, err)
	require.Equal(t, "captured", result.ChargeState)
	require.Equal(t, PixlabSMSMemberFee, result.FeeAmount)
	require.Equal(t, 8.0, result.Balance)
	require.Equal(t, "654322", result.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPixlabSMSServiceMemberCancelReleasesHeldFee(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Contains(t, []string{"/check", "/cancel"}, r.URL.Path)
		_, _ = w.Write([]byte(`{"success":true,"status":"WAITING"}`))
	}))
	t.Cleanup(provider.Close)

	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT consumed_at
		FROM xiass_sms_card_keys
		WHERE session_id = $1 AND owner_user_id = $2 AND status = 'active'`)).
		WithArgs("member-session", int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"consumed_at"}).AddRow(time.Now().Add(-PixlabSMSMemberMutationDelay)))

	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT encrypted_key, consumed_at
		FROM xiass_sms_card_keys
		WHERE session_id = $1 AND owner_user_id = $2 AND status = 'active'`)).
		WithArgs("member-session", int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"encrypted_key", "consumed_at"}).AddRow("enc:MEMBER-CARD", time.Now().Add(-PixlabSMSMemberMutationDelay)))
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT
			COUNT(*) FILTER (WHERE status = 'queued'),
			COUNT(*) FILTER (WHERE status = 'active' AND ($1 = 0 OR owner_user_id = $1))
		FROM xiass_sms_card_keys`)).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"queued", "active"}).AddRow(2, 1))
	expectPixlabSMSSessionExpiry(mock, "member-session", 7, time.Now().Add(-PixlabSMSMemberMutationDelay))
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT encrypted_key, consumed_at
		FROM xiass_sms_card_keys
		WHERE session_id = $1 AND owner_user_id = $2 AND status = 'active'`)).
		WithArgs("member-session", int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"encrypted_key", "consumed_at"}).AddRow("enc:MEMBER-CARD", time.Now().Add(-PixlabSMSMemberMutationDelay)))
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT
			COUNT(*) FILTER (WHERE status = 'queued'),
			COUNT(*) FILTER (WHERE status = 'active' AND ($1 = 0 OR owner_user_id = $1))
		FROM xiass_sms_card_keys`)).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"queued", "active"}).AddRow(2, 1))
	expectPixlabSMSSessionExpiry(mock, "member-session", 7, time.Now().Add(-PixlabSMSMemberMutationDelay))
	expectPixlabSMSSettlement(mock, 7, 24, true, pixlabSettlementRelease)
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT
			COUNT(*) FILTER (WHERE status = 'queued'),
			COUNT(*) FILTER (WHERE status = 'active' AND ($1 = 0 OR owner_user_id = $1))
		FROM xiass_sms_card_keys`)).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"queued", "active"}).AddRow(3, 0))
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT balance FROM users WHERE id = $1 AND deleted_at IS NULL`)).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow(10.0))
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT status, amount
		FROM xiass_sms_member_charges
		WHERE session_id = $1 AND user_id = $2`)).
		WithArgs("member-session", int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"status", "amount"}).AddRow("released", PixlabSMSMemberFee))

	svc := newPixlabSMSService(db, pixlabSMSPrefixEncryptor{}, provider.Client(), provider.URL)
	result, err := svc.CancelForMember(context.Background(), 7, "member-session")
	require.NoError(t, err)
	require.Equal(t, "released", result.ChargeState)
	require.Equal(t, 10.0, result.Balance)
	require.Equal(t, "CANCELLED", result.Status)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestIsPixlabCardUsageLimitError(t *testing.T) {
	require.True(t, isPixlabCardUsageLimitError(infraerrors.BadRequest("SMS_PROVIDER_REJECTED", "单卡连续换号上限（6 次）触发熔断")))
	require.True(t, isPixlabCardUsageLimitError(infraerrors.BadRequest("SMS_PROVIDER_REJECTED", "连续换号上限")))
	require.True(t, isPixlabCardUsageLimitMessage("单卡连续换号上限（6次）触发熔断"))
	require.False(t, isPixlabCardUsageLimitError(infraerrors.BadRequest("SMS_PROVIDER_REJECTED", "号码暂时不足，请稍后重试")))
	require.False(t, isPixlabCardUsageLimitError(infraerrors.ServiceUnavailable("SMS_PROVIDER_UNAVAILABLE", "接码服务暂时无法连接")))
}

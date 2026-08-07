package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

type pixlabSMSPrefixEncryptor struct{}

func (pixlabSMSPrefixEncryptor) Encrypt(value string) (string, error) { return "enc:" + value, nil }
func (pixlabSMSPrefixEncryptor) Decrypt(value string) (string, error) {
	return value[len("enc:"):], nil
}

func TestPixlabSMSServiceAddCardKeysEncryptsAndDeduplicates(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	insert := regexp.QuoteMeta(`
			INSERT INTO xiass_sms_card_keys (encrypted_key, key_fingerprint)
			VALUES ($1, $2)
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
		RETURNING card.encrypted_key`)
	mock.ExpectQuery(claim).
		WithArgs(int64(42), sqlmock.AnyArg()).
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
		SELECT encrypted_key
		FROM xiass_sms_card_keys
		WHERE session_id = $1 AND owner_user_id = $2 AND status = 'active'`)).
		WithArgs("session-1", int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"encrypted_key"}).AddRow("enc:CONSUMED-CARD"))
	svc := newPixlabSMSService(db, pixlabSMSPrefixEncryptor{}, provider.Client(), provider.URL)
	_, err = svc.Check(context.Background(), 42, "session-1")
	require.Error(t, err)
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
		SELECT encrypted_key
		FROM xiass_sms_card_keys
		WHERE session_id = $1 AND owner_user_id = $2 AND status = 'active'`)).
		WithArgs("session-1", int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"encrypted_key"}).AddRow("enc:REUSABLE-CARD"))
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT
			COUNT(*) FILTER (WHERE status = 'queued'),
			COUNT(*) FILTER (WHERE status = 'active' AND ($1 = 0 OR owner_user_id = $1))
		FROM xiass_sms_card_keys`)).
		WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"queued", "active"}).AddRow(2, 1))
	mock.ExpectExec(regexp.QuoteMeta(`
		UPDATE xiass_sms_card_keys
		SET status = 'queued', owner_user_id = NULL, session_id = NULL,
			consumed_at = NULL, updated_at = NOW()
		WHERE session_id = $1 AND owner_user_id = $2 AND status = 'active'`)).
		WithArgs("session-1", int64(42)).
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
		RETURNING card.encrypted_key`)).
		WithArgs(int64(7), sqlmock.AnyArg()).
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
	mock.ExpectExec(regexp.QuoteMeta(`
		UPDATE xiass_sms_member_charges
		SET status = 'captured', captured_at = NOW(), updated_at = NOW()
		WHERE session_id = $1 AND user_id = $2 AND status = 'held'`)).
		WithArgs(sqlmock.AnyArg(), int64(7)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta(`
		DELETE FROM xiass_sms_card_keys
		WHERE session_id = $1 AND owner_user_id = $2 AND status = 'active'`)).
		WithArgs(sqlmock.AnyArg(), int64(7)).
		WillReturnResult(sqlmock.NewResult(0, 1))
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
		SELECT encrypted_key
		FROM xiass_sms_card_keys
		WHERE session_id = $1 AND owner_user_id = $2 AND status = 'active'`)).
		WithArgs("member-session", int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"encrypted_key"}).AddRow("enc:MEMBER-CARD"))
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT
			COUNT(*) FILTER (WHERE status = 'queued'),
			COUNT(*) FILTER (WHERE status = 'active' AND ($1 = 0 OR owner_user_id = $1))
		FROM xiass_sms_card_keys`)).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"queued", "active"}).AddRow(2, 1))
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT encrypted_key
		FROM xiass_sms_card_keys
		WHERE session_id = $1 AND owner_user_id = $2 AND status = 'active'`)).
		WithArgs("member-session", int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"encrypted_key"}).AddRow("enc:MEMBER-CARD"))
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT
			COUNT(*) FILTER (WHERE status = 'queued'),
			COUNT(*) FILTER (WHERE status = 'active' AND ($1 = 0 OR owner_user_id = $1))
		FROM xiass_sms_card_keys`)).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"queued", "active"}).AddRow(2, 1))
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT amount
		FROM xiass_sms_member_charges
		WHERE session_id = $1 AND user_id = $2 AND status = 'held'
		FOR UPDATE`)).
		WithArgs("member-session", int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"amount"}).AddRow(PixlabSMSMemberFee))
	mock.ExpectQuery(regexp.QuoteMeta(`
		UPDATE users
		SET balance = balance + $1, updated_at = NOW()
		WHERE id = $2 AND deleted_at IS NULL
		RETURNING balance`)).
		WithArgs(PixlabSMSMemberFee, int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow(10.0))
	mock.ExpectExec(regexp.QuoteMeta(`
		UPDATE xiass_sms_member_charges
		SET status = 'released', released_at = NOW(), updated_at = NOW()
		WHERE session_id = $1 AND user_id = $2 AND status = 'held'`)).
		WithArgs("member-session", int64(7)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	mock.ExpectExec(regexp.QuoteMeta(`
		UPDATE xiass_sms_card_keys
		SET status = 'queued', owner_user_id = NULL, session_id = NULL,
			consumed_at = NULL, updated_at = NOW()
		WHERE session_id = $1 AND owner_user_id = $2 AND status = 'active'`)).
		WithArgs("member-session", int64(7)).
		WillReturnResult(sqlmock.NewResult(0, 1))
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

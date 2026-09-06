package repository

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestPersistentRefreshTokenStoreRejectsInvalidMetadataWithoutSQL(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	s := NewPersistentRefreshTokenStore(db)
	now := time.Now().UTC()
	valid := service.RefreshTokenData{
		Issuance: &service.RefreshTokenIssuance{ID: "7a7aa320-7244-4526-8868-9c9fdf6b474e", UserID: 1},
		UserID:   1, FamilyID: strings.Repeat("a", 32), CreatedAt: now,
		ExpiresAt: now.Add(time.Hour), FamilyExpiresAt: now.Add(time.Hour),
	}
	for _, tc := range []struct {
		name string
		edit func(*service.RefreshTokenData)
	}{
		{"missing-ticket", func(d *service.RefreshTokenData) { d.Issuance = nil }},
		{"user", func(d *service.RefreshTokenData) { d.UserID = 0 }},
		{"family", func(d *service.RefreshTokenData) { d.FamilyID = "raw-secret-not-a-family" }},
		{"binding", func(d *service.RefreshTokenData) { d.BindingHash = "raw-browser-data" }},
		{"created", func(d *service.RefreshTokenData) { d.CreatedAt = time.Time{} }},
		{"expires", func(d *service.RefreshTokenData) { d.ExpiresAt = time.Time{} }},
		{"no-legacy-inference", func(d *service.RefreshTokenData) { d.FamilyExpiresAt = time.Time{} }},
		{"invalid-window", func(d *service.RefreshTokenData) { d.ExpiresAt = now.Add(-time.Second) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := valid
			tc.edit(&d)
			require.ErrorIs(t, s.StoreRefreshToken(context.Background(), strings.Repeat("b", 64), &d, time.Hour), ErrPersistentRefreshTokenMetadata)
		})
	}
	require.ErrorIs(t, s.StoreRefreshToken(context.Background(), strings.Repeat("b", 64), nil, time.Hour), ErrPersistentRefreshTokenMetadata)
	for _, hash := range []string{"", "rt_raw-refresh-credential", strings.Repeat("A", 64), strings.Repeat("b", 63)} {
		require.ErrorIs(t, s.StoreRefreshToken(context.Background(), hash, &valid, time.Hour), ErrPersistentRefreshTokenMetadata)
		got, err := s.GetRefreshToken(context.Background(), hash)
		require.Nil(t, got)
		require.ErrorIs(t, err, ErrPersistentRefreshTokenMetadata)
		got, err = s.ConsumeRefreshToken(context.Background(), hash)
		require.Nil(t, got)
		require.ErrorIs(t, err, ErrPersistentRefreshTokenMetadata)
		require.ErrorIs(t, s.DeleteRefreshToken(context.Background(), hash), ErrPersistentRefreshTokenMetadata)
	}
	for _, ttl := range []time.Duration{0, -time.Second, time.Nanosecond} {
		require.ErrorIs(t, s.StoreRefreshToken(context.Background(), strings.Repeat("b", 64), &valid, ttl), ErrPersistentRefreshTokenMetadata)
	}
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPersistentRefreshTokenStoreAcknowledgesOnlyCommittedRevocation(t *testing.T) {
	for _, scope := range []string{"hash", "family", "user", "global"} {
		t.Run(scope, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			s := NewPersistentRefreshTokenStore(db)
			mock.ExpectBegin()
			mock.ExpectExec("SET LOCAL synchronous_commit = on").WillReturnResult(sqlmock.NewResult(0, 0))
			mock.ExpectQuery("SELECT generation FROM refresh_token_revocation_state").
				WillReturnRows(sqlmock.NewRows([]string{"generation"}).AddRow(0))
			var revoke func() error
			switch scope {
			case "hash":
				mock.ExpectExec("INSERT INTO refresh_tokens").WillReturnResult(sqlmock.NewResult(0, 1))
				revoke = func() error { return s.DeleteRefreshToken(context.Background(), strings.Repeat("a", 64)) }
			case "family":
				mock.ExpectExec("INSERT INTO refresh_token_families").WillReturnResult(sqlmock.NewResult(0, 1))
				revoke = func() error { return s.DeleteTokenFamily(context.Background(), strings.Repeat("a", 32)) }
			case "user":
				mock.ExpectExec("INSERT INTO refresh_token_users").WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectQuery("SELECT generation FROM refresh_token_users").
					WillReturnRows(sqlmock.NewRows([]string{"generation"}).AddRow(0))
				mock.ExpectExec("UPDATE refresh_token_users").WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectExec("UPDATE refresh_token_families").WillReturnResult(sqlmock.NewResult(0, 1))
				revoke = func() error { return s.DeleteUserRefreshTokens(context.Background(), 1) }
			case "global":
				mock.ExpectExec("UPDATE refresh_token_revocation_state").WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectExec("UPDATE refresh_token_families").WillReturnResult(sqlmock.NewResult(0, 1))
				revoke = func() error { return s.DeleteAllRefreshTokens(context.Background()) }
			}
			commitErr := errors.New("commit acknowledgment lost")
			mock.ExpectCommit().WillReturnError(commitErr)
			require.ErrorIs(t, revoke(), commitErr)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestPersistentRefreshTokenStoreConsumeNeverReturnsUncommittedMetadata(t *testing.T) {
	for _, tc := range []struct {
		name     string
		affected int64
	}{{"no rows updated", 0}, {"commit failure", 1}} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			s := NewPersistentRefreshTokenStore(db)
			now := time.Now().UTC()
			rows := func() *sqlmock.Rows {
				return sqlmock.NewRows([]string{"user_id", "token_version", "family_id", "binding_hash", "created_at", "expires_at", "family_expires_at"}).
					AddRow(1, -32, strings.Repeat("a", 32), "", now, now.Add(time.Hour), now.Add(time.Hour))
			}
			mock.ExpectBegin()
			mock.ExpectExec("SET LOCAL synchronous_commit = on").WillReturnResult(sqlmock.NewResult(0, 0))
			mock.ExpectQuery("SELECT generation FROM refresh_token_revocation_state").
				WillReturnRows(sqlmock.NewRows([]string{"generation"}).AddRow(0))
			mock.ExpectQuery("SELECT t.user_id, t.token_version").WillReturnRows(rows())
			mock.ExpectExec("INSERT INTO refresh_token_users").WillReturnResult(sqlmock.NewResult(0, 0))
			mock.ExpectQuery("SELECT generation FROM refresh_token_users").
				WillReturnRows(sqlmock.NewRows([]string{"generation"}).AddRow(0))
			mock.ExpectQuery("SELECT family_id FROM refresh_token_families").
				WillReturnRows(sqlmock.NewRows([]string{"family_id"}).AddRow(strings.Repeat("a", 32)))
			mock.ExpectQuery("SELECT t.user_id, t.token_version").WillReturnRows(rows())
			mock.ExpectExec("UPDATE refresh_tokens SET consumed_at").WillReturnResult(sqlmock.NewResult(0, tc.affected))
			wantErr := service.ErrRefreshTokenNotFound
			if tc.affected == 0 {
				mock.ExpectRollback()
			} else {
				wantErr = errors.New("ambiguous commit")
				mock.ExpectCommit().WillReturnError(wantErr)
			}
			data, err := s.ConsumeRefreshToken(context.Background(), strings.Repeat("b", 64))
			require.Nil(t, data)
			require.ErrorIs(t, err, wantErr)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestPersistentRefreshTokenIssuanceIsNotSerialized(t *testing.T) {
	data := service.RefreshTokenData{UserID: 1, Issuance: &service.RefreshTokenIssuance{
		ID: "7a7aa320-7244-4526-8868-9c9fdf6b474e", UserID: 1, UserGeneration: 2, GlobalGeneration: 3,
	}}
	encoded, err := json.Marshal(data)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), data.Issuance.ID)
	require.NotContains(t, string(encoded), "Generation")
	var decoded service.RefreshTokenData
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	require.Nil(t, decoded.Issuance, "Redis/legacy JSON cannot carry an admission ticket")
	require.Equal(t, data.UserID, decoded.UserID)
}

func TestPersistentRefreshTokenPrepareCommitFailureReturnsNoTicket(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL synchronous_commit = on").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT generation FROM refresh_token_revocation_state").
		WillReturnRows(sqlmock.NewRows([]string{"generation"}).AddRow(3))
	mock.ExpectExec("INSERT INTO refresh_token_users").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT generation FROM refresh_token_users").
		WillReturnRows(sqlmock.NewRows([]string{"generation"}).AddRow(4))
	mock.ExpectQuery("INSERT INTO refresh_token_issuances").
		WillReturnRows(sqlmock.NewRows([]string{"ticket_id"}).AddRow("7a7aa320-7244-4526-8868-9c9fdf6b474e"))
	commitErr := errors.New("lost prepare commit acknowledgment")
	mock.ExpectCommit().WillReturnError(commitErr)
	ticket, err := NewPersistentRefreshTokenStore(db).PrepareRefreshTokenIssuance(context.Background(), 1)
	require.Nil(t, ticket)
	require.ErrorIs(t, err, commitErr)
	require.NoError(t, mock.ExpectationsWereMet())
}

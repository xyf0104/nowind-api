//go:build integration

package repository

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestPersistentRefreshIssuanceRevocationInvalidatesFutureDatedStore(t *testing.T) {
	ctx := context.Background()
	s := NewPersistentRefreshTokenStore(integrationDB)
	for _, scope := range []string{"user", "global", "family"} {
		t.Run(scope, func(t *testing.T) {
			hash, data := persistentRefreshTestData(t, 0)
			oldTicket := *data.Issuance
			// Model a fast app clock and a delayed Store. Neither this timestamp
			// nor replacing it after revocation can repair the old admission.
			data.CreatedAt = data.CreatedAt.Add(24 * time.Hour)
			data.ExpiresAt = data.CreatedAt.Add(7 * 24 * time.Hour)
			data.FamilyExpiresAt = data.ExpiresAt
			switch scope {
			case "user":
				require.NoError(t, s.DeleteUserRefreshTokens(ctx, data.UserID))
			case "global":
				require.NoError(t, s.DeleteAllRefreshTokens(ctx))
			case "family":
				require.NoError(t, s.DeleteTokenFamily(ctx, data.FamilyID))
			}
			require.ErrorIs(t, s.StoreRefreshToken(ctx, hash, data, time.Hour), ErrPersistentRefreshTokenRejected)
			require.NoError(t, integrationDB.QueryRow(`SELECT clock_timestamp()`).Scan(&data.CreatedAt))
			require.ErrorIs(t, s.StoreRefreshToken(ctx, hash, data, time.Hour), ErrPersistentRefreshTokenRejected)
			assertPersistentRefreshMissing(t, s, hash)

			newHash, independent := persistentRefreshTestData(t, data.UserID)
			require.NotEqual(t, oldTicket.ID, independent.Issuance.ID)
			if scope == "user" {
				require.Equal(t, oldTicket.UserGeneration+1, independent.Issuance.UserGeneration)
				require.Equal(t, oldTicket.GlobalGeneration, independent.Issuance.GlobalGeneration)
			} else if scope == "global" {
				require.Equal(t, oldTicket.GlobalGeneration+1, independent.Issuance.GlobalGeneration)
				require.Equal(t, oldTicket.UserGeneration, independent.Issuance.UserGeneration)
			}
			// Even forging current counter values cannot turn an old DB ticket
			// into a fresh one: the persisted snapshot must also match.
			forged := *independent.Issuance
			forged.ID = oldTicket.ID
			data.Issuance = &forged
			require.ErrorIs(t, s.StoreRefreshToken(ctx, hash, data, time.Hour), ErrPersistentRefreshTokenRejected)
			if scope == "family" {
				candidate := *independent
				candidate.FamilyID, candidate.FamilyExpiresAt = data.FamilyID, data.FamilyExpiresAt
				require.ErrorIs(t, s.StoreRefreshToken(ctx, newHash, &candidate, time.Hour), ErrPersistentRefreshTokenRejected,
					"fresh admission cannot revive a revoked family")
			}
			// New independent login remains valid even when its metadata clock
			// is ahead. Only the DB-issued admission governs revocation safety.
			independent.CreatedAt = independent.CreatedAt.Add(time.Hour)
			require.NoError(t, s.StoreRefreshToken(ctx, newHash, independent, time.Hour))
			_, err := s.GetRefreshToken(ctx, newHash)
			require.NoError(t, err)
		})
	}
}

func TestPersistentRefreshIssuancePrepareRevocationRace(t *testing.T) {
	ctx := context.Background()
	s := NewPersistentRefreshTokenStore(integrationDB)
	for _, scope := range []string{"user", "global"} {
		t.Run(scope, func(t *testing.T) {
			for iteration := 0; iteration < 16; iteration++ {
				hash, data := persistentRefreshTestData(t, 0)
				start := make(chan struct{})
				prepared := make(chan *service.RefreshTokenIssuance, 1)
				prepErr, revokeErr := make(chan error, 1), make(chan error, 1)
				go func() {
					<-start
					ticket, err := s.PrepareRefreshTokenIssuance(ctx, data.UserID)
					prepared <- ticket
					prepErr <- err
				}()
				go func() {
					<-start
					if scope == "user" {
						revokeErr <- s.DeleteUserRefreshTokens(ctx, data.UserID)
					} else {
						revokeErr <- s.DeleteAllRefreshTokens(ctx)
					}
				}()
				close(start)
				ticket := <-prepared
				require.NoError(t, <-prepErr)
				require.NoError(t, <-revokeErr)
				fresh, err := s.PrepareRefreshTokenIssuance(ctx, data.UserID)
				require.NoError(t, err)
				data.Issuance = ticket
				data.CreatedAt = data.CreatedAt.Add(time.Hour)
				err = s.StoreRefreshToken(ctx, hash, data, time.Hour)
				if ticket.UserGeneration == fresh.UserGeneration && ticket.GlobalGeneration == fresh.GlobalGeneration {
					require.NoError(t, err, "prepare linearized after revocation")
				} else {
					require.ErrorIs(t, err, ErrPersistentRefreshTokenRejected, "prepare linearized before revocation")
					assertPersistentRefreshMissing(t, s, hash)
				}
			}
		})
	}
}

func TestPersistentRefreshIssuanceSingleUseRace(t *testing.T) {
	ctx := context.Background()
	s := NewPersistentRefreshTokenStore(integrationDB)
	aHash, a := persistentRefreshTestData(t, 0)
	bHash, b := persistentRefreshTestData(t, a.UserID)
	b.Issuance = a.Issuance
	start := make(chan struct{})
	results := make(chan error, 2)
	go func() { <-start; results <- s.StoreRefreshToken(ctx, aHash, a, time.Hour) }()
	go func() { <-start; results <- s.StoreRefreshToken(ctx, bHash, b, time.Hour) }()
	close(start)
	wins := 0
	for i := 0; i < 2; i++ {
		if err := <-results; err == nil {
			wins++
		} else {
			require.ErrorIs(t, err, ErrPersistentRefreshTokenRejected)
		}
	}
	require.Equal(t, 1, wins)
	var count int
	require.NoError(t, integrationDB.QueryRow(`SELECT count(*) FROM refresh_tokens WHERE issuance_id = $1`, a.Issuance.ID).Scan(&count))
	require.Equal(t, 1, count)
}

func TestPersistentRefreshIssuanceRejectsMissingForgedExpiredAndCrossUserTickets(t *testing.T) {
	ctx := context.Background()
	s := NewPersistentRefreshTokenStore(integrationDB)
	hash, data := persistentRefreshTestData(t, 0)
	ticket := data.Issuance
	data.Issuance = nil
	require.ErrorIs(t, s.StoreRefreshToken(ctx, hash, data, time.Hour), ErrPersistentRefreshTokenMetadata)
	data.Issuance = &service.RefreshTokenIssuance{ID: uuid.NewString(), UserID: data.UserID,
		UserGeneration: ticket.UserGeneration, GlobalGeneration: ticket.GlobalGeneration}
	require.ErrorIs(t, s.StoreRefreshToken(ctx, hash, data, time.Hour), ErrPersistentRefreshTokenRejected)
	_, other := persistentRefreshTestData(t, 0)
	forged := *other.Issuance
	forged.UserID = data.UserID
	data.Issuance = &forged
	require.ErrorIs(t, s.StoreRefreshToken(ctx, hash, data, time.Hour), ErrPersistentRefreshTokenRejected)
	data.Issuance = ticket
	_, err := integrationDB.Exec(`UPDATE refresh_token_issuances SET prepared_at = clock_timestamp() - INTERVAL '10 minutes',
		expires_at = clock_timestamp() - INTERVAL '1 minute' WHERE ticket_id = $1`, ticket.ID)
	require.NoError(t, err)
	require.ErrorIs(t, s.StoreRefreshToken(ctx, hash, data, time.Hour), ErrPersistentRefreshTokenRejected)
	var count int
	require.NoError(t, integrationDB.QueryRow(`SELECT count(*) FROM refresh_token_issuances WHERE ticket_id = $1`, ticket.ID).Scan(&count))
	require.Equal(t, 1, count, "expired tickets are not silently deleted or recreated")
	data.Issuance, err = s.PrepareRefreshTokenIssuance(ctx, data.UserID)
	require.NoError(t, err)
	require.NoError(t, s.StoreRefreshToken(ctx, hash, data, time.Hour))
	_, err = s.ConsumeRefreshToken(ctx, hash)
	require.NoError(t, err)
	data.Issuance, err = s.PrepareRefreshTokenIssuance(ctx, data.UserID)
	require.NoError(t, err)
	require.ErrorIs(t, s.StoreRefreshToken(ctx, hash, data, time.Hour), ErrPersistentRefreshTokenRejected,
		"even a fresh valid admission cannot resurrect a consumed hash")
	require.NoError(t, s.DeleteRefreshToken(ctx, hash))
	data.Issuance, err = s.PrepareRefreshTokenIssuance(ctx, data.UserID)
	require.NoError(t, err)
	require.ErrorIs(t, s.StoreRefreshToken(ctx, hash, data, time.Hour), ErrPersistentRefreshTokenRejected,
		"even a fresh valid admission cannot resurrect a revoked hash")
}

func TestPersistentRefreshIssuanceCommitRollback(t *testing.T) {
	ctx := context.Background()
	s := NewPersistentRefreshTokenStore(integrationDB)
	hash, data := persistentRefreshTestData(t, 0)
	cleanup := persistentRefreshFailCommit(t, "refresh_token_issuances", fmt.Sprintf("NEW.user_id = %d", data.UserID))
	ticket, err := s.PrepareRefreshTokenIssuance(ctx, data.UserID)
	require.ErrorContains(t, err, "commit refresh token transaction")
	require.Nil(t, ticket)
	// Failure of the ticket-consumption write must also roll back the token and
	// family writes, not just failure of the token write tested elsewhere.
	require.ErrorContains(t, s.StoreRefreshToken(ctx, hash, data, time.Hour), "commit refresh token transaction")
	assertPersistentRefreshMissing(t, s, hash)
	var used sql.NullTime
	require.NoError(t, integrationDB.QueryRow(`SELECT used_at FROM refresh_token_issuances WHERE ticket_id = $1`, data.Issuance.ID).Scan(&used))
	require.False(t, used.Valid)
	var count int
	require.NoError(t, integrationDB.QueryRow(`SELECT count(*) FROM refresh_token_issuances WHERE user_id = $1`, data.UserID).Scan(&count))
	require.Equal(t, 1, count, "failed preparation leaves no extra ticket")
	cleanup()
	require.NoError(t, s.StoreRefreshToken(ctx, hash, data, time.Hour), "fully rolled-back Store can retry the same ticket")
}

func TestPersistentRefreshIssuanceAuthServiceRequiresCommittedPreparation(t *testing.T) {
	ctx := context.Background()
	user := mustCreateUser(t, testEntClient(t), &service.User{})
	cleanup := persistentRefreshFailCommit(t, "refresh_token_issuances", fmt.Sprintf("NEW.user_id = %d", user.ID))
	auth := service.NewAuthService(testEntClient(t), NewUserRepository(testEntClient(t), integrationDB), nil,
		NewPersistentRefreshTokenStore(integrationDB), &config.Config{JWT: config.JWTConfig{
			Secret: "prepare-rollback-test-only-signing-secret", ExpireHour: 168, RefreshTokenExpireDays: 7,
		}}, nil, nil, nil, nil, nil, nil, nil, nil)
	pair, err := auth.GenerateTokenPair(ctx, user, "")
	require.Nil(t, pair)
	require.ErrorContains(t, err, "commit refresh token transaction")
	var count int
	require.NoError(t, integrationDB.QueryRow(`SELECT count(*) FROM refresh_tokens WHERE user_id = $1`, user.ID).Scan(&count))
	require.Zero(t, count)
	cleanup()
	pair, err = auth.GenerateTokenPair(ctx, user, "")
	require.NoError(t, err, "the real parent-owned AuthService hook prepares before generation")
	require.NotEmpty(t, pair.RefreshToken)
}

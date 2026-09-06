//go:build unit

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type authCallerRevocationStore struct {
	RefreshTokenCache
	err     error
	userIDs []int64
}

func (s *authCallerRevocationStore) DeleteUserRefreshTokens(_ context.Context, userID int64) error {
	s.userIDs = append(s.userIDs, userID)
	return s.err
}

type authCallerAdmissionStore struct {
	*authCallerRevocationStore
}

type authCallerPolicyStore struct {
	RefreshTokenCache
}

func (*authCallerPolicyStore) RequiresRefreshTokenIssuanceAdmission() bool { return true }

func (*authCallerAdmissionStore) PrepareRefreshTokenIssuance(context.Context, int64) (*RefreshTokenIssuance, error) {
	panic("the admission accessor must not prepare credentials")
}

type authCallerUserRepo struct {
	UserRepository
	user *User
	err  error
}

func (r *authCallerUserRepo) GetByID(context.Context, int64) (*User, error) {
	if r.err != nil {
		return nil, r.err
	}
	user := *r.user
	return &user, nil
}

func TestAuthServiceRequiresRefreshTokenIssuanceAdmission(t *testing.T) {
	for _, tc := range []struct {
		name string
		svc  *AuthService
		want bool
	}{
		{name: "nil service"},
		{name: "unconfigured legacy", svc: &AuthService{}},
		{name: "legacy store", svc: &AuthService{refreshTokenCache: &authCallerRevocationStore{}}},
		{name: "admission store", svc: &AuthService{refreshTokenCache: &authCallerAdmissionStore{}}, want: true},
		{name: "authority guarded legacy", svc: &AuthService{refreshTokenCache: &authCallerPolicyStore{}}, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, tc.svc.RequiresRefreshTokenIssuanceAdmission())
		})
	}
}

func TestAuthServiceRevokeAllUserTokensPropagatesStoreFailure(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{name: "committed"},
		{name: "commit failure", err: errors.New("synthetic commit failure")},
		{name: "canceled", err: context.Canceled},
		{name: "deadline", err: context.DeadlineExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			user := &User{ID: 17, Email: "synthetic@example.invalid", PasswordHash: "unchanged", TokenVersion: 7}
			before := *user
			store := &authCallerRevocationStore{err: tc.err}
			svc := &AuthService{userRepo: &authCallerUserRepo{user: user}, refreshTokenCache: store}
			err := svc.RevokeAllUserTokens(context.Background(), user.ID)
			if tc.err == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, tc.err)
			}
			require.Equal(t, []int64{user.ID}, store.userIDs)
			require.Equal(t, before, *user, "revocation must not rewrite user credentials or token version")
		})
	}
}

func TestAuthServiceRevokeAllUserTokensLookupAndLegacyNoStore(t *testing.T) {
	lookupErr := errors.New("synthetic lookup failure")
	store := &authCallerRevocationStore{}
	svc := &AuthService{userRepo: &authCallerUserRepo{err: lookupErr}, refreshTokenCache: store}
	require.ErrorIs(t, svc.RevokeAllUserTokens(context.Background(), 17), lookupErr)
	require.Empty(t, store.userIDs)

	svc = &AuthService{userRepo: &authCallerUserRepo{user: &User{ID: 17}}}
	require.NoError(t, svc.RevokeAllUserTokens(context.Background(), 17))
}

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type userGroupAccountAllowlistRepositoryStub struct {
	accountIDs []int64
	restricted bool
	getErr     error
	replaceErr error

	replacedUserID     int64
	replacedGroupID    int64
	replacedAccountIDs []int64
}

func (r *userGroupAccountAllowlistRepositoryStub) GetAllowedAccountIDs(context.Context, int64, int64) ([]int64, bool, error) {
	return append([]int64(nil), r.accountIDs...), r.restricted, r.getErr
}

func (r *userGroupAccountAllowlistRepositoryStub) ReplaceAllowedAccountIDs(_ context.Context, userID, groupID int64, accountIDs []int64) error {
	r.replacedUserID = userID
	r.replacedGroupID = groupID
	r.replacedAccountIDs = append([]int64(nil), accountIDs...)
	return r.replaceErr
}

type userGroupAccountAllowlistCandidateRepositoryStub struct {
	accounts []Account
	err      error
}

func (r *userGroupAccountAllowlistCandidateRepositoryStub) ListSchedulableByGroupID(context.Context, int64) ([]Account, error) {
	return append([]Account(nil), r.accounts...), r.err
}

func TestUserGroupAccountAllowlistServiceReplaceNormalizesAndValidatesCandidates(t *testing.T) {
	repo := &userGroupAccountAllowlistRepositoryStub{}
	candidates := &userGroupAccountAllowlistCandidateRepositoryStub{accounts: []Account{
		{ID: 12, Name: "first", Status: StatusActive, Schedulable: true},
		{ID: 7, Name: "second", Status: StatusActive, Schedulable: true},
	}}
	svc := NewUserGroupAccountAllowlistService(repo, candidates)

	err := svc.ReplaceAllowedAccountIDs(context.Background(), 41, 9, []int64{12, 7, 12})
	require.NoError(t, err)
	require.Equal(t, int64(41), repo.replacedUserID)
	require.Equal(t, int64(9), repo.replacedGroupID)
	require.Equal(t, []int64{7, 12}, repo.replacedAccountIDs)

	err = svc.ReplaceAllowedAccountIDs(context.Background(), 41, 9, []int64{99})
	require.ErrorIs(t, err, ErrUserGroupAccountAllowlistUnavailable)
	require.Equal(t, []int64{7, 12}, repo.replacedAccountIDs)
}

func TestUserGroupAccountAllowlistServiceReplaceEmptyRestoresUnrestricted(t *testing.T) {
	repo := &userGroupAccountAllowlistRepositoryStub{}
	svc := NewUserGroupAccountAllowlistService(repo, nil)

	err := svc.ReplaceAllowedAccountIDs(context.Background(), 41, 9, []int64{})
	require.NoError(t, err)
	require.Empty(t, repo.replacedAccountIDs)
}

func TestUserGroupAccountAllowlistServiceRejectsInvalidIDsBeforeCandidateLookup(t *testing.T) {
	repo := &userGroupAccountAllowlistRepositoryStub{}
	candidates := &userGroupAccountAllowlistCandidateRepositoryStub{err: errors.New("should not query candidates")}
	svc := NewUserGroupAccountAllowlistService(repo, candidates)

	err := svc.ReplaceAllowedAccountIDs(context.Background(), 41, 9, []int64{0})
	require.ErrorIs(t, err, ErrUserGroupAccountAllowlistInvalidID)
	require.Empty(t, repo.replacedAccountIDs)
}

func TestUserGroupAccountAllowlistServiceGetAdminSelectionKeepsStoredIDsAndFiltersCandidates(t *testing.T) {
	repo := &userGroupAccountAllowlistRepositoryStub{
		accountIDs: []int64{5, 2, 5},
		restricted: true,
	}
	candidates := &userGroupAccountAllowlistCandidateRepositoryStub{accounts: []Account{
		{ID: 2, Name: "allowed", Platform: PlatformOpenAI, Type: AccountTypeOAuth, Priority: 1, Concurrency: 3, Status: StatusActive, Schedulable: true},
		{ID: 4, Name: "temporary", Status: StatusActive, Schedulable: true, RateLimitResetAt: allowlistTimePtr(time.Now().Add(time.Hour))},
		{ID: 5, Name: "no longer active", Status: StatusDisabled, Schedulable: true},
	}}
	svc := NewUserGroupAccountAllowlistService(repo, candidates)

	selection, err := svc.GetAdminSelection(context.Background(), 41, 9)
	require.NoError(t, err)
	require.True(t, selection.Restricted)
	require.Equal(t, []int64{2, 5}, selection.AllowedAccountIDs)
	require.Len(t, selection.Candidates, 1)
	require.Equal(t, int64(2), selection.Candidates[0].AccountID)
	require.True(t, selection.Candidates[0].Allowed)
}

func allowlistTimePtr(value time.Time) *time.Time { return &value }

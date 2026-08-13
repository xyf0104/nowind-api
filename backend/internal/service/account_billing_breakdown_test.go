//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/stretchr/testify/require"
)

type accountBillingAccountRepoStub struct {
	AccountRepository
	account *Account
	err     error
}

func (r *accountBillingAccountRepoStub) GetByID(context.Context, int64) (*Account, error) {
	return r.account, r.err
}

type accountBillingUsageRepoStub struct {
	UsageLogRepository
	users        []usagestats.AccountBillingUser
	selectedUser *usagestats.AccountBillingSelectedUser
	models       []usagestats.AccountBillingModel
	usersCalls   int
	modelsCalls  int
}

func (r *accountBillingUsageRepoStub) GetAccountBillingUsers(context.Context, int64, time.Time, time.Time) ([]usagestats.AccountBillingUser, error) {
	r.usersCalls++
	return r.users, nil
}

func (r *accountBillingUsageRepoStub) GetAccountBillingModels(context.Context, int64, int64, time.Time, time.Time) (*usagestats.AccountBillingSelectedUser, []usagestats.AccountBillingModel, error) {
	r.modelsCalls++
	return r.selectedUser, r.models, nil
}

func TestAccountBillingBreakdownSupportsAllAccountTypes(t *testing.T) {
	accountTypes := []string{
		AccountTypeOAuth,
		AccountTypeSetupToken,
		AccountTypeAPIKey,
		AccountTypeUpstream,
		AccountTypeBedrock,
		AccountTypeServiceAccount,
	}
	start := time.Date(2026, 8, 13, 1, 0, 0, 0, time.UTC)
	end := start.Add(5 * time.Hour)

	for _, accountType := range accountTypes {
		t.Run(accountType, func(t *testing.T) {
			usageRepo := &accountBillingUsageRepoStub{users: []usagestats.AccountBillingUser{
				{UserID: 7, Requests: 1, Tokens: 10, AccountCost: 0.5, UserCost: 1},
			}}
			svc := &AccountUsageService{
				accountRepo:  &accountBillingAccountRepoStub{account: &Account{ID: 9, Type: accountType}},
				usageLogRepo: usageRepo,
			}

			result, err := svc.GetAccountBillingBreakdown(context.Background(), 9, start, end, nil, "Asia/Shanghai")
			require.NoError(t, err)
			require.NotNil(t, result.Users)
			require.Len(t, *result.Users, 1)
			require.Equal(t, 1, usageRepo.usersCalls)
			require.Zero(t, usageRepo.modelsCalls)
		})
	}
}

func TestAccountBillingBreakdownPreservesAccountNotFound(t *testing.T) {
	svc := &AccountUsageService{
		accountRepo:  &accountBillingAccountRepoStub{err: ErrAccountNotFound},
		usageLogRepo: &accountBillingUsageRepoStub{},
	}

	_, err := svc.GetAccountBillingBreakdown(context.Background(), 404, time.Now().Add(-time.Hour), time.Now(), nil, "UTC")
	require.ErrorIs(t, err, ErrAccountNotFound)
}

func TestAccountBillingBreakdownBuildsUserAndModelSummaries(t *testing.T) {
	accountRepo := &accountBillingAccountRepoStub{account: &Account{ID: 9, Type: AccountTypeOAuth}}
	usageRepo := &accountBillingUsageRepoStub{
		users: []usagestats.AccountBillingUser{
			{UserID: 1, Requests: 2, Tokens: 30, AccountCost: 1.2, UserCost: 2.4},
			{UserID: 2, Requests: 3, Tokens: 40, AccountCost: 1.8, UserCost: 3.6},
		},
		selectedUser: &usagestats.AccountBillingSelectedUser{UserID: 1, Username: "alice"},
		models: []usagestats.AccountBillingModel{
			{Model: "gpt-5.6", Requests: 2, Tokens: 30, AccountCost: 1.2, UserCost: 2.4},
		},
	}
	svc := &AccountUsageService{accountRepo: accountRepo, usageLogRepo: usageRepo}
	start := time.Date(2026, 8, 13, 1, 0, 0, 0, time.UTC)
	end := start.Add(5 * time.Hour)

	usersResult, err := svc.GetAccountBillingBreakdown(context.Background(), 9, start, end, nil, "UTC")
	require.NoError(t, err)
	require.NotNil(t, usersResult.Users)
	require.Nil(t, usersResult.Models)
	require.Equal(t, int64(5), usersResult.Summary.Requests)
	require.Equal(t, int64(70), usersResult.Summary.Tokens)
	require.InDelta(t, 3.0, usersResult.Summary.AccountCost, 0.0001)
	require.InDelta(t, 6.0, usersResult.Summary.UserCost, 0.0001)

	userID := int64(1)
	modelsResult, err := svc.GetAccountBillingBreakdown(context.Background(), 9, start, end, &userID, "UTC")
	require.NoError(t, err)
	require.Nil(t, modelsResult.Users)
	require.NotNil(t, modelsResult.Models)
	require.Equal(t, int64(1), modelsResult.SelectedUser.UserID)
	require.Equal(t, int64(2), modelsResult.Summary.Requests)
}

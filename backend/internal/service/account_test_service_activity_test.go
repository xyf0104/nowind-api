//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type accountTestActivityRepo struct {
	mockAccountRepoForGemini
	account    *Account
	touchedIDs []int64
}

func (r *accountTestActivityRepo) GetByID(_ context.Context, _ int64) (*Account, error) {
	return r.account, nil
}

func (r *accountTestActivityRepo) UpdateLastUsed(_ context.Context, id int64) error {
	r.touchedIDs = append(r.touchedIDs, id)
	return nil
}

func TestAccountTestServiceRecordsActivityBeforeSuccessfulModelTest(t *testing.T) {
	account := &Account{
		ID:       901,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Extra:    map[string]any{"synthetic_ui_test": true},
	}
	repo := &accountTestActivityRepo{account: account}
	svc := &AccountTestService{accountRepo: repo, recordTestActivity: repo.UpdateLastUsed}
	ctx, _ := newTestContext()

	require.NoError(t, svc.TestAccountConnection(ctx, account.ID, "", "", AccountTestModeDefault))
	require.Equal(t, []int64{account.ID}, repo.touchedIDs)
}

func TestAccountTestServiceRecordsActivityBeforeFailedModelTest(t *testing.T) {
	account := &Account{
		ID:          902,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Credentials: map[string]any{},
	}
	repo := &accountTestActivityRepo{account: account}
	svc := &AccountTestService{accountRepo: repo, recordTestActivity: repo.UpdateLastUsed}
	ctx, _ := newTestContext()

	require.Error(t, svc.TestAccountConnection(ctx, account.ID, "", "", AccountTestModeDefault))
	require.Equal(t, []int64{account.ID}, repo.touchedIDs)
}

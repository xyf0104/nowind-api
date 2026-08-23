package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

// emptySchedulerSnapshotCache is intentionally empty: it models a ready
// bucket whose asynchronous rebuild has not published the current account yet.
type emptySchedulerSnapshotCache struct {
	accounts map[int64]*Account
}

func (c *emptySchedulerSnapshotCache) GetSnapshot(context.Context, SchedulerBucket) ([]*Account, bool, error) {
	return []*Account{}, true, nil
}

func (c *emptySchedulerSnapshotCache) CaptureBucketWriteToken(context.Context, SchedulerBucket) (SchedulerBucketWriteToken, error) {
	return SchedulerBucketWriteToken{Epoch: 1}, nil
}

func (c *emptySchedulerSnapshotCache) SetSnapshot(context.Context, SchedulerBucket, SchedulerBucketWriteToken, []Account) error {
	return nil
}

func (c *emptySchedulerSnapshotCache) RetireBucket(context.Context, SchedulerBucket) error {
	return nil
}

func (c *emptySchedulerSnapshotCache) ReopenBucket(context.Context, SchedulerBucket) (SchedulerBucketWriteToken, error) {
	return SchedulerBucketWriteToken{Epoch: 1}, nil
}

func (c *emptySchedulerSnapshotCache) TryAcquireGroupLifecycleLease(context.Context, int64, time.Duration) (SchedulerGroupLifecycleLease, bool, error) {
	return SchedulerGroupLifecycleLease{}, false, nil
}

func (c *emptySchedulerSnapshotCache) ReleaseGroupLifecycleLease(context.Context, SchedulerGroupLifecycleLease) error {
	return nil
}

func (c *emptySchedulerSnapshotCache) GetAccount(_ context.Context, accountID int64) (*Account, error) {
	return c.accounts[accountID], nil
}

func (c *emptySchedulerSnapshotCache) SetAccount(context.Context, *Account) error { return nil }
func (c *emptySchedulerSnapshotCache) DeleteAccount(context.Context, int64) error { return nil }
func (c *emptySchedulerSnapshotCache) UpdateLastUsed(context.Context, map[int64]time.Time) error {
	return nil
}
func (c *emptySchedulerSnapshotCache) TryLockBucket(context.Context, SchedulerBucket, time.Duration) (bool, error) {
	return true, nil
}
func (c *emptySchedulerSnapshotCache) UnlockBucket(context.Context, SchedulerBucket) error {
	return nil
}
func (c *emptySchedulerSnapshotCache) ListBuckets(context.Context) ([]SchedulerBucket, error) {
	return nil, nil
}
func (c *emptySchedulerSnapshotCache) GetOutboxWatermark(context.Context) (int64, error) {
	return 0, nil
}
func (c *emptySchedulerSnapshotCache) SetOutboxWatermark(context.Context, int64) error { return nil }

type snapshotFallbackAccountRepo struct {
	AccountRepository
	accounts []Account
}

func (r *snapshotFallbackAccountRepo) GetByID(_ context.Context, id int64) (*Account, error) {
	for index := range r.accounts {
		if r.accounts[index].ID == id {
			return &r.accounts[index], nil
		}
	}
	return nil, nil
}

func (r *snapshotFallbackAccountRepo) ListSchedulableByPlatform(_ context.Context, platform string) ([]Account, error) {
	return filterFallbackAccounts(r.accounts, platform), nil
}

func (r *snapshotFallbackAccountRepo) ListSchedulableByGroupIDAndPlatform(ctx context.Context, _ int64, platform string) ([]Account, error) {
	return r.ListSchedulableByPlatform(ctx, platform)
}

func (r *snapshotFallbackAccountRepo) ListSchedulableUngroupedByPlatform(ctx context.Context, platform string) ([]Account, error) {
	return r.ListSchedulableByPlatform(ctx, platform)
}

func (r *snapshotFallbackAccountRepo) ListSchedulableByPlatforms(_ context.Context, platforms []string) ([]Account, error) {
	result := make([]Account, 0, len(r.accounts))
	for _, platform := range platforms {
		result = append(result, filterFallbackAccounts(r.accounts, platform)...)
	}
	return result, nil
}

func (r *snapshotFallbackAccountRepo) ListSchedulableByGroupIDAndPlatforms(ctx context.Context, _ int64, platforms []string) ([]Account, error) {
	return r.ListSchedulableByPlatforms(ctx, platforms)
}

func (r *snapshotFallbackAccountRepo) ListSchedulableUngroupedByPlatforms(ctx context.Context, platforms []string) ([]Account, error) {
	return r.ListSchedulableByPlatforms(ctx, platforms)
}

func filterFallbackAccounts(accounts []Account, platform string) []Account {
	result := make([]Account, 0, len(accounts))
	for _, account := range accounts {
		if account.Platform == platform && account.Status == StatusActive && account.Schedulable {
			result = append(result, account)
		}
	}
	return result
}

func fallbackTestAccount(id int64, platform string) Account {
	return Account{
		ID:          id,
		Platform:    platform,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{"api_key": "test-key"},
	}
}

func TestGatewayListSchedulableAccountsFallsBackWhenSnapshotIsEmpty(t *testing.T) {
	account := fallbackTestAccount(41, PlatformAnthropic)
	repo := &snapshotFallbackAccountRepo{accounts: []Account{account}}
	cache := &emptySchedulerSnapshotCache{accounts: map[int64]*Account{account.ID: &account}}
	snapshot := NewSchedulerSnapshotService(cache, nil, nil, nil, nil)
	svc := &GatewayService{
		accountRepo:       repo,
		schedulerSnapshot: snapshot,
		cfg:               &config.Config{RunMode: config.RunModeStandard},
	}

	accounts, mixed, err := svc.listSchedulableAccounts(context.Background(), nil, PlatformAnthropic, false)
	if err != nil {
		t.Fatalf("listSchedulableAccounts returned error: %v", err)
	}
	if !mixed || len(accounts) != 1 || accounts[0].ID != account.ID {
		t.Fatalf("expected repository fallback account, mixed=%v accounts=%+v", mixed, accounts)
	}
}

func TestOpenAIListSchedulableAccountsFallsBackWhenSnapshotIsEmpty(t *testing.T) {
	account := fallbackTestAccount(42, PlatformOpenAI)
	repo := &snapshotFallbackAccountRepo{accounts: []Account{account}}
	cache := &emptySchedulerSnapshotCache{accounts: map[int64]*Account{account.ID: &account}}
	snapshot := NewSchedulerSnapshotService(cache, nil, nil, nil, nil)
	svc := &OpenAIGatewayService{
		accountRepo:       repo,
		schedulerSnapshot: snapshot,
	}

	accounts, err := svc.listSchedulableAccounts(context.Background(), nil, PlatformOpenAI)
	if err != nil {
		t.Fatalf("listSchedulableAccounts returned error: %v", err)
	}
	if len(accounts) != 1 || accounts[0].ID != account.ID {
		t.Fatalf("expected repository fallback account, accounts=%+v", accounts)
	}
}

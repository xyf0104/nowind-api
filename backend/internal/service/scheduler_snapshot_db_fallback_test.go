package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// emptySchedulerSnapshotCache is intentionally empty: it models a ready
// bucket whose asynchronous rebuild has not published the current account yet.
type emptySchedulerSnapshotCache struct {
	accounts         map[int64]*Account
	snapshotAccounts []*Account
}

func (c *emptySchedulerSnapshotCache) GetSnapshot(context.Context, SchedulerBucket) ([]*Account, bool, error) {
	if len(c.snapshotAccounts) == 0 {
		return []*Account{}, true, nil
	}
	out := make([]*Account, 0, len(c.snapshotAccounts))
	for _, account := range c.snapshotAccounts {
		if account == nil {
			continue
		}
		cloned := *account
		out = append(out, &cloned)
	}
	return out, true, nil
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
	snapshot := NewSchedulerSnapshotService(cache, nil, repo, nil, &config.Config{
		RunMode: config.RunModeStandard,
		Gateway: config.GatewayConfig{
			Scheduling: config.GatewaySchedulingConfig{DbFallbackEnabled: true},
		},
	})
	svc := &GatewayService{
		accountRepo:       repo,
		schedulerSnapshot: snapshot,
		cfg: &config.Config{
			RunMode: config.RunModeStandard,
			Gateway: config.GatewayConfig{
				Scheduling: config.GatewaySchedulingConfig{DbFallbackEnabled: true},
			},
		},
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
	snapshot := NewSchedulerSnapshotService(cache, nil, repo, nil, &config.Config{
		Gateway: config.GatewayConfig{
			Scheduling: config.GatewaySchedulingConfig{DbFallbackEnabled: true},
		},
	})
	svc := &OpenAIGatewayService{
		accountRepo:       repo,
		schedulerSnapshot: snapshot,
		cfg: &config.Config{
			Gateway: config.GatewayConfig{
				Scheduling: config.GatewaySchedulingConfig{DbFallbackEnabled: true},
			},
		},
	}

	accounts, err := svc.listSchedulableAccounts(context.Background(), nil, PlatformOpenAI)
	if err != nil {
		t.Fatalf("listSchedulableAccounts returned error: %v", err)
	}
	if len(accounts) != 1 || accounts[0].ID != account.ID {
		t.Fatalf("expected repository fallback account, accounts=%+v", accounts)
	}
}

func TestOpenAISelectionRecoversWhenFailoverExhaustsPartialSnapshot(t *testing.T) {
	primary := fallbackTestAccount(44, PlatformOpenAI)
	backup := fallbackTestAccount(45, PlatformOpenAI)
	repo := &snapshotFallbackAccountRepo{accounts: []Account{primary, backup}}
	cache := &emptySchedulerSnapshotCache{
		accounts:         map[int64]*Account{primary.ID: &primary, backup.ID: &backup},
		snapshotAccounts: []*Account{&primary},
	}
	cfg := &config.Config{
		RunMode: config.RunModeStandard,
		Gateway: config.GatewayConfig{
			Scheduling: config.GatewaySchedulingConfig{DbFallbackEnabled: true},
		},
	}
	snapshot := NewSchedulerSnapshotService(cache, nil, repo, nil, cfg)
	svc := &OpenAIGatewayService{
		accountRepo:       repo,
		schedulerSnapshot: snapshot,
		cfg:               cfg,
	}

	selected, err := svc.SelectAccountForModelWithExclusions(
		context.Background(),
		nil,
		"",
		"",
		map[int64]struct{}{primary.ID: {}},
	)
	require.NoError(t, err)
	require.NotNil(t, selected)
	require.Equal(t, backup.ID, selected.ID,
		"a stale partial snapshot must not turn failover into a false 503")
}

func TestOpenAIAdvancedSchedulerRecoversWhenFailoverExhaustsPartialSnapshot(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	defer resetOpenAIAdvancedSchedulerSettingCacheForTest()

	primary := fallbackTestAccount(46, PlatformOpenAI)
	backup := fallbackTestAccount(47, PlatformOpenAI)
	repo := &snapshotFallbackAccountRepo{accounts: []Account{primary, backup}}
	cache := &emptySchedulerSnapshotCache{
		accounts:         map[int64]*Account{primary.ID: &primary, backup.ID: &backup},
		snapshotAccounts: []*Account{&primary},
	}
	cfg := &config.Config{
		RunMode: config.RunModeStandard,
		Gateway: config.GatewayConfig{
			Scheduling: config.GatewaySchedulingConfig{DbFallbackEnabled: true},
		},
	}
	svc := &OpenAIGatewayService{
		accountRepo:        repo,
		schedulerSnapshot:  NewSchedulerSnapshotService(cache, nil, repo, nil, cfg),
		cfg:                cfg,
		rateLimitService:   newOpenAIAdvancedSchedulerRateLimitService("true"),
		concurrencyService: nil,
	}

	selection, _, err := svc.SelectAccountWithScheduler(
		context.Background(),
		nil,
		"",
		"",
		"",
		map[int64]struct{}{primary.ID: {}},
		OpenAIUpstreamTransportAny,
		false,
	)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Account)
	require.Equal(t, backup.ID, selection.Account.ID,
		"the advanced scheduler must recover the authoritative pool before returning 503")
}

func TestSchedulerSnapshotDatabaseFallbackHonorsConfigurationAndRateLimit(t *testing.T) {
	account := fallbackTestAccount(43, PlatformOpenAI)
	repo := &snapshotFallbackAccountRepo{accounts: []Account{account}}
	cache := &emptySchedulerSnapshotCache{accounts: map[int64]*Account{account.ID: &account}}

	disabled := NewSchedulerSnapshotService(cache, nil, repo, nil, &config.Config{
		Gateway: config.GatewayConfig{
			Scheduling: config.GatewaySchedulingConfig{DbFallbackEnabled: false},
		},
	})
	_, _, err := disabled.ListSchedulableAccountsFromDatabase(context.Background(), nil, PlatformOpenAI, false)
	if !errors.Is(err, ErrSchedulerCacheNotReady) {
		t.Fatalf("expected disabled fallback to fail closed, got %v", err)
	}

	limited := NewSchedulerSnapshotService(cache, nil, repo, nil, &config.Config{
		Gateway: config.GatewayConfig{
			Scheduling: config.GatewaySchedulingConfig{
				DbFallbackEnabled: true,
				DbFallbackMaxQPS:  1,
			},
		},
	})
	accounts, _, err := limited.ListSchedulableAccountsFromDatabase(context.Background(), nil, PlatformOpenAI, false)
	if err != nil || len(accounts) != 1 {
		t.Fatalf("expected first bounded fallback to succeed, accounts=%+v err=%v", accounts, err)
	}
	_, _, err = limited.ListSchedulableAccountsFromDatabase(context.Background(), nil, PlatformOpenAI, false)
	if !errors.Is(err, ErrSchedulerFallbackLimited) {
		t.Fatalf("expected second fallback to be rate limited, got %v", err)
	}
}

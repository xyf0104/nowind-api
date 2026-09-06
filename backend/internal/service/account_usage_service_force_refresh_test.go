package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
)

type forceRefreshAccountUsageRepo struct {
	AccountRepository
	account *Account
}

type orderedCodexSnapshotRaceRepo struct {
	AccountRepository
	mu              sync.Mutex
	latestAt        time.Time
	latest          map[string]any
	blockedAt       time.Time
	blockedStarted  chan struct{}
	releaseBlocked  chan struct{}
	blockedFinished chan struct{}
}

func (r *orderedCodexSnapshotRaceRepo) UpdateOpenAICodexSnapshotIfIdentityMatches(_ context.Context, _ *Account, updates map[string]any) (bool, error) {
	updatedAtText, ok := updates["codex_usage_updated_at"].(string)
	if !ok {
		return false, fmt.Errorf("codex_usage_updated_at has type %T, want string", updates["codex_usage_updated_at"])
	}
	updatedAt, err := time.Parse(time.RFC3339Nano, updatedAtText)
	if err != nil {
		return false, err
	}
	if updatedAt.Equal(r.blockedAt) {
		close(r.blockedStarted)
		<-r.releaseBlocked
		defer close(r.blockedFinished)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.latestAt.IsZero() && updatedAt.Before(r.latestAt) {
		return false, nil
	}
	r.latestAt = updatedAt
	r.latest = make(map[string]any, len(updates))
	for key, value := range updates {
		r.latest[key] = value
	}
	return true, nil
}

func (r *forceRefreshAccountUsageRepo) GetByID(_ context.Context, _ int64) (*Account, error) {
	return r.account, nil
}

func (r *forceRefreshAccountUsageRepo) UpdateExtra(_ context.Context, _ int64, updates map[string]any) error {
	if r.account.Extra == nil {
		r.account.Extra = make(map[string]any)
	}
	for key, value := range updates {
		r.account.Extra[key] = value
	}
	return nil
}

func (r *forceRefreshAccountUsageRepo) CompareAndSwapOpenAIWeeklyState(_ context.Context, expected *Account, updates map[string]any) (bool, error) {
	if expected == nil || r.account == nil || expected.ID != r.account.ID {
		return false, nil
	}
	mergeAccountExtra(r.account, updates)
	return true, nil
}

type forceRefreshWindowStatsRepo struct {
	UsageLogRepository
	calls      int
	rangeCalls int
	rangeStart time.Time
	rangeEnd   time.Time
	stats      *usagestats.AccountStats
	rangeStats *usagestats.AccountStats
}

func (r *forceRefreshWindowStatsRepo) GetAccountWindowStats(_ context.Context, _ int64, _ time.Time) (*usagestats.AccountStats, error) {
	r.calls++
	return r.stats, nil
}

func (r *forceRefreshWindowStatsRepo) GetAccountWindowStatsRange(_ context.Context, _ int64, startTime, endTime time.Time) (*usagestats.AccountStats, error) {
	r.rangeCalls++
	r.rangeStart = startTime
	r.rangeEnd = endTime
	if r.rangeStats != nil {
		return r.rangeStats, nil
	}
	return r.stats, nil
}

func newForceRefreshOpenAIUsageService() (*AccountUsageService, *forceRefreshWindowStatsRepo) {
	resetAt := time.Now().Add(6 * 24 * time.Hour).UTC().Truncate(time.Second)
	rateLimitedUntil := time.Now().Add(time.Hour)
	account := &Account{
		ID:               42,
		Platform:         PlatformOpenAI,
		Type:             AccountTypeOAuth,
		Status:           StatusActive,
		RateLimitResetAt: &rateLimitedUntil,
		Extra: map[string]any{
			"codex_5h_used_percent": 4.0,
			"codex_5h_reset_at":     time.Now().Add(4 * time.Hour).UTC().Truncate(time.Second).Format(time.RFC3339),
			"codex_7d_used_percent": 13.0,
			"codex_7d_reset_at":     resetAt.Format(time.RFC3339),
		},
	}
	windowRepo := &forceRefreshWindowStatsRepo{stats: &usagestats.AccountStats{
		Requests:     3464,
		Tokens:       426_000_000,
		Cost:         413.92,
		StandardCost: 413.92,
		UserCost:     96.09,
	}}
	return &AccountUsageService{
		accountRepo:  &forceRefreshAccountUsageRepo{account: account},
		usageLogRepo: windowRepo,
		cache:        NewUsageCache(),
	}, windowRepo
}

func TestAccountUsageService_OpenAIForceRefreshFailureDoesNotMixOldQuotaWithCurrentCost(t *testing.T) {
	t.Parallel()

	svc, windowRepo := newForceRefreshOpenAIUsageService()
	usage, err := svc.GetUsage(context.Background(), 42, true)
	if err == nil {
		t.Fatal("expected forced OpenAI quota refresh failure")
	}
	if usage != nil {
		t.Fatalf("forced refresh returned mixed usage: %#v", usage)
	}
	if !strings.Contains(err.Error(), "force refresh openai codex quota failed") {
		t.Fatalf("unexpected forced refresh error: %v", err)
	}
	if windowRepo.calls != 0 {
		t.Fatalf("window stats queried %d times after failed forced probe; want 0", windowRepo.calls)
	}
}

func TestAccountUsageService_OpenAINonForceRefreshFailureKeepsFallback(t *testing.T) {
	t.Parallel()

	svc, windowRepo := newForceRefreshOpenAIUsageService()
	usage, err := svc.GetUsage(context.Background(), 42, false)
	if err != nil {
		t.Fatalf("non-forced OpenAI usage fallback returned error: %v", err)
	}
	if usage == nil || usage.SevenDay == nil || usage.SevenDay.WindowStats == nil {
		t.Fatalf("non-forced OpenAI usage fallback is incomplete: %#v", usage)
	}
	if usage.SevenDay.Utilization != 13 {
		t.Fatalf("fallback utilization = %v, want old snapshot 13", usage.SevenDay.Utilization)
	}
	if usage.SevenDay.WindowStats.Cost != 413.92 {
		t.Fatalf("fallback DB cost = %v, want 413.92", usage.SevenDay.WindowStats.Cost)
	}
	if windowRepo.calls != 2 {
		t.Fatalf("window stats queried %d times, want 2 for 5h and 7d", windowRepo.calls)
	}
}

func TestAccountUsageService_OpenAIForceRefreshReturnsMatchingQuotaAndCostSnapshot(t *testing.T) {
	t.Parallel()

	parentID := int64(100)
	shadow := &Account{
		ID:               42,
		ParentAccountID:  &parentID,
		Platform:         PlatformOpenAI,
		Type:             AccountTypeOAuth,
		Status:           StatusActive,
		QuotaDimension:   QuotaDimensionSpark,
		RateLimitResetAt: nil,
	}
	parent := &Account{
		ID:       parentID,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Status:   StatusActive,
		Credentials: map[string]any{
			"chatgpt_account_id": "org-force-refresh-parent",
			"access_token":       "fake-access-token",
		},
	}
	repo := &sparkShadowUsageTestRepo{accounts: map[int64]*Account{
		shadow.ID: shadow,
		parent.ID: parent,
	}}
	tokenProvider := NewOpenAITokenProvider(repo, &stubQuotaTokenCache{tokens: map[string]string{
		OpenAITokenCacheKey(parent): "fake-access-token",
	}}, nil)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		_ = json.NewEncoder(w).Encode(OpenAIQuotaUsage{
			AdditionalRateLimits: []OpenAIAdditionalRateLimit{{
				MeteredFeature: "codex_bengalfox",
				RateLimit: &OpenAIRateLimit{
					PrimaryWindow: &OpenAIRateLimitWindow{
						UsedPercent:        4,
						ResetAfterSeconds:  4 * 60 * 60,
						LimitWindowSeconds: 5 * 60 * 60,
					},
					SecondaryWindow: &OpenAIRateLimitWindow{
						UsedPercent:        13,
						ResetAfterSeconds:  6 * 24 * 60 * 60,
						LimitWindowSeconds: 7 * 24 * 60 * 60,
					},
				},
			}},
		})
	}))
	t.Cleanup(server.Close)

	windowRepo := &forceRefreshWindowStatsRepo{stats: &usagestats.AccountStats{
		Requests:     3464,
		Tokens:       426_000_000,
		Cost:         413.92,
		StandardCost: 413.92,
		UserCost:     96.09,
	}}
	svc := &AccountUsageService{
		accountRepo:        repo,
		usageLogRepo:       windowRepo,
		openAIQuotaService: NewOpenAIQuotaService(repo, nil, tokenProvider, newQuotaRedirectingFactory(server)),
		cache:              NewUsageCache(),
	}

	usage, err := svc.GetUsage(context.Background(), shadow.ID, true)
	if err != nil {
		t.Fatalf("forced OpenAI refresh failed: %v", err)
	}
	if usage == nil || usage.SevenDay == nil || usage.SevenDay.WindowStats == nil {
		t.Fatalf("forced OpenAI refresh returned incomplete usage: %#v", usage)
	}
	if usage.SevenDay.Utilization != 13 || usage.SevenDay.WindowStats.Cost != 413.92 {
		t.Fatalf("forced refresh quota/cost mismatch: %#v", usage.SevenDay)
	}
	if usage.SevenDay.WeeklyEstimateUSD != nil {
		t.Fatalf("first forced refresh weekly estimate = %v, want a join baseline", *usage.SevenDay.WeeklyEstimateUSD)
	}
	if windowRepo.rangeCalls != 1 || windowRepo.calls != 2 || !windowRepo.rangeStart.Before(windowRepo.rangeEnd) {
		t.Fatalf("invalid point-in-time sampling: range=%d windows=%d", windowRepo.rangeCalls, windowRepo.calls)
	}
	if snapshotAt, parseErr := time.Parse(time.RFC3339Nano, fmt.Sprint(shadow.Extra["codex_usage_updated_at"])); parseErr != nil || !windowRepo.rangeEnd.Equal(snapshotAt) {
		t.Fatalf("range end = %v, want quota snapshot %v (parse error: %v)", windowRepo.rangeEnd, snapshotAt, parseErr)
	}
}

func TestAccountUsageService_OpenAIWeeklyEstimateUsesPointInTimeRangeCost(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC().Truncate(time.Microsecond)
	resetAt := now.Add(6 * 24 * time.Hour)
	firstAt := now.Add(-2 * time.Minute)
	secondAt := now.Add(-1 * time.Minute)
	account := &Account{
		ID:       77,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Status:   StatusActive,
		Credentials: map[string]any{
			"chatgpt_account_id": "point-in-time-account",
		},
		Extra: map[string]any{
			"codex_7d_used_percent":  20.0,
			"codex_7d_reset_at":      resetAt.Format(time.RFC3339Nano),
			"codex_usage_updated_at": firstAt.Format(time.RFC3339Nano),
		},
	}
	repo := &forceRefreshAccountUsageRepo{account: account}
	windowRepo := &forceRefreshWindowStatsRepo{
		stats:      &usagestats.AccountStats{Cost: 999},
		rangeStats: &usagestats.AccountStats{Cost: 100},
	}
	svc := &AccountUsageService{
		accountRepo:  repo,
		usageLogRepo: windowRepo,
		cache:        NewUsageCache(),
	}

	first, err := svc.GetUsage(context.Background(), account.ID)
	if err != nil {
		t.Fatalf("first usage query failed: %v", err)
	}
	if first == nil || first.SevenDay == nil {
		t.Fatalf("first point-in-time sample is incomplete: %#v", first)
	}
	if first.SevenDay.WeeklyEstimateUSD != nil {
		t.Fatalf("first point-in-time sample estimate = %v, want a 20%% / $100 join baseline", *first.SevenDay.WeeklyEstimateUSD)
	}

	account.Extra["codex_7d_used_percent"] = 21.0
	account.Extra["codex_usage_updated_at"] = secondAt.Format(time.RFC3339Nano)
	windowRepo.rangeStats = &usagestats.AccountStats{Cost: 110}
	second, err := svc.GetUsage(context.Background(), account.ID)
	if err != nil {
		t.Fatalf("second usage query failed: %v", err)
	}
	if second == nil || second.SevenDay == nil || second.SevenDay.WeeklyEstimateUSD == nil {
		t.Fatalf("second point-in-time sample should freeze the 21%% value: %#v", second)
	}
	if got := *second.SevenDay.WeeklyEstimateUSD; math.Abs(got-1000) > 1e-9 {
		t.Fatalf("estimate = %v, want 1000 from ($110 - $100) / (21%% - 20%%) * 100", got)
	}
	if windowRepo.rangeCalls != 2 {
		t.Fatalf("point-in-time range query count = %d, want 2", windowRepo.rangeCalls)
	}
}

func TestAccountUsageService_OpenAIMissingSevenDaySnapshotDoesNotCreateEstimateBaseline(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC().Truncate(time.Microsecond)
	account := &Account{
		ID:       78,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Status:   StatusActive,
		Extra: map[string]any{
			"codex_5h_used_percent":  12.0,
			"codex_usage_updated_at": now.Add(-time.Minute).Format(time.RFC3339Nano),
		},
	}
	windowRepo := &forceRefreshWindowStatsRepo{stats: &usagestats.AccountStats{Cost: 120}}
	svc := &AccountUsageService{
		accountRepo:  &forceRefreshAccountUsageRepo{account: account},
		usageLogRepo: windowRepo,
		cache:        NewUsageCache(),
	}

	usage, err := svc.GetUsage(context.Background(), account.ID)
	if err != nil {
		t.Fatalf("usage query failed: %v", err)
	}
	if usage == nil || usage.SevenDay != nil {
		t.Fatalf("missing official 7d window must stay absent, got %#v", usage)
	}
	if _, exists := account.Extra[openAIWeeklyEstimateBaselineKey]; exists {
		t.Fatalf("missing official 7d window unexpectedly created estimate state: %#v", account.Extra[openAIWeeklyEstimateBaselineKey])
	}
	if windowRepo.rangeCalls != 0 {
		t.Fatalf("missing official 7d window made %d point-in-time queries, want 0", windowRepo.rangeCalls)
	}
	if windowRepo.calls != 1 {
		t.Fatalf("missing official 7d window made %d live-window queries, want only the 5h query", windowRepo.calls)
	}
}

func TestOpenAICodexSnapshot_ManualRefreshSupersedesDelayedGatewayWrite(t *testing.T) {
	oldAt := time.Date(2026, time.August, 14, 9, 0, 0, 100000000, time.UTC)
	newAt := oldAt.Add(time.Second)
	repo := &orderedCodexSnapshotRaceRepo{
		blockedAt:       oldAt,
		blockedStarted:  make(chan struct{}),
		releaseBlocked:  make(chan struct{}),
		blockedFinished: make(chan struct{}),
	}
	gateway := &OpenAIGatewayService{accountRepo: repo}
	usageService := &AccountUsageService{accountRepo: repo}

	account := &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	gateway.updateCodexUsageSnapshot(context.Background(), account, &OpenAICodexUsageSnapshot{
		PrimaryUsedPercent:   ptrFloat64WS(12),
		PrimaryWindowMinutes: ptrIntWS(10080),
		UpdatedAt:            oldAt.Format(time.RFC3339Nano),
	})
	<-repo.blockedStarted

	applied, err := usageService.persistOpenAICodexProbeSnapshot(account, map[string]any{
		"codex_usage_updated_at": newAt.Format(time.RFC3339Nano),
		"codex_7d_used_percent":  13.0,
	})
	if err != nil || !applied {
		t.Fatalf("manual snapshot persistence = (%v, %v), want (true, nil)", applied, err)
	}
	close(repo.releaseBlocked)
	<-repo.blockedFinished

	repo.mu.Lock()
	defer repo.mu.Unlock()
	if repo.latestAt != newAt {
		t.Fatalf("persisted timestamp = %v, want newer manual timestamp %v", repo.latestAt, newAt)
	}
	if got := repo.latest["codex_7d_used_percent"]; got != 13.0 {
		t.Fatalf("persisted utilization = %v, want manual refresh value 13", got)
	}
}

func TestAccountUsageService_OpenAIForceRefreshRejectsPartialQuotaSnapshot(t *testing.T) {
	t.Parallel()

	parentID := int64(100)
	shadow := &Account{
		ID:              42,
		ParentAccountID: &parentID,
		Platform:        PlatformOpenAI,
		Type:            AccountTypeOAuth,
		Status:          StatusActive,
		QuotaDimension:  QuotaDimensionSpark,
		Extra: map[string]any{
			"codex_7d_used_percent": 12.0,
			"codex_7d_reset_at":     time.Now().Add(6 * 24 * time.Hour).UTC().Format(time.RFC3339),
		},
	}
	parent := &Account{
		ID:       parentID,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Status:   StatusActive,
		Credentials: map[string]any{
			"chatgpt_account_id": "org-partial-refresh-parent",
			"access_token":       "fake-access-token",
		},
	}
	repo := &sparkShadowUsageTestRepo{accounts: map[int64]*Account{
		shadow.ID: shadow,
		parent.ID: parent,
	}}
	tokenProvider := NewOpenAITokenProvider(repo, &stubQuotaTokenCache{tokens: map[string]string{
		OpenAITokenCacheKey(parent): "fake-access-token",
	}}, nil)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		_ = json.NewEncoder(w).Encode(OpenAIQuotaUsage{
			AdditionalRateLimits: []OpenAIAdditionalRateLimit{{
				MeteredFeature: "codex_bengalfox",
				RateLimit: &OpenAIRateLimit{
					PrimaryWindow: &OpenAIRateLimitWindow{
						UsedPercent:        4,
						ResetAfterSeconds:  4 * 60 * 60,
						LimitWindowSeconds: 5 * 60 * 60,
					},
				},
			}},
		})
	}))
	t.Cleanup(server.Close)

	windowRepo := &forceRefreshWindowStatsRepo{stats: &usagestats.AccountStats{Cost: 413.92}}
	svc := &AccountUsageService{
		accountRepo:        repo,
		usageLogRepo:       windowRepo,
		openAIQuotaService: NewOpenAIQuotaService(repo, nil, tokenProvider, newQuotaRedirectingFactory(server)),
		cache:              NewUsageCache(),
	}

	usage, err := svc.GetUsage(context.Background(), shadow.ID, true)
	if err == nil || !strings.Contains(err.Error(), "no 7d quota snapshot") {
		t.Fatalf("partial forced refresh error = %v, want missing 7d error", err)
	}
	if usage != nil {
		t.Fatalf("partial forced refresh returned mixed usage: %#v", usage)
	}
	if windowRepo.calls != 0 {
		t.Fatalf("window stats queried %d times after partial forced refresh; want 0", windowRepo.calls)
	}
	if _, exists := shadow.Extra["codex_5h_used_percent"]; exists {
		t.Fatal("partial forced refresh mutated the previous complete account snapshot")
	}
}

func TestApplyExtraToUsageClearsWindowMissingFromLatestSnapshot(t *testing.T) {
	t.Parallel()

	usage := &UsageInfo{
		FiveHour: &UsageProgress{Utilization: 3},
		SevenDay: &UsageProgress{Utilization: 12},
	}
	applyExtraToUsage(usage, map[string]any{
		"codex_5h_used_percent": 4.0,
		"codex_7d_used_percent": nil,
	}, time.Now())

	if usage.FiveHour == nil || usage.FiveHour.Utilization != 4 {
		t.Fatalf("latest 5h snapshot not applied: %#v", usage.FiveHour)
	}
	if usage.SevenDay != nil {
		t.Fatalf("stale 7d window survived latest snapshot: %#v", usage.SevenDay)
	}
}

func TestAccountUsageService_OpenAINewerForceRefreshSupersedesOlderPersistence(t *testing.T) {
	parentID := int64(100)
	shadow := &Account{
		ID:              42,
		ParentAccountID: &parentID,
		Platform:        PlatformOpenAI,
		Type:            AccountTypeOAuth,
		Status:          StatusActive,
		QuotaDimension:  QuotaDimensionSpark,
	}
	parent := &Account{
		ID:       parentID,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Status:   StatusActive,
		Credentials: map[string]any{
			"chatgpt_account_id": "org-concurrent-refresh-parent",
			"access_token":       "fake-access-token",
		},
	}
	updates := make(chan map[string]any, 2)
	repo := &sparkShadowUsageTestRepo{
		accounts:      map[int64]*Account{shadow.ID: shadow, parent.ID: parent},
		updateExtraCh: updates,
	}
	tokenProvider := NewOpenAITokenProvider(repo, &stubQuotaTokenCache{tokens: map[string]string{
		OpenAITokenCacheKey(parent): "fake-access-token",
	}}, nil)

	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	var requestMu sync.Mutex
	usageRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		if r.URL.Path != "/backend-api/wham/usage" {
			_ = json.NewEncoder(w).Encode(map[string]any{})
			return
		}
		requestMu.Lock()
		usageRequests++
		requestNumber := usageRequests
		requestMu.Unlock()
		if requestNumber == 1 {
			close(firstStarted)
			<-releaseFirst
		}
		utilization := 9.0
		if requestNumber == 2 {
			utilization = 13.0
		}
		_ = json.NewEncoder(w).Encode(OpenAIQuotaUsage{
			AdditionalRateLimits: []OpenAIAdditionalRateLimit{{
				MeteredFeature: "codex_bengalfox",
				RateLimit: &OpenAIRateLimit{
					PrimaryWindow:   &OpenAIRateLimitWindow{UsedPercent: 4, ResetAfterSeconds: 3600, LimitWindowSeconds: 18000},
					SecondaryWindow: &OpenAIRateLimitWindow{UsedPercent: utilization, ResetAfterSeconds: 86400, LimitWindowSeconds: 604800},
				},
			}},
		})
	}))
	t.Cleanup(server.Close)

	svc := &AccountUsageService{
		accountRepo:        repo,
		openAIQuotaService: NewOpenAIQuotaService(repo, nil, tokenProvider, newQuotaRedirectingFactory(server)),
		cache:              NewUsageCache(),
	}
	firstErr := make(chan error, 1)
	go func() {
		_, err := svc.getOpenAIUsage(context.Background(), shadow, true)
		firstErr <- err
	}()
	<-firstStarted

	secondErr := make(chan error, 1)
	go func() {
		_, err := svc.getOpenAIUsage(context.Background(), shadow, true)
		secondErr <- err
	}()
	deadline := time.Now().Add(2 * time.Second)
	for svc.openAIProbeState(shadow.ID).generation.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if svc.openAIProbeState(shadow.ID).generation.Load() != 2 {
		t.Fatal("newer force refresh did not advance the account generation")
	}
	close(releaseFirst)

	if err := <-firstErr; err == nil || !strings.Contains(err.Error(), "superseded") {
		t.Fatalf("older force refresh error = %v, want superseded", err)
	}
	if err := <-secondErr; err != nil {
		t.Fatalf("newer force refresh failed: %v", err)
	}

	select {
	case persisted := <-updates:
		if persisted["codex_7d_used_percent"] != 13.0 {
			t.Fatalf("persisted utilization = %v, want latest 13", persisted["codex_7d_used_percent"])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("newer force refresh did not persist")
	}
	select {
	case stale := <-updates:
		t.Fatalf("older force refresh unexpectedly persisted: %#v", stale)
	default:
	}
}

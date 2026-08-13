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

func (r *orderedCodexSnapshotRaceRepo) UpdateOpenAICodexSnapshot(_ context.Context, _ int64, updates map[string]any) (bool, error) {
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

type forceRefreshWindowStatsRepo struct {
	UsageLogRepository
	calls int
	stats *usagestats.AccountStats
}

func (r *forceRefreshWindowStatsRepo) GetAccountWindowStats(_ context.Context, _ int64, _ time.Time) (*usagestats.AccountStats, error) {
	r.calls++
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
	if usage.SevenDay.Utilization != 13 {
		t.Fatalf("forced refresh utilization = %v, want 13", usage.SevenDay.Utilization)
	}
	if usage.SevenDay.WindowStats.Cost != 413.92 {
		t.Fatalf("forced refresh account cost = %v, want 413.92", usage.SevenDay.WindowStats.Cost)
	}
	weeklyLimit := usage.SevenDay.WindowStats.Cost / (usage.SevenDay.Utilization / 100)
	if math.Round(weeklyLimit) != 3184 {
		t.Fatalf("forced refresh weekly limit = %v, want 3184 from 413.92 / 13%%", weeklyLimit)
	}
	if windowRepo.calls != 2 {
		t.Fatalf("window stats queried %d times, want 2 for 5h and 7d", windowRepo.calls)
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

	gateway.updateCodexUsageSnapshot(context.Background(), 42, &OpenAICodexUsageSnapshot{
		PrimaryUsedPercent:   ptrFloat64WS(12),
		PrimaryWindowMinutes: ptrIntWS(10080),
		UpdatedAt:            oldAt.Format(time.RFC3339Nano),
	})
	<-repo.blockedStarted

	applied, err := usageService.persistOpenAICodexProbeSnapshot(42, map[string]any{
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

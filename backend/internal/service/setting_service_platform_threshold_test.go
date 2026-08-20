//go:build unit

package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type platformThresholdSettingRepo struct {
	*mockSettingRepo

	callsMu       sync.Mutex
	getValueErr   error
	getValueCalls int
	getValueHook  func(context.Context, string) (string, error)
}

func (r *platformThresholdSettingRepo) GetValue(ctx context.Context, key string) (string, error) {
	r.callsMu.Lock()
	r.getValueCalls++
	hook := r.getValueHook
	err := r.getValueErr
	r.callsMu.Unlock()

	if hook != nil {
		return hook(ctx, key)
	}
	if err != nil {
		return "", err
	}
	return r.mockSettingRepo.GetValue(ctx, key)
}

func (r *platformThresholdSettingRepo) valueCalls() int {
	r.callsMu.Lock()
	defer r.callsMu.Unlock()
	return r.getValueCalls
}

func (r *platformThresholdSettingRepo) resetValueCalls() {
	r.callsMu.Lock()
	r.getValueCalls = 0
	r.callsMu.Unlock()
}

func newSettingServiceForPlatformThresholdTest(seed map[string]string) (*SettingService, *platformThresholdSettingRepo) {
	accountSchedulingThresholdsSF.Forget(SettingKeyAccountSchedulingThresholds)
	accountSchedulingThresholdsCache.Store(&cachedAccountSchedulingThresholds{})
	repo := &platformThresholdSettingRepo{mockSettingRepo: newMockSettingRepo()}
	for key, value := range seed {
		repo.data[key] = value
	}
	return NewSettingService(repo, &config.Config{}), repo
}

func TestPlatformSchedulingThresholds_RoundTrip_DefaultsAndStoredValues(t *testing.T) {
	svc, _ := newSettingServiceForPlatformThresholdTest(nil)

	got := svc.parseSettings(map[string]string{})
	require.Equal(t, map[string]int{
		PlatformOpenAI:    100,
		PlatformAnthropic: 100,
		PlatformGrok:      100,
	}, got.AccountSchedulingThresholds)

	got = svc.parseSettings(map[string]string{
		SettingKeyAccountSchedulingThresholds: `{"openai":91,"grok":77,"gemini":85,"kiro":99}`,
	})
	require.Equal(t, 91, got.AccountSchedulingThresholds[PlatformOpenAI])
	require.Equal(t, 100, got.AccountSchedulingThresholds[PlatformAnthropic])
	require.Equal(t, 77, got.AccountSchedulingThresholds[PlatformGrok])
	require.NotContains(t, got.AccountSchedulingThresholds, PlatformGemini)
	require.NotContains(t, got.AccountSchedulingThresholds, "kiro")
}

func TestBuildSystemSettingsUpdates_PersistsAccountSchedulingThresholds(t *testing.T) {
	svc, _ := newSettingServiceForPlatformThresholdTest(nil)

	updates, err := svc.buildSystemSettingsUpdates(context.Background(), &SystemSettings{
		AccountSchedulingThresholds: map[string]int{
			PlatformOpenAI:    91,
			PlatformAnthropic: 88,
			PlatformGrok:      77,
		},
	})
	require.NoError(t, err)
	require.JSONEq(t, `{"openai":91,"anthropic":88,"grok":77}`, updates[SettingKeyAccountSchedulingThresholds])
}

func TestValidateAndNormalizeAccountSchedulingThresholds_FillsMissingPlatforms(t *testing.T) {
	normalized, err := validateAndNormalizeAccountSchedulingThresholds(map[string]int{
		PlatformOpenAI: 91,
	})
	require.NoError(t, err)
	require.Equal(t, 91, normalized[PlatformOpenAI])
	require.Equal(t, 100, normalized[PlatformAnthropic])
	require.Equal(t, 100, normalized[PlatformGrok])
	require.NotContains(t, normalized, PlatformGemini)
	require.NotContains(t, normalized, "kiro")
	require.NotContains(t, normalized, PlatformAntigravity)
}

func TestValidateAndNormalizeAccountSchedulingThresholds_RejectsUnsupportedPlatforms(t *testing.T) {
	_, err := validateAndNormalizeAccountSchedulingThresholds(map[string]int{
		PlatformGemini: 85,
	})
	require.Error(t, err)
}

func TestUpdateSettings_StoresAndPrewarmsAccountSchedulingThresholds(t *testing.T) {
	svc, repo := newSettingServiceForPlatformThresholdTest(nil)

	err := svc.UpdateSettings(context.Background(), &SystemSettings{
		AccountSchedulingThresholds: map[string]int{
			PlatformOpenAI:    92,
			PlatformAnthropic: 89,
			PlatformGrok:      76,
		},
	})
	require.NoError(t, err)

	got := svc.parseSettings(map[string]string{
		SettingKeyAccountSchedulingThresholds: repo.data[SettingKeyAccountSchedulingThresholds],
	})
	require.Equal(t, 92, got.AccountSchedulingThresholds[PlatformOpenAI])
	require.Equal(t, 89, got.AccountSchedulingThresholds[PlatformAnthropic])
	require.Equal(t, 76, got.AccountSchedulingThresholds[PlatformGrok])
	require.NotContains(t, got.AccountSchedulingThresholds, "kiro")

	repo.resetValueCalls()
	repo.getValueErr = errors.New("unexpected DB read after cache prewarm")
	gotThresholds := svc.GetAccountSchedulingThresholds(context.Background())
	require.Equal(t, 92, gotThresholds[PlatformOpenAI])
	require.Zero(t, repo.valueCalls(), "successful settings update should prewarm the runtime cache")
}

func TestGetAccountSchedulingThresholds_ReadsStoredValue(t *testing.T) {
	svc, _ := newSettingServiceForPlatformThresholdTest(map[string]string{
		SettingKeyAccountSchedulingThresholds: `{"openai":93,"grok":88,"kiro":87}`,
	})

	got := svc.GetAccountSchedulingThresholds(context.Background())

	require.Equal(t, 93, got[PlatformOpenAI])
	require.Equal(t, 100, got[PlatformAnthropic])
	require.Equal(t, 88, got[PlatformGrok])
	require.NotContains(t, got, "kiro")
}

func TestGetAccountSchedulingThresholds_MissingSettingUsesDefaultsAndNormalCacheTTL(t *testing.T) {
	svc, repo := newSettingServiceForPlatformThresholdTest(nil)
	repo.getValueErr = ErrSettingNotFound

	got := svc.GetAccountSchedulingThresholds(context.Background())
	require.Equal(t, defaultAccountSchedulingThresholds(), got)
	require.Equal(t, 1, repo.valueCalls())

	repo.data[SettingKeyAccountSchedulingThresholds] = `{"openai":91}`
	got = svc.GetAccountSchedulingThresholds(context.Background())
	require.Equal(t, 100, got[PlatformOpenAI], "missing-setting defaults should remain cached for the normal TTL")
	require.Equal(t, 1, repo.valueCalls())

	cached, ok := accountSchedulingThresholdsCache.Load().(*cachedAccountSchedulingThresholds)
	require.True(t, ok)
	require.Greater(t, cached.expiresAt, time.Now().Add(accountSchedulingThresholdsCacheTTL-time.Second).UnixNano())
}

func TestGetAccountSchedulingThresholds_UsesDetachedFiveSecondDBContext(t *testing.T) {
	svc, repo := newSettingServiceForPlatformThresholdTest(nil)
	var (
		observedErr      error
		observedDeadline time.Time
		deadlineOK       bool
	)
	repo.getValueHook = func(ctx context.Context, _ string) (string, error) {
		observedErr = ctx.Err()
		observedDeadline, deadlineOK = ctx.Deadline()
		return "", ErrSettingNotFound
	}

	parent, cancel := context.WithCancel(context.Background())
	cancel()
	got := svc.GetAccountSchedulingThresholds(parent)

	require.Equal(t, defaultAccountSchedulingThresholds(), got)
	require.NoError(t, observedErr, "DB lookup must be detached from caller cancellation")
	require.True(t, deadlineOK)
	remaining := time.Until(observedDeadline)
	require.Greater(t, remaining, 4*time.Second)
	require.LessOrEqual(t, remaining, accountSchedulingThresholdsDBTimeout)
}

func TestGetAccountSchedulingThresholds_CollapsesConcurrentCacheMisses(t *testing.T) {
	svc, repo := newSettingServiceForPlatformThresholdTest(nil)
	repo.getValueHook = func(context.Context, string) (string, error) {
		time.Sleep(25 * time.Millisecond)
		return `{"openai":86}`, nil
	}

	const readers = 12
	start := make(chan struct{})
	results := make(chan map[string]int, readers)
	var wg sync.WaitGroup
	for range readers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- svc.GetAccountSchedulingThresholds(context.Background())
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	for got := range results {
		require.Equal(t, 86, got[PlatformOpenAI])
	}
	require.Equal(t, 1, repo.valueCalls())
}

func TestUpdateSettings_OmittedAccountSchedulingThresholdsDoesNotCacheDefaults(t *testing.T) {
	svc, repo := newSettingServiceForPlatformThresholdTest(map[string]string{
		SettingKeyAccountSchedulingThresholds: `{"openai":85,"grok":88,"kiro":87}`,
	})

	accountSchedulingThresholdsCache.Store(&cachedAccountSchedulingThresholds{
		thresholds: map[string]int{PlatformOpenAI: 71},
		expiresAt:  time.Now().Add(time.Hour).UnixNano(),
	})
	err := svc.UpdateSettings(context.Background(), &SystemSettings{
		FrontendURL: "https://example.test",
	})
	require.NoError(t, err)

	repo.resetValueCalls()
	got := svc.GetAccountSchedulingThresholds(context.Background())
	require.Equal(t, 85, got[PlatformOpenAI])
	require.Equal(t, 88, got[PlatformGrok])
	require.NotContains(t, got, "kiro")
	require.Equal(t, 1, repo.valueCalls(), "omitted threshold payload should force a DB reload")
}

func TestAccountSchedulingThresholds_InvalidStoredValueUsesSameDefaultsInSettingsAndCache(t *testing.T) {
	svc, _ := newSettingServiceForPlatformThresholdTest(map[string]string{
		SettingKeyAccountSchedulingThresholds: `{"openai":0,"grok":88,"kiro":87}`,
	})

	settings := svc.parseSettings(map[string]string{
		SettingKeyAccountSchedulingThresholds: `{"openai":0,"grok":88,"kiro":87}`,
	})
	cached := svc.GetAccountSchedulingThresholds(context.Background())

	require.Equal(t, settings.AccountSchedulingThresholds, cached)
	require.Equal(t, 100, cached[PlatformOpenAI])
	require.Equal(t, 88, cached[PlatformGrok])
	require.NotContains(t, cached, "kiro")
}

func TestGetAccountSchedulingThresholds_NilRepoReturnsDefaults(t *testing.T) {
	svc := &SettingService{}
	got := svc.GetAccountSchedulingThresholds(context.Background())
	require.Equal(t, map[string]int{
		PlatformOpenAI:    100,
		PlatformAnthropic: 100,
		PlatformGrok:      100,
	}, got)
}

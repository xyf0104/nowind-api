package service

import (
	"context"
	"encoding/json"
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenAIWeeklyStateRevisionPreparation(t *testing.T) {
	baseline := map[string]any{"version": 15, "observed_at": "2026-09-06T00:00:00Z"}
	patch := map[string]any{openAIWeeklyEstimateBaselineKey: baseline}
	for _, tc := range []struct {
		name  string
		extra map[string]any
		want  int64
	}{
		{name: "initial", want: 1},
		{name: "existing v15 before fence", extra: map[string]any{openAIWeeklyEstimateBaselineKey: baseline}, want: 1},
		{name: "decoded revision", extra: map[string]any{OpenAIWeeklyStateRevisionKey: 7.0}, want: 8},
		{name: "integer JSON revision", extra: map[string]any{OpenAIWeeklyStateRevisionKey: json.RawMessage(`7.000`)}, want: 8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := PrepareOpenAIWeeklyStateUpdate(tc.extra, patch)
			require.NoError(t, err)
			require.Equal(t, tc.want, got[OpenAIWeeklyStateRevisionKey])
			again, err := PrepareOpenAIWeeklyStateUpdate(tc.extra, got)
			require.NoError(t, err)
			require.Equal(t, got, again, "service and repository preparation must be idempotent")
			require.NotContains(t, patch, OpenAIWeeklyStateRevisionKey)
		})
	}
	legacy := map[string]any{openAIWeeklyEstimateBaselineKey: map[string]any{"version": 14}}
	got, err := PrepareOpenAIWeeklyStateUpdate(nil, legacy)
	require.NoError(t, err)
	require.Equal(t, legacy, got)
	got, err = PrepareOpenAIWeeklyStateUpdate(map[string]any{OpenAIWeeklyStateRevisionKey: 9}, nil)
	require.NoError(t, err)
	require.Nil(t, got, "read-only/no-op reductions must not advance revision")
}

func TestOpenAIWeeklyStateRevisionRejectsMalformedAndForgedCounters(t *testing.T) {
	patch := map[string]any{openAIWeeklyEstimateBaselineKey: map[string]any{"version": 15}}
	for _, bad := range []any{nil, "7", -1, 1.5, math.NaN(), math.Inf(1),
		json.RawMessage(`9007199254740991.1`), openAIWeeklyStateMaxRevision, json.RawMessage(`9223372036854775807`)} {
		got, err := PrepareOpenAIWeeklyStateUpdate(map[string]any{OpenAIWeeklyStateRevisionKey: bad}, patch)
		require.Nil(t, got)
		require.ErrorIs(t, err, ErrOpenAIWeeklyStateInvalidInput)
	}
	for _, forged := range []any{nil, 7, 9, "8", 8.5} {
		got, err := PrepareOpenAIWeeklyStateUpdate(map[string]any{OpenAIWeeklyStateRevisionKey: 7},
			map[string]any{openAIWeeklyEstimateBaselineKey: patch[openAIWeeklyEstimateBaselineKey], OpenAIWeeklyStateRevisionKey: forged})
		require.Nil(t, got)
		require.ErrorIs(t, err, ErrOpenAIWeeklyStateInvalidInput)
	}
}

func TestOpenAIWeeklyStateRevisionUsesOriginalJoinRepairCAS(t *testing.T) {
	for _, applied := range []bool{false, true} {
		svc, repo, account, progress, observed := weeklyJoinRestoreFixture()
		repo.applied = applied
		account.Extra[OpenAIWeeklyStateRevisionKey] = 7.0
		before, err := json.Marshal(account.Extra)
		require.NoError(t, err)
		svc.setOpenAIWeeklyFrozenEstimate(account, progress, 853.5, 860, true, observed, context.Background())
		require.Same(t, account, repo.expected)
		require.Equal(t, int64(8), repo.updates[OpenAIWeeklyStateRevisionKey])
		require.Equal(t, 1, repo.calls)
		if applied {
			require.Equal(t, int64(8), account.Extra[OpenAIWeeklyStateRevisionKey])
			require.NotNil(t, progress.WeeklyEstimateUSD)
			require.InDelta(t, 853.5/34*100, *progress.WeeklyEstimateUSD, 1e-8)
		} else {
			after, err := json.Marshal(account.Extra)
			require.NoError(t, err)
			require.JSONEq(t, string(before), string(after))
			require.Nil(t, progress.WeeklyEstimateUSD)
		}
	}
}

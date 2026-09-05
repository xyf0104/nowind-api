//go:build unit

package service

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// TestCalculateCost_RateMultiplier_NegativeClampedToZero 锁定负数倍率被
// 钳制为 0（而非历史上的 1.0），避免配置异常导致静默按标准价扣费。
func TestCalculateCost_RateMultiplier_NegativeClampedToZero(t *testing.T) {
	svc := newTestBillingService()
	tokens := UsageTokens{InputTokens: 1000, OutputTokens: 500}

	tests := []struct {
		name       string
		multiplier float64
		wantRatio  float64 // ActualCost / TotalCost
	}{
		{"negative clamped to 0", -1.5, 0},
		{"zero passes through as 0 (defense in depth)", 0, 0},
		{"positive 2x applied", 2.0, 2.0},
		{"positive 0.5x applied", 0.5, 0.5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cost, err := svc.CalculateCost("claude-sonnet-4", tokens, tt.multiplier)
			require.NoError(t, err)
			require.Greater(t, cost.TotalCost, 0.0, "TotalCost should be non-zero")
			require.InDelta(t, tt.wantRatio*cost.TotalCost, cost.ActualCost, 1e-9)
		})
	}
}

// TestCalculateImageCost_RateMultiplier_NegativeClampedToZero 图片按次计费路径
// 同样遵循"负数 → 0"语义。
func TestCalculateImageCost_RateMultiplier_NegativeClampedToZero(t *testing.T) {
	svc := newTestBillingService()
	price := 0.04
	cfg := &ImagePriceConfig{Price1K: &price}

	tests := []struct {
		name       string
		multiplier float64
		wantRatio  float64
	}{
		{"negative clamped to 0", -0.5, 0},
		{"zero passes through", 0, 0},
		{"positive 3x applied", 3.0, 3.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cost := svc.CalculateImageCost("imagen-3", "1K", 2, cfg, tt.multiplier)
			require.NotNil(t, cost)
			require.Greater(t, cost.TotalCost, 0.0)
			require.InDelta(t, tt.wantRatio*cost.TotalCost, cost.ActualCost, 1e-9)
		})
	}
}

func TestComputeTokenBreakdown_GPT6AstraConfirmedPriceAndDiscount(t *testing.T) {
	svc := NewBillingService(&config.Config{}, nil)
	tokens := UsageTokens{
		InputTokens:         1_000_000,
		OutputTokens:        100_000,
		CacheCreationTokens: 200_000,
		CacheReadTokens:     300_000,
	}

	// Resolve through production pricing so the regression test cannot drift
	// away from the actual billing path.
	astraPricing, err := svc.GetModelPricing("openai/gpt-6-astra-2026-09-01")
	require.NoError(t, err)
	astra := svc.computeTokenBreakdown(astraPricing, tokens, 1, "", false)

	want := float64(tokens.InputTokens)*10e-6 +
		float64(tokens.OutputTokens)*50e-6 +
		float64(tokens.CacheCreationTokens)*12.5e-6 +
		float64(tokens.CacheReadTokens)*1e-6
	require.InDelta(t, want, astra.TotalCost, 1e-12)
	require.InDelta(t, astra.TotalCost, astra.ActualCost, 1e-12)

	// The account/group multiplier is a separate layer. It changes the actual
	// charge but must not change the model's standard cost or its 2.5x ratio.
	astraDiscounted := svc.computeTokenBreakdown(astraPricing, tokens, 0.178, "", false)
	require.InDelta(t, astra.TotalCost, astraDiscounted.TotalCost, 1e-12)
	require.InDelta(t, astra.TotalCost*0.178, astraDiscounted.ActualCost, 1e-12)
}

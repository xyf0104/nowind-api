//go:build unit

package service

import (
	"testing"

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

func TestComputeTokenBreakdown_GPT6AstraPricesAre2_5xGPT56Sol(t *testing.T) {
	svc := &BillingService{}
	tokens := UsageTokens{
		InputTokens:         1_000_000,
		OutputTokens:        100_000,
		CacheCreationTokens: 200_000,
		CacheReadTokens:     300_000,
	}

	// These are the configured per-million-token prices shown in the admin
	// pricing cards, expressed in the USD-per-token unit used by BillingService.
	sol := svc.computeTokenBreakdown(&ModelPricing{
		InputPricePerToken:         4e-6,
		OutputPricePerToken:        20e-6,
		CacheCreationPricePerToken: 5e-6,
		CacheReadPricePerToken:     0.4e-6,
	}, tokens, 1, "", false)
	astra := svc.computeTokenBreakdown(&ModelPricing{
		InputPricePerToken:         10e-6,
		OutputPricePerToken:        50e-6,
		CacheCreationPricePerToken: 12.5e-6,
		CacheReadPricePerToken:     1e-6,
	}, tokens, 1, "", false)

	require.InDelta(t, sol.TotalCost*2.5, astra.TotalCost, 1e-12)
	require.InDelta(t, astra.TotalCost, astra.ActualCost, 1e-12)

	// The account/group multiplier is a separate layer. It changes the actual
	// charge but must not change the model's standard cost or its 2.5x ratio.
	astraDiscounted := svc.computeTokenBreakdown(&ModelPricing{
		InputPricePerToken:         10e-6,
		OutputPricePerToken:        50e-6,
		CacheCreationPricePerToken: 12.5e-6,
		CacheReadPricePerToken:     1e-6,
	}, tokens, 0.178, "", false)
	require.InDelta(t, astra.TotalCost, astraDiscounted.TotalCost, 1e-12)
	require.InDelta(t, astra.TotalCost*0.178, astraDiscounted.ActualCost, 1e-12)
}

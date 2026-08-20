package dto

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestGroupFromServiceAdminIncludesGroupModelPricing(t *testing.T) {
	pricing := []service.ChannelModelPricing{{
		Platform:    service.PlatformOpenAI,
		Models:      []string{"gpt-5.6-terra"},
		BillingMode: service.BillingModeToken,
	}}

	admin := GroupFromServiceAdmin(&service.Group{
		LongContextPricingEnabled: true,
		ModelPricing:              pricing,
	})

	require.True(t, admin.LongContextPricingEnabled)
	require.Equal(t, pricing, admin.ModelPricing)
}

package repository

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestMarshalGroupModelPricingUsesEmptyArrayForUnsetPricing(t *testing.T) {
	payload, err := marshalGroupModelPricing(nil)
	require.NoError(t, err)
	require.JSONEq(t, `[]`, string(payload))
}

func TestMarshalGroupModelPricingPreservesStaticPricingEntries(t *testing.T) {
	price := 0.00001
	payload, err := marshalGroupModelPricing([]service.ChannelModelPricing{{
		Platform:    service.PlatformOpenAI,
		Models:      []string{"gpt-5.6-terra"},
		BillingMode: service.BillingModeToken,
		InputPrice:  &price,
	}})
	require.NoError(t, err)

	var decoded []service.ChannelModelPricing
	require.NoError(t, json.Unmarshal(payload, &decoded))
	require.Len(t, decoded, 1)
	require.Equal(t, service.PlatformOpenAI, decoded[0].Platform)
	require.Equal(t, []string{"gpt-5.6-terra"}, decoded[0].Models)
	require.Equal(t, service.BillingModeToken, decoded[0].BillingMode)
	require.NotNil(t, decoded[0].InputPrice)
	require.InDelta(t, price, *decoded[0].InputPrice, 1e-12)
}

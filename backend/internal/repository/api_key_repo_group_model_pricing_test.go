package repository

import (
	"encoding/json"
	"testing"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/stretchr/testify/require"
)

func TestGroupEntityToServiceLoadsGroupModelPricing(t *testing.T) {
	entity := &dbent.Group{
		ID:                        42,
		LongContextPricingEnabled: true,
		ModelPricing: json.RawMessage(`[
			{"platform":"openai","models":["gpt-5.6-terra"],"billing_mode":"token"}
		]`),
	}

	result := groupEntityToService(entity)

	require.True(t, result.LongContextPricingEnabled)
	require.Len(t, result.ModelPricing, 1)
	require.Equal(t, "openai", result.ModelPricing[0].Platform)
	require.Equal(t, []string{"gpt-5.6-terra"}, result.ModelPricing[0].Models)
}

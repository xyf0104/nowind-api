package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolveBillingServiceTierOnlyLowersCost(t *testing.T) {
	tests := []struct {
		name       string
		requested  string
		observed   string
		billing    string
		downgraded bool
	}{
		{name: "priority served as default", requested: "priority", observed: "default", billing: "default", downgraded: true},
		{name: "fast served as standard", requested: "fast", observed: "standard", billing: "standard", downgraded: true},
		{name: "flex is never raised", requested: "flex", observed: "default", billing: "flex"},
		{name: "untiered request stays untiered", observed: "priority", billing: ""},
		{name: "unknown response is ignored", requested: "priority", observed: "turbo", billing: "priority"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := ResolveBillingServiceTier(test.requested, test.observed)
			require.Equal(t, test.billing, got.Billing)
			require.Equal(t, test.downgraded, got.Downgraded)
		})
	}
}

func TestOpenAIServiceTierBillingHonorsCredentialProtocol(t *testing.T) {
	priority := "priority"

	apiKeyResult := &OpenAIForwardResult{
		ServiceTier:                 &priority,
		UpstreamResponseServiceTier: "default",
	}
	resolution := ApplyOpenAIServiceTierBillingResolution(
		&Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey},
		apiKeyResult,
	)
	require.True(t, resolution.Downgraded)
	require.Equal(t, "default", *apiKeyResult.ServiceTier)

	oauthResult := &OpenAIForwardResult{
		ServiceTier:                 &priority,
		UpstreamResponseServiceTier: "default",
	}
	resolution = ApplyOpenAIServiceTierBillingResolution(
		&Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth},
		oauthResult,
	)
	require.False(t, resolution.Downgraded)
	require.Equal(t, "priority", *oauthResult.ServiceTier)

	oauthFlexResult := &OpenAIForwardResult{
		ServiceTier:                 &priority,
		UpstreamResponseServiceTier: "flex",
	}
	resolution = ApplyOpenAIServiceTierBillingResolution(
		&Account{Platform: PlatformOpenAI, Type: AccountTypeSetupToken},
		oauthFlexResult,
	)
	require.True(t, resolution.Downgraded)
	require.Equal(t, "flex", *oauthFlexResult.ServiceTier)
}

func TestForwardServiceTierBillingUsesObservedTier(t *testing.T) {
	priority := "priority"
	result := &ForwardResult{
		ServiceTier:                 &priority,
		UpstreamResponseServiceTier: "standard",
	}
	require.True(t, ApplyForwardServiceTierBillingResolution(result).Downgraded)
	require.Equal(t, "standard", *result.ServiceTier)
}

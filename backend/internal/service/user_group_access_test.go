package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUserCanBindGroup_PublicRestriction(t *testing.T) {
	user := &User{AllowedGroups: []int64{20, 30}}

	require.True(t, user.CanBindGroup(10, false), "public groups stay available by default")
	require.True(t, user.CanBindGroup(20, true), "exclusive grants keep their existing behavior")
	require.False(t, user.CanBindGroup(21, true))

	user.RestrictPublicGroups = true
	require.True(t, user.CanBindGroup(20, false), "selected public groups remain available")
	require.False(t, user.CanBindGroup(10, false), "unselected public groups are hidden")
	require.True(t, user.CanBindGroup(30, true), "exclusive grants remain available in restricted mode")
}

func TestAPIKeyServiceFiltersHiddenPublicGroupFromAvailableGroups(t *testing.T) {
	apiKeyService := &APIKeyService{}
	user := &User{
		RestrictPublicGroups: true,
		AllowedGroups:        []int64{11},
	}

	require.True(t, apiKeyService.canUserBindGroupInternal(
		user,
		&Group{ID: 11, SubscriptionType: SubscriptionTypeStandard},
		nil,
	))
	require.False(t, apiKeyService.canUserBindGroupInternal(
		user,
		&Group{ID: 12, SubscriptionType: SubscriptionTypeStandard},
		nil,
	), "hidden public groups must not reach group/model-price APIs")
}

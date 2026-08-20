//go:build !unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// groupModelPricingRepositoryStub embeds the full repository contract and only
// implements the calls exercised by this focused service test.
type groupModelPricingRepositoryStub struct {
	GroupRepository
	created *Group
	updated *Group
	current *Group
}

func (s *groupModelPricingRepositoryStub) Create(_ context.Context, group *Group) error {
	group.ID = 99
	s.created = group
	return nil
}

func (s *groupModelPricingRepositoryStub) GetByID(_ context.Context, _ int64) (*Group, error) {
	return s.current, nil
}

func (s *groupModelPricingRepositoryStub) Update(_ context.Context, group *Group) error {
	s.updated = group
	return nil
}

func TestAdminService_GroupModelPricingLifecycle(t *testing.T) {
	price := 0.00001
	repo := &groupModelPricingRepositoryStub{}
	svc := &adminServiceImpl{groupRepo: repo}

	created, err := svc.CreateGroup(context.Background(), &CreateGroupInput{
		Name:           "group-model-pricing-default",
		Platform:       PlatformOpenAI,
		RateMultiplier: 1,
		ModelPricing: []ChannelModelPricing{{
			ID:         99,
			ChannelID:  88,
			Models:     []string{" gpt-5.6-terra "},
			InputPrice: &price,
		}},
	})
	require.NoError(t, err)
	require.True(t, created.LongContextPricingEnabled)
	require.Len(t, created.ModelPricing, 1)
	require.Zero(t, created.ModelPricing[0].ID)
	require.Zero(t, created.ModelPricing[0].ChannelID)
	require.Equal(t, PlatformOpenAI, created.ModelPricing[0].Platform)
	require.Equal(t, []string{"gpt-5.6-terra"}, created.ModelPricing[0].Models)

	repo.current = created
	disabled := false
	replacement := []ChannelModelPricing{{Models: []string{"gpt-5.6-terra"}, InputPrice: &price}}
	updated, err := svc.UpdateGroup(context.Background(), created.ID, &UpdateGroupInput{
		LongContextPricingEnabled: &disabled,
		ModelPricing:              &replacement,
	})
	require.NoError(t, err)
	require.False(t, updated.LongContextPricingEnabled)
	require.Len(t, updated.ModelPricing, 1)
	require.Equal(t, PlatformOpenAI, updated.ModelPricing[0].Platform)
	require.NotNil(t, repo.updated)

	empty := []ChannelModelPricing{}
	updated, err = svc.UpdateGroup(context.Background(), created.ID, &UpdateGroupInput{ModelPricing: &empty})
	require.NoError(t, err)
	require.False(t, updated.LongContextPricingEnabled)
	require.Empty(t, updated.ModelPricing)
}

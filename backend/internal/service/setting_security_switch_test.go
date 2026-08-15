//go:build unit

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type securitySwitchSettingRepoStub struct {
	SettingRepository
	value string
	err   error
}

func (s *securitySwitchSettingRepoStub) GetValue(context.Context, string) (string, error) {
	return s.value, s.err
}

func TestSecuritySwitchReadsPreserveMissingDefaultAndFailClosedOnOperationalError(t *testing.T) {
	tests := []struct {
		name string
		read func(*SettingService) (bool, error)
	}{
		{
			name: "totp",
			read: func(s *SettingService) (bool, error) {
				return s.IsTotpEnabled(context.Background())
			},
		},
		{
			name: "step-up",
			read: func(s *SettingService) (bool, error) {
				return s.IsStepUpEnabled(context.Background())
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Run("explicit false disables", func(t *testing.T) {
				svc := NewSettingService(&securitySwitchSettingRepoStub{value: "false"}, nil)
				enabled, err := test.read(svc)
				require.NoError(t, err)
				require.False(t, enabled)
			})

			t.Run("true enables", func(t *testing.T) {
				svc := NewSettingService(&securitySwitchSettingRepoStub{value: "true"}, nil)
				enabled, err := test.read(svc)
				require.NoError(t, err)
				require.True(t, enabled)
			})

			t.Run("unexpected value stays closed against downgrade", func(t *testing.T) {
				svc := NewSettingService(&securitySwitchSettingRepoStub{value: "invalid"}, nil)
				enabled, err := test.read(svc)
				require.NoError(t, err)
				require.True(t, enabled)
			})

			t.Run("missing setting keeps compatibility default disabled", func(t *testing.T) {
				svc := NewSettingService(&securitySwitchSettingRepoStub{err: ErrSettingNotFound}, nil)
				enabled, err := test.read(svc)
				require.NoError(t, err)
				require.False(t, enabled)
			})

			t.Run("operational storage error is service unavailable", func(t *testing.T) {
				storageErr := errors.New("database unavailable")
				svc := NewSettingService(&securitySwitchSettingRepoStub{err: storageErr}, nil)
				enabled, err := test.read(svc)
				require.False(t, enabled)
				require.ErrorIs(t, err, ErrServiceUnavailable)
				require.ErrorIs(t, err, storageErr)
			})
		})
	}
}

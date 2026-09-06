//go:build unit

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestRemoteAccountManagementRequiresKnownOfflineAndCurrentPermission(t *testing.T) {
	for _, tc := range []struct {
		name    string
		health  map[string]bool
		choice  string
		readErr error
		want    string
	}{
		{"healthy", map[string]bool{"api": true}, "true", nil, "ACCOUNT_REMOTE_NODE_READ_ONLY"},
		{"unknown", map[string]bool{}, "true", nil, "ACCOUNT_REMOTE_NODE_STATUS_UNAVAILABLE"},
		{"disabled", map[string]bool{"api": false}, "false", nil, "ACCOUNT_REMOTE_NODE_TAKEOVER_DISABLED"},
		{"explicitly enabled", map[string]bool{"api": false}, "true", nil, ""},
		{"policy unavailable", map[string]bool{"api": false}, "true", errors.New("database unavailable"), "ACCOUNT_REMOTE_NODE_STATUS_UNAVAILABLE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			settings := executionNodeAdminAccessService("api2", false, tc.choice)
			settings.SetExecutionNodeHealthReader(executionNodeAdminAccessHealth{values: tc.health})
			// An old routing cache may stay available during database failure.
			settings.executionNodeRoutingCache.Store(&cachedExecutionNodeRoutingSettings{
				settings:  ExecutionNodeRoutingSettings{Available: true, EmergencyLocalEgress: true},
				expiresAt: time.Now().Add(time.Minute).UnixNano(),
			})
			if tc.readErr != nil {
				settings.settingRepo = executionNodeAdminAccessSettingsError{SettingRepository: settings.settingRepo, err: tc.readErr}
			}
			svc := &adminServiceImpl{settingService: settings}
			account := &Account{ID: 7, Name: "remote", Extra: map[string]any{"xiass_execution_node_id": "api"}}
			err := svc.ensureAccountManagementAccess(context.Background(), account)
			if tc.want == "" {
				require.NoError(t, err)
			} else {
				require.Equal(t, tc.want, infraerrors.Reason(err))
			}
		})
	}
}

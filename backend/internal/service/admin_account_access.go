package service

import (
	"context"
	"net/http"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// legacyExecutionNodeID is the compatibility owner for accounts created before
// XIASS started persisting an explicit execution-node marker.
func (s *adminServiceImpl) legacyExecutionNodeID() string {
	if s != nil && s.settingService != nil && s.settingService.cfg != nil {
		if value := strings.TrimSpace(s.settingService.cfg.Gateway.ExecutionNode.LegacyUnassignedNodeID); value != "" {
			return value
		}
	}
	return "api"
}

// CheckAccountManagementAccess enforces the shared-pool ownership boundary for
// every admin endpoint that has a side effect. Read-only account and usage
// queries deliberately do not call this method.
func (s *adminServiceImpl) CheckAccountManagementAccess(ctx context.Context, accountID int64) error {
	if !s.executionNodeManagementAccessEnforced() {
		return nil
	}
	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return err
	}
	return s.ensureAccountManagementAccess(ctx, account)
}

func (s *adminServiceImpl) executionNodeManagementAccessEnforced() bool {
	return s != nil && s.settingService != nil && s.settingService.cfg != nil && s.settingService.cfg.Gateway.ExecutionNode.Enabled
}

func (s *adminServiceImpl) ensureAccountManagementAccessByID(ctx context.Context, accountID int64) error {
	if accountID <= 0 {
		return infraerrors.BadRequest("INVALID_ACCOUNT_ID", "account id must be positive")
	}
	return s.CheckAccountManagementAccess(ctx, accountID)
}

func findAccountByID(accounts []*Account, accountID int64) (*Account, bool) {
	for _, account := range accounts {
		if account != nil && account.ID == accountID {
			return account, true
		}
	}
	return nil, false
}

func (s *adminServiceImpl) ensureAccountManagementAccess(ctx context.Context, account *Account) error {
	if account == nil || s == nil || s.settingService == nil || s.settingService.cfg == nil || s.settingService.settingRepo == nil {
		return nil
	}
	cfg := s.settingService.cfg.Gateway.ExecutionNode
	if !cfg.Enabled {
		return nil
	}
	localNodeID := strings.TrimSpace(cfg.ID)
	if !validExecutionNodeID(localNodeID) {
		return infraerrors.ServiceUnavailable("ACCOUNT_NODE_ACCESS_UNAVAILABLE", "the local execution node identity is not ready")
	}
	ownerNodeID := account.ExecutionNodeID(s.legacyExecutionNodeID())
	if ownerNodeID == localNodeID {
		return nil
	}

	reader := s.settingService.executionNodeHealthReader
	if reader == nil {
		return infraerrors.ServiceUnavailable("ACCOUNT_REMOTE_NODE_STATUS_UNAVAILABLE", "the shared node heartbeat is unavailable; remote accounts remain read-only")
	}
	health, err := reader.HealthyExecutionNodes(ctx, []string{ownerNodeID})
	if err != nil {
		return infraerrors.ServiceUnavailable("ACCOUNT_REMOTE_NODE_STATUS_UNAVAILABLE", "the shared node heartbeat is unavailable; remote accounts remain read-only").WithCause(err)
	}
	if health[ownerNodeID] {
		return infraerrors.Newf(http.StatusForbidden, "ACCOUNT_REMOTE_NODE_READ_ONLY", "%s accounts are managed by node %s and are read-only here", account.Name, ownerNodeID)
	}

	// The same shared-state read is used by request routing. If it is stale or
	// invalid, do not turn an uncertain state into a writable takeover.
	settings := s.settingService.GetExecutionNodeRoutingSettings(ctx)
	if !settings.Available {
		return infraerrors.ServiceUnavailable("ACCOUNT_REMOTE_NODE_STATUS_UNAVAILABLE", "the shared node policy is unavailable; remote accounts remain read-only")
	}
	if !settings.EmergencyLocalEgress {
		return infraerrors.Newf(http.StatusForbidden, "ACCOUNT_REMOTE_NODE_TAKEOVER_DISABLED", "%s is offline; enable emergency takeover on this server before managing its accounts", ownerNodeID)
	}
	return nil
}

package service

import (
	"context"
	"strings"
)

// MigrateExecutionNodeDefaultWeights upgrades the shared default only when
// this process is the source node. A joined node can therefore be upgraded
// first during a rolling deployment without changing the live policy.
func (s *SettingService) MigrateExecutionNodeDefaultWeights(ctx context.Context) (bool, error) {
	if s == nil || s.cfg == nil || s.settingRepo == nil || !s.cfg.Gateway.ExecutionNode.Enabled {
		return false, nil
	}
	localNodeID := strings.TrimSpace(s.cfg.Gateway.ExecutionNode.ID)
	sourceNodeID := strings.TrimSpace(s.cfg.Gateway.ExecutionNode.LegacyUnassignedNodeID)
	if !validExecutionNodeID(localNodeID) || !validExecutionNodeID(sourceNodeID) || localNodeID != sourceNodeID {
		return false, nil
	}
	migrator, ok := s.settingRepo.(ExecutionNodeDefaultWeightsMigrator)
	if !ok {
		return false, nil
	}
	migrated, err := migrator.MigrateExecutionNodeDefaultWeights(ctx, sourceNodeID)
	if err != nil || !migrated {
		return migrated, err
	}
	// The setting repository is shared by all instances, so discard the local
	// routing snapshot immediately after the transaction commits.
	s.executionNodeRoutingCache.Store((*cachedExecutionNodeRoutingSettings)(nil))
	if s.onUpdate != nil {
		s.onUpdate()
	}
	return true, nil
}

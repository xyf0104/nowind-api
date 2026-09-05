package service

import (
	"context"
	"strings"
)

// ExecutionNodeAdminWriteAccess reports whether this instance may change
// shared administrative data. The original node (the legacy account owner)
// remains the normal write node. A secondary node becomes writable only when
// the primary heartbeat is definitively offline and its local emergency
// takeover switch is enabled.
//
// Routing weights are intentionally not covered by this decision: they are a
// shared cluster control and are allowed from either node.
func (s *SettingService) ExecutionNodeAdminWriteAccess(ctx context.Context) (bool, string) {
	if s == nil || s.cfg == nil || !s.cfg.Gateway.ExecutionNode.Enabled {
		return true, "single_node"
	}

	localNodeID := strings.TrimSpace(s.cfg.Gateway.ExecutionNode.ID)
	primaryNodeID := strings.TrimSpace(s.cfg.Gateway.ExecutionNode.LegacyUnassignedNodeID)
	if primaryNodeID == "" {
		primaryNodeID = "api"
	}
	if localNodeID == "" || localNodeID == primaryNodeID {
		return true, "primary"
	}

	// Unknown or unreadable shared state must never grant write access to a
	// secondary node. The explicit heartbeat result also prevents a startup or
	// Redis outage from being mistaken for a primary failure.
	if s.settingRepo == nil || s.executionNodeHealthReader == nil {
		return false, "secondary_read_only"
	}
	health, err := s.executionNodeHealthReader.HealthyExecutionNodes(ctx, []string{primaryNodeID})
	if err != nil {
		return false, "secondary_read_only"
	}
	primaryHealthy, known := health[primaryNodeID]
	if !known || primaryHealthy {
		return false, "secondary_read_only"
	}

	takeover := s.cfg.Gateway.ExecutionNode.EmergencyLocalEgress
	if key := executionNodeEmergencyEgressSettingKey(localNodeID); key != "" {
		raw, readErr := s.settingRepo.GetValue(ctx, key)
		if readErr == nil {
			parsed, parseErr := decodeExecutionNodeEmergencyEgress(raw, takeover)
			if parseErr != nil {
				return false, "secondary_read_only"
			}
			takeover = parsed
		}
	}
	if takeover {
		return true, "emergency_takeover"
	}
	return false, "secondary_read_only"
}

func (s *SettingService) CanWriteSharedAdminState(ctx context.Context) bool {
	allowed, _ := s.ExecutionNodeAdminWriteAccess(ctx)
	return allowed
}

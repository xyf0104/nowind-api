package admin

import (
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// parseExecutionNodeFilter accepts the same stable identifier shape used by
// pairing. It is shared by account, usage, dashboard, and snapshot handlers so
// all UI surfaces reject malformed filters consistently.
func parseExecutionNodeFilter(c *gin.Context) (string, error) {
	value := strings.TrimSpace(c.Query("execution_node_id"))
	if value == "" {
		return "", nil
	}
	if len(value) > 64 {
		return "", infraerrors.BadRequest("INVALID_EXECUTION_NODE_FILTER", "execution node filter is invalid")
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' {
			continue
		}
		return "", infraerrors.BadRequest("INVALID_EXECUTION_NODE_FILTER", "execution node filter is invalid")
	}
	return value, nil
}

func filterAccountsByExecutionNode(accounts []service.Account, executionNodeID string) []service.Account {
	if strings.TrimSpace(executionNodeID) == "" {
		return accounts
	}
	filtered := make([]service.Account, 0, len(accounts))
	for i := range accounts {
		if accounts[i].ExecutionNodeID("") == executionNodeID {
			filtered = append(filtered, accounts[i])
		}
	}
	return filtered
}

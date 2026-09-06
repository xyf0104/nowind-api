package middleware

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type executionNodeLegacyContextKey struct{}

// ExecutionNodeReadContext supplies deployment-owned metadata, not a
// client-supplied filter. Both paired panels must resolve unmarked accounts
// against the source node even when their local node IDs differ.
func ExecutionNodeReadContext(settings *service.SettingService) gin.HandlerFunc {
	legacyID := settings.LegacyExecutionNodeID()
	return func(c *gin.Context) {
		ctx := context.WithValue(c.Request.Context(), executionNodeLegacyContextKey{}, legacyID)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

func ExecutionNodeLegacyID(ctx context.Context) string {
	if value, ok := ctx.Value(executionNodeLegacyContextKey{}).(string); ok && value != "" {
		return value
	}
	return "api"
}

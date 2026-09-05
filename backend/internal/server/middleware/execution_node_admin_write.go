package middleware

import (
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// ExecutionNodeSharedWriteGuard protects shared business configuration on a
// secondary node. Read requests remain available, while the primary node (or
// an explicitly enabled emergency takeover) is allowed to mutate it.
// Execution-node pairing and routing controls are deliberately outside this
// guard because weight changes must be possible from either connected node.
func ExecutionNodeSharedWriteGuard(settingService *service.SettingService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if settingService == nil || isReadOnlyHTTPMethod(c.Request.Method) || settingService.CanWriteSharedAdminState(c.Request.Context()) {
			c.Next()
			return
		}
		AbortWithError(c, http.StatusForbidden, "EXECUTION_NODE_ADMIN_READ_ONLY", "This machine is read-only for shared groups, prices, and customer configuration while the primary machine is online")
	}
}

func isReadOnlyHTTPMethod(method string) bool {
	switch strings.ToUpper(strings.TrimSpace(method)) {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	default:
		return false
	}
}

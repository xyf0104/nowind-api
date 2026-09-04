package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/gin-gonic/gin"
)

// RegisterExecutionNodePairingRoutes registers the single invite-authenticated
// peer handshake. It intentionally lives outside the admin group: the calling
// instance is already proving authorization with the one-time invite token.
func RegisterExecutionNodePairingRoutes(v1 *gin.RouterGroup, h *handler.Handlers) {
	if h == nil || h.ExecutionNodePairing == nil {
		return
	}
	internal := v1.Group("/internal/execution-nodes")
	internal.POST("/pair", h.ExecutionNodePairing.Accept)
	internal.POST("/pairing/finalize", h.ExecutionNodePairing.Finalize)
	internal.GET("/tunnel/:kind", h.ExecutionNodePairing.Tunnel)
}

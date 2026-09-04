package handler

import (
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
)

// ExecutionNodePairingHandler is the narrow, invite-authenticated endpoint
// used by another XIASS instance. It is not an admin endpoint and exposes only
// non-secret compatibility metadata.
type ExecutionNodePairingHandler struct {
	settingService *service.SettingService
}

func NewExecutionNodePairingHandler(settingService *service.SettingService) *ExecutionNodePairingHandler {
	return &ExecutionNodePairingHandler{settingService: settingService}
}

func (h *ExecutionNodePairingHandler) Accept(c *gin.Context) {
	token := c.GetHeader("X-XIASS-Execution-Node-Invite")
	if token == "" {
		response.Unauthorized(c, "pairing invite is required")
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64*1024)
	var req service.ExecutionNodePairingHandshakeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid pairing request")
		return
	}
	result, err := h.settingService.AcceptExecutionNodePairingHandshake(c.Request.Context(), token, &req)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

func (h *ExecutionNodePairingHandler) Finalize(c *gin.Context) {
	requesterNodeID := c.GetHeader("X-XIASS-Execution-Node-ID")
	var req struct {
		NodeID string `json:"node_id"`
		Proof  string `json:"proof"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.NodeID) == "" {
		response.BadRequest(c, "invalid pairing finalize request")
		return
	}
	if requesterNodeID != "" && requesterNodeID != req.NodeID {
		response.Unauthorized(c, "pairing requester does not match the target node")
		return
	}
	if err := h.settingService.MarkExecutionNodePairingReady(c.Request.Context(), req.NodeID, req.Proof); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"ready": true})
}

// Tunnel relays only the private state endpoints and the source node's own
// egress destinations. Authentication is performed before websocket upgrade,
// and all forwarding is bounded by the already paired proof.
func (h *ExecutionNodePairingHandler) Tunnel(c *gin.Context) {
	kind := strings.TrimSpace(c.Param("kind"))
	requesterNodeID := c.GetHeader("X-XIASS-Execution-Node-ID")
	ownerNodeID := c.GetHeader("X-XIASS-Execution-Owner-Node-ID")
	proof := c.GetHeader("X-XIASS-Execution-Node-Tunnel")
	if err := h.settingService.AuthorizeExecutionNodeTunnel(c.Request.Context(), requesterNodeID, proof, kind, ownerNodeID); err != nil {
		response.Unauthorized(c, "execution-node tunnel authorization failed")
		return
	}
	var targetHost string
	var targetPort int
	if kind == "egress" {
		encoded := strings.TrimSpace(c.GetHeader("X-XIASS-Execution-Destination"))
		destination, err := base64.RawURLEncoding.DecodeString(encoded)
		if err != nil || len(destination) > 512 {
			response.BadRequest(c, "execution-node egress destination is invalid")
			return
		}
		var portString string
		targetHost, portString, err = net.SplitHostPort(string(destination))
		if err != nil || strings.TrimSpace(targetHost) == "" {
			response.BadRequest(c, "execution-node egress destination is invalid")
			return
		}
		targetPort, err = strconv.Atoi(portString)
		if err != nil || targetPort <= 0 || targetPort > 65535 {
			response.BadRequest(c, "execution-node egress port is invalid")
			return
		}
	} else {
		var err error
		targetHost, targetPort, err = h.settingService.ExecutionNodeTunnelTarget(kind)
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}
	}
	wsConn, err := coderws.Accept(c.Writer, c.Request, &coderws.AcceptOptions{OriginPatterns: []string{"*"}})
	if err != nil {
		return
	}
	wsConn.SetReadLimit(256 * 1024 * 1024)
	defer wsConn.Close(coderws.StatusNormalClosure, "")
	remote, err := net.DialTimeout("tcp", net.JoinHostPort(targetHost, strconv.Itoa(targetPort)), 10*time.Second)
	if err != nil {
		_ = wsConn.Close(coderws.StatusBadGateway, "source target unavailable")
		return
	}
	defer remote.Close()
	websocketConn := coderws.NetConn(c.Request.Context(), wsConn, coderws.MessageBinary)
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(remote, websocketConn); done <- struct{}{} }()
	go func() { _, _ = io.Copy(websocketConn, remote); done <- struct{}{} }()
	<-done
}

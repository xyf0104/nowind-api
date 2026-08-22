package admin

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

// Team child workflow requests are deliberately narrow. The backend accepts
// only an OpenAI authorization URL produced by its existing OAuth endpoint;
// mailbox access tokens, verification codes, passwords, and browser cookies
// never cross this proxy.
type teamChildWorkflowStartRequest struct {
	SeatEmail   string `json:"seat_email" binding:"required,email"`
	InviteEmail string `json:"invite_email" binding:"required,email"`
	AuthURL     string `json:"auth_url" binding:"required"`
	Confirmed   bool   `json:"confirmed"`
}

// StartTeamChildWorkflow confirms the chosen replacement seat, sends the
// temporary-email invitation through the internal Playwright service, and opens
// the OAuth page in a separate persistent Chromium tab. The service pauses for
// manual external verification and exposes no credentials to XIASS.
// POST /api/v1/admin/openai/team-child/workflows
func (h *OpenAIOAuthHandler) StartTeamChildWorkflow(c *gin.Context) {
	var req teamChildWorkflowStartRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请输入待替换成员、临时邮箱和授权链接")
		return
	}
	req.SeatEmail = strings.TrimSpace(req.SeatEmail)
	req.InviteEmail = strings.TrimSpace(req.InviteEmail)
	req.AuthURL = strings.TrimSpace(req.AuthURL)
	if !req.Confirmed {
		response.BadRequest(c, "请先确认移除成员并发送邀请")
		return
	}
	if strings.EqualFold(req.SeatEmail, req.InviteEmail) {
		response.BadRequest(c, "临时邮箱不能与待替换成员相同")
		return
	}
	if err := validateTeamChildWorkflowAuthURL(req.AuthURL); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	h.teamChildMemberAutomationRequest(c, http.MethodPost, "/workflows", req)
}

// GetTeamChildWorkflow returns only a short-lived progress snapshot. A callback
// URL appears only after the persistent browser has reached it and remains in
// the automation process memory until the existing OAuth import consumes it.
// GET /api/v1/admin/openai/team-child/workflows/:workflow_id
func (h *OpenAIOAuthHandler) GetTeamChildWorkflow(c *gin.Context) {
	workflowID := strings.TrimSpace(c.Param("workflow_id"))
	if !validTeamChildWorkflowID(workflowID) {
		response.BadRequest(c, "工作流 ID 无效")
		return
	}
	h.teamChildMemberAutomationRequest(c, http.MethodGet, "/workflows/"+url.PathEscape(workflowID), nil)
}

// CancelTeamChildWorkflow stops only the pending callback observation. It never
// reverses an already confirmed member removal or invitation, because guessing
// at an external workspace rollback would be more destructive than stopping.
// DELETE /api/v1/admin/openai/team-child/workflows/:workflow_id
func (h *OpenAIOAuthHandler) CancelTeamChildWorkflow(c *gin.Context) {
	workflowID := strings.TrimSpace(c.Param("workflow_id"))
	if !validTeamChildWorkflowID(workflowID) {
		response.BadRequest(c, "工作流 ID 无效")
		return
	}
	h.teamChildMemberAutomationRequest(c, http.MethodDelete, "/workflows/"+url.PathEscape(workflowID), nil)
}

func validTeamChildWorkflowID(value string) bool {
	if len(value) < 16 || len(value) > 128 {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') && r != '_' && r != '-' {
			return false
		}
	}
	return true
}

func validateTeamChildWorkflowAuthURL(raw string) error {
	if len(raw) == 0 || len(raw) > 8192 {
		return fmt.Errorf("授权链接无效")
	}
	parsed, err := url.ParseRequestURI(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return fmt.Errorf("授权链接无效")
	}
	switch strings.ToLower(parsed.Hostname()) {
	case "auth.openai.com", "login.openai.com", "chatgpt.com":
		return nil
	default:
		return fmt.Errorf("授权链接不是受支持的 OpenAI 地址")
	}
}

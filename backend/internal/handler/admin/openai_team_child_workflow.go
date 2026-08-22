package admin

import (
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"unicode"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

// Team child workflow requests are deliberately narrow. The backend accepts
// only an OpenAI authorization URL produced by its existing OAuth endpoint;
// mailbox access tokens, passwords, and browser cookies never cross this
// proxy. A one-time SMS code is forwarded only during the active workflow
// action and is never persisted or written to logs.
type teamChildWorkflowStartRequest struct {
	SeatEmail          string `json:"seat_email" binding:"omitempty,email"`
	InviteEmail        string `json:"invite_email" binding:"required,email"`
	AuthURL            string `json:"auth_url" binding:"required"`
	SeatAlreadyRemoved bool   `json:"seat_already_removed"`
	Confirmed          bool   `json:"confirmed"`
}

type teamChildWorkflowPhoneRequest struct {
	Phone string `json:"phone" binding:"required"`
}

type teamChildWorkflowCodeRequest struct {
	Code string `json:"code" binding:"required"`
}

type teamChildWorkflowRestartOAuthRequest struct {
	AuthURL string `json:"auth_url" binding:"required"`
}

var (
	teamChildWorkflowPhonePattern = regexp.MustCompile(`^\+[1-9][0-9]{7,14}$`)
	teamChildWorkflowCodePattern  = regexp.MustCompile(`^[0-9]{4,8}$`)
)

func normalizeTeamChildWorkflowPhone(value string) (string, error) {
	raw := strings.TrimSpace(value)
	if raw == "" {
		return "", fmt.Errorf("手机号格式无效")
	}

	var normalized strings.Builder
	for _, r := range raw {
		switch {
		case r >= '0' && r <= '9':
			normalized.WriteRune(r)
		case r == '+' && normalized.Len() == 0:
			normalized.WriteRune(r)
		case unicode.IsSpace(r) || strings.ContainsRune("-().", r):
			// The SMS panel may display a readable international number such as
			// "+57 315 1855041". Separators are presentation only; the
			// embedded OAuth page receives the canonical E.164 value below.
		default:
			return "", fmt.Errorf("手机号格式无效")
		}
	}

	result := normalized.String()
	if !teamChildWorkflowPhonePattern.MatchString(result) {
		return "", fmt.Errorf("手机号格式无效")
	}
	return result, nil
}

// StartTeamChildWorkflow confirms the chosen replacement seat, sends the
// temporary-email invitation through the internal Playwright service, and opens
// the OAuth page in a separate persistent Chromium tab. The service pauses for
// manual external verification and exposes no credentials to XIASS.
// POST /api/v1/admin/openai/team-child/workflows
func (h *OpenAIOAuthHandler) StartTeamChildWorkflow(c *gin.Context) {
	if !requireTeamChildAdminSession(c) {
		return
	}
	var req teamChildWorkflowStartRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请输入临时邮箱和授权链接")
		return
	}
	req.SeatEmail = strings.TrimSpace(req.SeatEmail)
	req.InviteEmail = strings.TrimSpace(req.InviteEmail)
	req.AuthURL = strings.TrimSpace(req.AuthURL)
	if !req.Confirmed {
		response.BadRequest(c, "请先确认成员操作并发送邀请")
		return
	}
	if req.SeatAlreadyRemoved {
		if req.SeatEmail != "" {
			response.BadRequest(c, "人工腾位工作流不能携带待移除成员")
			return
		}
	} else if req.SeatEmail == "" {
		response.BadRequest(c, "请选择待替换的普通成员")
		return
	}
	if req.SeatEmail != "" && strings.EqualFold(req.SeatEmail, req.InviteEmail) {
		response.BadRequest(c, "临时邮箱不能与待替换成员相同")
		return
	}
	if req.SeatEmail != "" && isTeamChildProtectedMemberEmail(req.SeatEmail) {
		response.Forbidden(c, "受保护的管理员账号不可替换")
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

// ContinueTeamChildWorkflow rechecks the live Team page after an operator has
// handled an external interruption. The automation service resumes only the
// unfinished stages and never replays completed operations blindly.
// POST /api/v1/admin/openai/team-child/workflows/:workflow_id/continue
func (h *OpenAIOAuthHandler) ContinueTeamChildWorkflow(c *gin.Context) {
	workflowID := strings.TrimSpace(c.Param("workflow_id"))
	if !validTeamChildWorkflowID(workflowID) {
		response.BadRequest(c, "工作流 ID 无效")
		return
	}
	h.teamChildMemberAutomationRequest(c, http.MethodPost, "/workflows/"+url.PathEscape(workflowID)+"/continue", nil)
}

// SubmitTeamChildWorkflowPhone replaces the visible phone field in the current
// OAuth tab after the operator has confirmed a new XIASS SMS session. The
// value is forwarded only to the short-lived browser automation process.
func (h *OpenAIOAuthHandler) SubmitTeamChildWorkflowPhone(c *gin.Context) {
	workflowID := strings.TrimSpace(c.Param("workflow_id"))
	if !validTeamChildWorkflowID(workflowID) {
		response.BadRequest(c, "工作流 ID 无效")
		return
	}
	var req teamChildWorkflowPhoneRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "手机号格式无效")
		return
	}
	phone, err := normalizeTeamChildWorkflowPhone(req.Phone)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	req.Phone = phone
	h.teamChildMemberAutomationRequest(c, http.MethodPost, "/workflows/"+url.PathEscape(workflowID)+"/phone", req)
}

// SubmitTeamChildWorkflowCode sends one newly received SMS code to the active
// OAuth tab. Codes are never stored in the workflow summary or application DB.
func (h *OpenAIOAuthHandler) SubmitTeamChildWorkflowCode(c *gin.Context) {
	workflowID := strings.TrimSpace(c.Param("workflow_id"))
	if !validTeamChildWorkflowID(workflowID) {
		response.BadRequest(c, "工作流 ID 无效")
		return
	}
	var req teamChildWorkflowCodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "验证码格式无效")
		return
	}
	req.Code = strings.TrimSpace(req.Code)
	if !teamChildWorkflowCodePattern.MatchString(req.Code) {
		response.BadRequest(c, "验证码格式无效")
		return
	}
	h.teamChildMemberAutomationRequest(c, http.MethodPost, "/workflows/"+url.PathEscape(workflowID)+"/code", req)
}

// RestartTeamChildWorkflowOAuth opens a fresh standard OAuth page after a
// confirmed SMS cancellation. Member removal, invitation, and mailbox state
// remain untouched; only OAuth/verification state is reset by the automation
// service.
func (h *OpenAIOAuthHandler) RestartTeamChildWorkflowOAuth(c *gin.Context) {
	workflowID := strings.TrimSpace(c.Param("workflow_id"))
	if !validTeamChildWorkflowID(workflowID) {
		response.BadRequest(c, "工作流 ID 无效")
		return
	}
	var req teamChildWorkflowRestartOAuthRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "授权链接无效")
		return
	}
	req.AuthURL = strings.TrimSpace(req.AuthURL)
	if err := validateTeamChildWorkflowAuthURL(req.AuthURL); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	h.teamChildMemberAutomationRequest(c, http.MethodPost, "/workflows/"+url.PathEscape(workflowID)+"/restart-oauth", req)
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

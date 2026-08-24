package admin

import (
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

// Team child workflow requests are deliberately narrow. The backend accepts
// only an OpenAI authorization URL/session produced by its existing OAuth
// endpoint. Browser cookies and generated passwords never cross this proxy;
// mailbox/SMS values are forwarded only to the active short-lived workflow and
// are never persisted by this handler.
type teamChildWorkflowStartRequest struct {
	// Temporary mailbox domains are validated with the same normalized rule as
	// the browser automation, so both workflow entry points accept the same
	// provider-generated address.
	SeatEmail          string `json:"seat_email"`
	InviteEmail        string `json:"invite_email" binding:"required"`
	AuthURL            string `json:"auth_url" binding:"required"`
	OAuthSessionID     string `json:"oauth_session_id" binding:"required"`
	SeatAlreadyRemoved bool   `json:"seat_already_removed"`
	StartStep          string `json:"start_step"`
	RunOnlyStep        bool   `json:"run_only_step"`
	Confirmed          bool   `json:"confirmed"`
}

type teamChildWorkflowCallbackRequest struct {
	CallbackURL string `json:"callback_url" binding:"required"`
}

type teamChildWorkflowRestartOAuthRequest struct {
	AuthURL        string `json:"auth_url" binding:"required"`
	OAuthSessionID string `json:"oauth_session_id" binding:"required"`
}

type teamChildWorkflowCodeRequest struct {
	Code string `json:"code" binding:"required"`
}

type teamChildWorkflowPhoneRequest struct {
	Phone string `json:"phone" binding:"required"`
}

var (
	teamChildWorkflowStartPattern   = regexp.MustCompile(`^(members|remove|invite|oauth|verify)?$`)
	teamChildWorkflowEmailPattern   = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)
	teamChildWorkflowEmbeddedEmail  = regexp.MustCompile(`(?i)[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}`)
	teamChildWorkflowSessionPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{16,128}$`)
	teamChildWorkflowCodePattern    = regexp.MustCompile(`^[0-9]{4,10}$`)
	teamChildWorkflowPhonePattern   = regexp.MustCompile(`^\+[1-9][0-9]{6,14}$`)
)

func normalizeTeamChildWorkflowEmail(value string) string {
	normalized := strings.TrimSpace(strings.ToLower(value))
	if match := teamChildWorkflowEmbeddedEmail.FindString(normalized); match != "" {
		return match
	}
	return normalized
}

func validTeamChildWorkflowEmail(value string) bool {
	return len(value) > 0 && len(value) <= 320 && teamChildWorkflowEmailPattern.MatchString(value)
}

// StartTeamChildWorkflow confirms the chosen replacement seat, sends the
// temporary-email invitation through the internal Playwright service. The
// service then prepares the official OAuth handoff and exposes no external
// credentials or verification values to XIASS.
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
	req.SeatEmail = normalizeTeamChildWorkflowEmail(req.SeatEmail)
	req.InviteEmail = normalizeTeamChildWorkflowEmail(req.InviteEmail)
	req.AuthURL = strings.TrimSpace(req.AuthURL)
	req.OAuthSessionID = strings.TrimSpace(req.OAuthSessionID)
	req.StartStep = strings.TrimSpace(strings.ToLower(req.StartStep))
	if req.InviteEmail == "" || !validTeamChildWorkflowEmail(req.InviteEmail) {
		response.BadRequest(c, "临时邮箱格式无效")
		return
	}
	if req.SeatEmail != "" && !validTeamChildWorkflowEmail(req.SeatEmail) {
		response.BadRequest(c, "成员邮箱格式无效")
		return
	}
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
	if !teamChildWorkflowStartPattern.MatchString(req.StartStep) {
		response.BadRequest(c, "工作流起始步骤无效")
		return
	}
	if err := validateTeamChildWorkflowAuthURL(req.AuthURL); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if !teamChildWorkflowSessionPattern.MatchString(req.OAuthSessionID) {
		response.BadRequest(c, "XIASS OAuth 会话无效")
		return
	}
	h.teamChildMemberAutomationRequest(c, http.MethodPost, "/workflows", req)
}

// RunTeamChildWorkflowStep executes one selected unfinished workflow stage.
// The automation service rechecks all earlier stages against the live browser
// state before it performs the requested stage.
// POST /api/v1/admin/openai/team-child/workflows/:workflow_id/run-step
func (h *OpenAIOAuthHandler) RunTeamChildWorkflowStep(c *gin.Context) {
	workflowID := strings.TrimSpace(c.Param("workflow_id"))
	if !validTeamChildWorkflowID(workflowID) {
		response.BadRequest(c, "工作流 ID 无效")
		return
	}
	var req struct {
		Step string `json:"step" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "步骤无效")
		return
	}
	req.Step = strings.TrimSpace(strings.ToLower(req.Step))
	if !teamChildWorkflowStartPattern.MatchString(req.Step) || req.Step == "" {
		response.BadRequest(c, "步骤无效")
		return
	}
	h.teamChildMemberAutomationRequest(c, http.MethodPost, "/workflows/"+url.PathEscape(workflowID)+"/run-step", req)
}

// GetTeamChildWorkflow returns only a short-lived progress snapshot. A callback
// URL is accepted only after the administrator pastes it and the automation
// service validates its PKCE state.
// GET /api/v1/admin/openai/team-child/workflows/:workflow_id
func (h *OpenAIOAuthHandler) GetTeamChildWorkflow(c *gin.Context) {
	workflowID := strings.TrimSpace(c.Param("workflow_id"))
	if !validTeamChildWorkflowID(workflowID) {
		response.BadRequest(c, "工作流 ID 无效")
		return
	}
	h.teamChildMemberAutomationRequest(c, http.MethodGet, "/workflows/"+url.PathEscape(workflowID), nil)
}

// GetActiveTeamChildWorkflow restores a still-running Team child workflow
// after the admin page is reopened. It returns only the short-lived workflow
// summary; credentials, codes, mailbox tokens, and browser cookies remain in
// their existing automation/browser processes.
func (h *OpenAIOAuthHandler) GetActiveTeamChildWorkflow(c *gin.Context) {
	h.teamChildMemberAutomationRequest(c, http.MethodGet, "/workflows/active", nil)
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

// SubmitTeamChildWorkflowEmailCode forwards a code read by the active XIASS
// mailbox session to the matching short-lived browser workflow.
func (h *OpenAIOAuthHandler) SubmitTeamChildWorkflowEmailCode(c *gin.Context) {
	workflowID, ok := validTeamChildWorkflowRequestID(c)
	if !ok {
		return
	}
	var req teamChildWorkflowCodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "邮箱验证码无效")
		return
	}
	req.Code = strings.ReplaceAll(strings.TrimSpace(req.Code), " ", "")
	if !teamChildWorkflowCodePattern.MatchString(req.Code) {
		response.BadRequest(c, "邮箱验证码无效")
		return
	}
	h.teamChildMemberAutomationRequest(c, http.MethodPost, "/workflows/"+url.PathEscape(workflowID)+"/email-code", req)
}

// SubmitTeamChildWorkflowPhone is called only after PixlabSMSReceiver's XIASS
// in-page confirmation has successfully redeemed a number.
func (h *OpenAIOAuthHandler) SubmitTeamChildWorkflowPhone(c *gin.Context) {
	workflowID, ok := validTeamChildWorkflowRequestID(c)
	if !ok {
		return
	}
	var req teamChildWorkflowPhoneRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "手机号无效")
		return
	}
	req.Phone = strings.NewReplacer(" ", "", "-", "", "(", "", ")", "").Replace(strings.TrimSpace(req.Phone))
	if !teamChildWorkflowPhonePattern.MatchString(req.Phone) {
		response.BadRequest(c, "手机号必须是完整国际格式")
		return
	}
	h.teamChildMemberAutomationRequest(c, http.MethodPost, "/workflows/"+url.PathEscape(workflowID)+"/phone", req)
}

// SubmitTeamChildWorkflowSMSCode forwards only the code returned by the active
// confirmed SMS receiver session.
func (h *OpenAIOAuthHandler) SubmitTeamChildWorkflowSMSCode(c *gin.Context) {
	workflowID, ok := validTeamChildWorkflowRequestID(c)
	if !ok {
		return
	}
	var req teamChildWorkflowCodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "短信验证码无效")
		return
	}
	req.Code = strings.ReplaceAll(strings.TrimSpace(req.Code), " ", "")
	if !teamChildWorkflowCodePattern.MatchString(req.Code) {
		response.BadRequest(c, "短信验证码无效")
		return
	}
	h.teamChildMemberAutomationRequest(c, http.MethodPost, "/workflows/"+url.PathEscape(workflowID)+"/sms-code", req)
}

// CompleteTeamChildWorkflow marks the final node complete only after the
// existing XIASS OAuth account import endpoint has returned successfully.
func (h *OpenAIOAuthHandler) CompleteTeamChildWorkflow(c *gin.Context) {
	workflowID, ok := validTeamChildWorkflowRequestID(c)
	if !ok {
		return
	}
	h.teamChildMemberAutomationRequest(c, http.MethodPost, "/workflows/"+url.PathEscape(workflowID)+"/complete", nil)
}

// RejectTeamChildWorkflowExternalValue keeps the ambiguous legacy code route
// diagnosable without guessing whether a value is an email or SMS code.
func (h *OpenAIOAuthHandler) RejectTeamChildWorkflowExternalValue(c *gin.Context) {
	if !validTeamChildWorkflowID(strings.TrimSpace(c.Param("workflow_id"))) {
		response.BadRequest(c, "工作流 ID 无效")
		return
	}
	response.BadRequest(c, "旧版外部验证码接口已停用，请刷新页面后使用当前自动化工作流")
}

func validTeamChildWorkflowRequestID(c *gin.Context) (string, bool) {
	workflowID := strings.TrimSpace(c.Param("workflow_id"))
	if !validTeamChildWorkflowID(workflowID) {
		response.BadRequest(c, "工作流 ID 无效")
		return "", false
	}
	return workflowID, true
}

// SubmitTeamChildWorkflowCallback records a callback URL pasted by the
// administrator after completing the official OAuth page. It is validated by
// the automation service against the workflow's PKCE state before import.
func (h *OpenAIOAuthHandler) SubmitTeamChildWorkflowCallback(c *gin.Context) {
	workflowID := strings.TrimSpace(c.Param("workflow_id"))
	if !validTeamChildWorkflowID(workflowID) {
		response.BadRequest(c, "工作流 ID 无效")
		return
	}
	var req teamChildWorkflowCallbackRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "回调 URL 无效")
		return
	}
	raw := strings.TrimSpace(req.CallbackURL)
	if len(raw) == 0 || len(raw) > 8192 {
		response.BadRequest(c, "回调 URL 无效")
		return
	}
	parsed, err := url.ParseRequestURI(raw)
	if err != nil || parsed.User != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Query().Get("code") == "" || parsed.Query().Get("state") == "" {
		response.BadRequest(c, "回调 URL 必须包含 code 和 state")
		return
	}
	req.CallbackURL = raw
	h.teamChildMemberAutomationRequest(c, http.MethodPost, "/workflows/"+url.PathEscape(workflowID)+"/callback", req)
}

// RestartTeamChildWorkflowOAuth prepares a fresh official OAuth handoff after
// a confirmed SMS cancellation. Member removal, invitation, and mailbox state
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
	req.OAuthSessionID = strings.TrimSpace(req.OAuthSessionID)
	if err := validateTeamChildWorkflowAuthURL(req.AuthURL); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if !teamChildWorkflowSessionPattern.MatchString(req.OAuthSessionID) {
		response.BadRequest(c, "XIASS OAuth 会话无效")
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
	query := parsed.Query()
	if strings.ToLower(parsed.Hostname()) != "auth.openai.com" ||
		parsed.Path != "/oauth/authorize" ||
		query.Get("response_type") != "code" ||
		query.Get("client_id") != openai.ClientID ||
		query.Get("redirect_uri") != openai.DefaultRedirectURI ||
		query.Get("scope") != openai.DefaultScopes ||
		query.Get("state") == "" ||
		query.Get("code_challenge") == "" ||
		query.Get("code_challenge_method") != "S256" ||
		query.Get("codex_cli_simplified_flow") != "true" ||
		query.Get("id_token_add_organizations") != "true" {
		return fmt.Errorf("授权链接必须使用 XIASS 内置 OpenAI PKCE 登录流程")
	}
	return nil
}

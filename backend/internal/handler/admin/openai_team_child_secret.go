package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

const teamChildSecretBodyLimit = 8 * 1024

type openAITeamChildWorkflowSecret struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// fetchTeamChildWorkflowSecret is the only bridge from the isolated browser
// service into XIASS account storage. It is service-token authenticated,
// protocol pinned, bounded, and never returns the secret through an ordinary
// account response.
func (h *OpenAIOAuthHandler) fetchTeamChildWorkflowSecret(ctx context.Context, workflowID string) (*openAITeamChildWorkflowSecret, error) {
	workflowID = strings.TrimSpace(workflowID)
	if !validTeamChildWorkflowID(workflowID) {
		return nil, fmt.Errorf("invalid Team-child workflow ID")
	}
	config, err := loadTeamChildMemberAutomationConfig()
	if err != nil {
		return nil, err
	}
	serviceToken := strings.TrimSpace(teamChildAutomationServiceToken())
	if serviceToken == "" {
		return nil, fmt.Errorf("Team-child automation token is unavailable")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, config.baseURL+"/workflows/"+url.PathEscape(workflowID)+"/secret", nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("X-XIASS-Team-Child-Token", serviceToken)
	result, err := config.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer result.Body.Close()
	body, err := io.ReadAll(io.LimitReader(result.Body, teamChildSecretBodyLimit+1))
	if err != nil {
		return nil, err
	}
	if len(body) > teamChildSecretBodyLimit {
		return nil, fmt.Errorf("Team-child secret response is too large")
	}
	if result.StatusCode < http.StatusOK || result.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("Team-child secret request returned HTTP %d", result.StatusCode)
	}
	if result.Header.Get(teamChildWorkflowProtocolHeader) != teamChildWorkflowProtocolVersion {
		return nil, fmt.Errorf("Team-child workflow protocol mismatch")
	}
	var secret openAITeamChildWorkflowSecret
	if err := json.Unmarshal(body, &secret); err != nil {
		return nil, err
	}
	secret.Email = normalizeTeamChildWorkflowEmail(secret.Email)
	if !validTeamChildWorkflowEmail(secret.Email) || len(secret.Password) < 8 || len(secret.Password) > 256 {
		return nil, fmt.Errorf("Team-child workflow secret is invalid")
	}
	return &secret, nil
}

func teamChildAutomationServiceToken() string {
	return strings.TrimSpace(os.Getenv("TEAM_CHILD_AUTOMATION_TOKEN"))
}

// RevealTeamChildWorkflowPassword returns a generated password only through a
// step-up protected route. The browser keeps it solely in component memory.
func (h *OpenAIOAuthHandler) RevealTeamChildWorkflowPassword(c *gin.Context) {
	if !requireTeamChildAdminSession(c) {
		return
	}
	workflowID := strings.TrimSpace(c.Param("workflow_id"))
	secret, err := h.fetchTeamChildWorkflowSecret(c.Request.Context(), workflowID)
	if err != nil {
		response.Error(c, http.StatusConflict, "当前工作流登录密码不可用")
		return
	}
	response.Success(c, secret)
}

// RevealTeamChildAccountPassword decrypts only accounts explicitly imported by
// the Team-child workflow. It cannot be used as a generic credential export.
func (h *OpenAIOAuthHandler) RevealTeamChildAccountPassword(c *gin.Context) {
	if !requireTeamChildAdminSession(c) {
		return
	}
	if h == nil || h.adminService == nil || h.secretEncryptor == nil {
		response.InternalError(c, "Team 子号密码服务不可用")
		return
	}
	accountID, err := strconv.ParseInt(strings.TrimSpace(c.Param("account_id")), 10, 64)
	if err != nil || accountID <= 0 {
		response.BadRequest(c, "账号 ID 无效")
		return
	}
	account, err := h.adminService.GetAccount(c.Request.Context(), accountID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	teamChild, _ := account.Extra[service.OpenAITeamChildExtraKey].(bool)
	email, _ := account.Extra[service.OpenAITeamChildEmailExtraKey].(string)
	ciphertext, _ := account.Credentials[service.OpenAITeamChildPasswordCredentialKey].(string)
	email = normalizeTeamChildWorkflowEmail(email)
	if account.Platform != service.PlatformOpenAI || !account.IsOAuth() || !teamChild || !validTeamChildWorkflowEmail(email) || strings.TrimSpace(ciphertext) == "" {
		response.NotFound(c, "该账号没有可查看的 Team 子号登录密码")
		return
	}
	password, err := h.secretEncryptor.Decrypt(ciphertext)
	if err != nil || len(password) < 8 || len(password) > 256 {
		response.InternalError(c, "Team 子号登录密码无法解密")
		return
	}
	response.Success(c, openAITeamChildWorkflowSecret{Email: email, Password: password})
}

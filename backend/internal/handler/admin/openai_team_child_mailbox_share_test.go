package admin

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// teamMailboxShareAdminStub lets a test simulate an imported Team account
// disappearing while preserving the broad account-service stub used elsewhere.
type teamMailboxShareAdminStub struct {
	*stubAdminService
	missing bool
}

func (s *teamMailboxShareAdminStub) GetAccount(ctx context.Context, id int64) (*service.Account, error) {
	if s.missing {
		return nil, service.ErrAccountNotFound
	}
	return s.stubAdminService.GetAccount(ctx, id)
}

// teamMailboxShareTestEncryptor deliberately stores an opaque encoded value so
// persistence assertions exercise the same no-plaintext invariant as the
// production encryptor without requiring production key material in a test.
type teamMailboxShareTestEncryptor struct {
	failDecrypt bool
}

func (e teamMailboxShareTestEncryptor) Encrypt(plaintext string) (string, error) {
	return "mailbox-test:" + base64.RawURLEncoding.EncodeToString([]byte(plaintext)), nil
}

func (e teamMailboxShareTestEncryptor) Decrypt(ciphertext string) (string, error) {
	if e.failDecrypt {
		return "", fmt.Errorf("test decrypt failure")
	}
	encoded, ok := strings.CutPrefix(ciphertext, "mailbox-test:")
	if !ok {
		return "", fmt.Errorf("test ciphertext format is invalid")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return "", err
	}
	return string(decoded), nil
}

func setupTeamMailboxShareRouter(t *testing.T) (*OpenAIOAuthHandler, *teamMailboxShareAdminStub, *gin.Engine, *atomic.Int32, *atomic.Int32, *atomic.Int32) {
	t.Helper()
	t.Setenv(teamMailboxShareFileEnv, filepath.Join(t.TempDir(), "team-mailbox-shares.json"))
	var createCalls atomic.Int32
	var listCalls atomic.Int32
	var detailCalls atomic.Int32
	handler, _ := setupTeamMailboxTestHandler(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/new_address":
			createCalls.Add(1)
			require.Equal(t, http.MethodPost, r.Method)
			require.Equal(t, "provider-api-key", r.Header.Get("X-API-Key"))
			var payload struct {
				Name   string `json:"name"`
				Domain string `json:"domain"`
			}
			require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
			_, _ = fmt.Fprintf(w, `{"address":%q,"jwt":"provider-mailbox-jwt"}`, payload.Name+"@"+payload.Domain)
		case "/api/mails":
			listCalls.Add(1)
			require.Equal(t, http.MethodGet, r.Method)
			require.Equal(t, "Bearer provider-mailbox-jwt", r.Header.Get("Authorization"))
			require.Empty(t, r.Header.Get("X-API-Key"))
			_, _ = w.Write([]byte(`{"messages":[
              {"id":"family-update","to":[{"address":"team1061@example.test"}],"from":{"name":"Family","address":"family@example.test"},"subject":"Family update","text":"Dinner is at 19:00."},
              {"id":"openai-code","to":[{"address":"team1061@example.test"}],"from":"OpenAI <noreply@openai.com>","subject":"OpenAI verification code: 418204","text":"Your verification code is 418204."},
              {"id":"foreign-inbox","to":[{"address":"team9999@example.test"}],"from":"private@example.test","subject":"Must stay private","text":"This message is for another inbox."}
            ]}`))
		case "/api/mail/family-update":
			detailCalls.Add(1)
			require.Equal(t, "Bearer provider-mailbox-jwt", r.Header.Get("Authorization"))
			_, _ = w.Write([]byte(`{"data":{"id":"family-update","to":[{"address":"team1061@example.test"}],"from":{"name":"Family","address":"family@example.test"},"subject":"Family update","text":"Dinner is at 19:00. Please bring dessert."}}`))
		case "/api/mail/openai-code":
			detailCalls.Add(1)
			require.Equal(t, "Bearer provider-mailbox-jwt", r.Header.Get("Authorization"))
			_, _ = w.Write([]byte(`{"data":{"id":"openai-code","to":[{"address":"team1061@example.test"}],"from":"OpenAI <noreply@openai.com>","subject":"OpenAI verification code: 418204","text":"Your verification code is 418204."}}`))
		default:
			t.Fatalf("unexpected provider request: %s", r.URL.Path)
		}
	})

	base := newStubAdminService()
	base.getAccountResult = &service.Account{
		ID:       61,
		Name:     "team1061@example.test",
		Platform: service.PlatformOpenAI,
		Type:     service.AccountTypeOAuth,
		Extra: map[string]any{
			service.OpenAITeamChildExtraKey:      true,
			service.OpenAITeamChildEmailExtraKey: "team1061@example.test",
		},
	}
	adminService := &teamMailboxShareAdminStub{stubAdminService: base}
	handler.adminService = adminService
	handler.ConfigureTeamChildSecrets(teamMailboxShareTestEncryptor{})

	router := gin.New()
	admin := router.Group("/admin")
	admin.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
		c.Set(string(middleware.ContextKeyUserRole), "admin")
		c.Next()
	})
	admin.GET("/team-child/mailbox-share", handler.GetPendingTeamChildMailboxShare)
	admin.POST("/team-child/mailbox-share", handler.CreatePendingTeamChildMailboxShare)
	admin.DELETE("/team-child/mailbox-share", handler.RevokePendingTeamChildMailboxShare)
	admin.GET("/team-child/accounts/:account_id/mailbox-share", handler.GetTeamChildMailboxShare)
	admin.POST("/team-child/accounts/:account_id/mailbox-share", handler.CreateTeamChildMailboxShare)
	admin.DELETE("/team-child/accounts/:account_id/mailbox-share", handler.RevokeTeamChildMailboxShare)
	router.GET("/public/team-mailbox/code", handler.PollPublicTeamChildMailboxShare)
	router.GET("/public/team-mailbox/messages", handler.ListPublicTeamChildMailboxShare)
	router.GET("/public/team-mailbox/messages/:message_id", handler.GetPublicTeamChildMailboxShareMessage)
	return handler, adminService, router, &createCalls, &listCalls, &detailCalls
}

func performTeamMailboxShareRequest(router http.Handler, method, path, token string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, nil)
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	router.ServeHTTP(recorder, request)
	return recorder
}

func createTeamMailboxShare(t *testing.T, router http.Handler, replace bool) teamMailboxShareStatusResponse {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/admin/team-child/accounts/61/mailbox-share", strings.NewReader(fmt.Sprintf(`{"replace":%t}`, replace)))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusOK, recorder.Code)
	var created struct {
		Data teamMailboxShareStatusResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &created))
	require.True(t, created.Data.Active)
	require.Equal(t, "team1061@example.test", created.Data.Email)
	require.NotEmpty(t, created.Data.Token)
	return created.Data
}

func TestTeamChildMailboxShareIsLongLivedReadOnlyAndRevocable(t *testing.T) {
	handler, adminService, router, createCalls, listCalls, detailCalls := setupTeamMailboxShareRouter(t)
	created := createTeamMailboxShare(t, router, false)

	// The persistent registry contains an encrypted token copy plus a digest,
	// never the plaintext bearer token or mailbox-provider session token.
	persisted, err := os.ReadFile(teamMailboxShareFilePath())
	require.NoError(t, err)
	require.NotContains(t, string(persisted), created.Token)
	require.NotContains(t, string(persisted), "provider-mailbox-jwt")
	fileInfo, err := os.Stat(teamMailboxShareFilePath())
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), fileInfo.Mode().Perm())

	statusRec := performTeamMailboxShareRequest(router, http.MethodGet, "/admin/team-child/accounts/61/mailbox-share", "")
	require.Equal(t, http.StatusOK, statusRec.Code)
	var restored struct {
		Data teamMailboxShareStatusResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(statusRec.Body.Bytes(), &restored))
	require.True(t, restored.Data.Active)
	require.Equal(t, created.Token, restored.Data.Token)

	firstList := performTeamMailboxShareRequest(router, http.MethodGet, "/public/team-mailbox/messages", created.Token)
	require.Equal(t, http.StatusOK, firstList.Code)
	require.Equal(t, "private, no-store, max-age=0", firstList.Header().Get("Cache-Control"))
	require.Equal(t, "no-referrer", firstList.Header().Get("Referrer-Policy"))
	require.Equal(t, "Authorization", firstList.Header().Get("Vary"))
	require.NotContains(t, firstList.Body.String(), "provider-mailbox-jwt")
	require.NotContains(t, firstList.Body.String(), "provider-api-key")
	var inbox struct {
		Data teamMailboxShareMessagesResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(firstList.Body.Bytes(), &inbox))
	require.Equal(t, "team1061@example.test", inbox.Data.Email)
	require.Len(t, inbox.Data.Messages, 2)
	require.Equal(t, "family-update", inbox.Data.Messages[0].ID)
	require.Equal(t, "Family update", inbox.Data.Messages[0].Subject)
	require.Equal(t, "openai-code", inbox.Data.Messages[1].ID)
	require.Equal(t, "418204", inbox.Data.Messages[1].Code)
	require.NotContains(t, firstList.Body.String(), "Must stay private")
	require.Equal(t, 1, int(createCalls.Load()))
	require.Equal(t, 1, int(listCalls.Load()))

	// The public page refreshes every five seconds, but immediate reloads reuse
	// one short snapshot instead of multiplying mailbox-provider reads.
	secondList := performTeamMailboxShareRequest(router, http.MethodGet, "/public/team-mailbox/messages", created.Token)
	require.Equal(t, http.StatusOK, secondList.Code)
	require.Equal(t, 1, int(createCalls.Load()))
	require.Equal(t, 1, int(listCalls.Load()))

	familyMessage := performTeamMailboxShareRequest(router, http.MethodGet, "/public/team-mailbox/messages/family-update", created.Token)
	require.Equal(t, http.StatusOK, familyMessage.Code)
	require.NotContains(t, familyMessage.Body.String(), "provider-mailbox-jwt")
	var detail struct {
		Data teamMailboxShareMessageDetailResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(familyMessage.Body.Bytes(), &detail))
	require.Equal(t, "Family update", detail.Data.Subject)
	require.Contains(t, detail.Data.Body, "Please bring dessert")
	require.Equal(t, 1, int(detailCalls.Load()))

	compactCode := performTeamMailboxShareRequest(router, http.MethodGet, "/public/team-mailbox/code", created.Token)
	require.Equal(t, http.StatusOK, compactCode.Code)
	var code struct {
		Data teamMailboxShareCodeResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(compactCode.Body.Bytes(), &code))
	require.Equal(t, "received", code.Data.Status)
	require.Equal(t, "418204", code.Data.Code)
	require.Equal(t, 1, int(listCalls.Load()))

	// A fresh registry object represents a normal application restart. The
	// long-lived capability must remain valid without persisting any provider
	// credentials in the registry.
	parsedToken, shareID, err := publicTeamMailboxShareToken("Bearer " + created.Token)
	require.NoError(t, err)
	restartedRegistry := newOpenAITeamMailboxShareRegistry()
	record, found, err := restartedRegistry.resolve(shareID, parsedToken)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, int64(61), record.AccountID)

	parts := strings.Split(created.Token, ".")
	require.Len(t, parts, 3)
	parts[1] = "1q" // The same secret cannot cross to a different share ID.
	require.Equal(t, http.StatusNotFound, performTeamMailboxShareRequest(router, http.MethodGet, "/public/team-mailbox/messages", strings.Join(parts, ".")).Code)

	replaced := createTeamMailboxShare(t, router, true)
	require.NotEqual(t, created.Token, replaced.Token)
	require.Equal(t, http.StatusNotFound, performTeamMailboxShareRequest(router, http.MethodGet, "/public/team-mailbox/messages", created.Token).Code)
	require.Equal(t, http.StatusOK, performTeamMailboxShareRequest(router, http.MethodGet, "/public/team-mailbox/messages", replaced.Token).Code)

	// Deleting the Team account invalidates its account-bound public mailbox
	// link without revealing that account's state to the visitor.
	adminService.missing = true
	require.Equal(t, http.StatusNotFound, performTeamMailboxShareRequest(router, http.MethodGet, "/public/team-mailbox/messages", replaced.Token).Code)
	adminService.missing = false

	revokeRec := performTeamMailboxShareRequest(router, http.MethodDelete, "/admin/team-child/accounts/61/mailbox-share", "")
	require.Equal(t, http.StatusOK, revokeRec.Code)
	require.Equal(t, http.StatusNotFound, performTeamMailboxShareRequest(router, http.MethodGet, "/public/team-mailbox/messages", replaced.Token).Code)
	require.NotNil(t, handler.teamMailboxShareStore)
}

func TestTeamMailboxShareStatusKeepsAnActiveLinkPrivateWhenDecryptionFails(t *testing.T) {
	handler, _, router, _, _, _ := setupTeamMailboxShareRouter(t)
	created := createTeamMailboxShare(t, router, false)

	// A missing or rotated encryption key must not reveal a malformed value or
	// invalidate the public capability that is still protected by its hash.
	handler.ConfigureTeamChildSecrets(teamMailboxShareTestEncryptor{failDecrypt: true})
	statusRec := performTeamMailboxShareRequest(router, http.MethodGet, "/admin/team-child/accounts/61/mailbox-share", "")
	require.Equal(t, http.StatusOK, statusRec.Code)
	var status struct {
		Data teamMailboxShareStatusResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(statusRec.Body.Bytes(), &status))
	require.True(t, status.Data.Active)
	require.Empty(t, status.Data.Token)
	require.Equal(t, http.StatusOK, performTeamMailboxShareRequest(router, http.MethodGet, "/public/team-mailbox/messages", created.Token).Code)
}

func TestTeamMailboxShareRegistryAcceptsHistoricalOneTimeLinks(t *testing.T) {
	t.Setenv(teamMailboxShareFileEnv, filepath.Join(t.TempDir(), "team-mailbox-shares.json"))
	shareID, token, tokenHash, err := newTeamMailboxShareToken()
	require.NoError(t, err)
	registry := persistedTeamMailboxShareRegistry{
		Version: 1,
		Shares: map[string]persistedTeamMailboxShare{
			shareID: {
				Email:     "team1061@example.test",
				TokenHash: tokenHash,
				CreatedAt: "2026-08-31T00:00:00Z",
			},
		},
	}
	body, err := json.Marshal(registry)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(teamMailboxShareFilePath(), body, 0o600))

	_, parsedShareID, err := publicTeamMailboxShareToken("Bearer " + token)
	require.NoError(t, err)
	record, found, err := newOpenAITeamMailboxShareRegistry().resolve(parsedShareID, token)
	require.NoError(t, err)
	require.True(t, found)
	require.Empty(t, record.TokenCiphertext)
}

func TestPendingTeamMailboxShareBindsAfterImportAndRejectsUnknownMailboxes(t *testing.T) {
	handler, _, router, _, _, _ := setupTeamMailboxShareRouter(t)
	const email = "team1061@example.test"
	require.NoError(t, handler.teamMailboxStore.remember(context.Background(), 42, email))

	createRec := httptest.NewRecorder()
	createReq := httptest.NewRequest(http.MethodPost, "/admin/team-child/mailbox-share", strings.NewReader(`{"email":"team1061@example.test","replace":false}`))
	createReq.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(createRec, createReq)
	require.Equal(t, http.StatusOK, createRec.Code)
	var created struct {
		Data teamMailboxShareStatusResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(createRec.Body.Bytes(), &created))
	require.NotEmpty(t, created.Data.Token)

	_, shareID, err := publicTeamMailboxShareToken("Bearer " + created.Data.Token)
	require.NoError(t, err)
	beforeImport, found, err := newOpenAITeamMailboxShareRegistry().resolve(shareID, created.Data.Token)
	require.NoError(t, err)
	require.True(t, found)
	require.Zero(t, beforeImport.AccountID)

	// This is the same binding performed by CreateAccountFromOAuth once the
	// Team child account is successfully imported.
	require.NoError(t, handler.ensureTeamMailboxShareRegistry().attachAccount(email, 61))
	afterImport, found, err := newOpenAITeamMailboxShareRegistry().resolve(shareID, created.Data.Token)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, int64(61), afterImport.AccountID)

	unknownRec := httptest.NewRecorder()
	unknownReq := httptest.NewRequest(http.MethodPost, "/admin/team-child/mailbox-share", strings.NewReader(`{"email":"team1999@example.test","replace":false}`))
	unknownReq.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(unknownRec, unknownReq)
	require.Equal(t, http.StatusForbidden, unknownRec.Code)
}

func TestTeamChildMailboxShareRejectsNonTeamAccounts(t *testing.T) {
	_, adminService, router, _, _, _ := setupTeamMailboxShareRouter(t)
	adminService.getAccountResult.Extra = nil

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/admin/team-child/accounts/61/mailbox-share", strings.NewReader(`{"replace":false}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
}

func TestTeamMailboxShareDecodesHeaderAndMultipartBodyWithoutEnvelope(t *testing.T) {
	const boundary = "_av-dLpcuwCqBNVICISo1-E6JA"
	plainText := "You've been invited to a ChatGPT Business workspace.\n\nJoin workspace"
	rawBody := strings.Join([]string{
		"--" + boundary,
		"Content-Type: text/plain; charset=utf-8",
		"Content-Transfer-Encoding: 7bit",
		"",
		plainText,
		"--" + boundary,
		"Content-Type: text/html; charset=utf-8",
		"Content-Transfer-Encoding: base64",
		"",
		base64.StdEncoding.EncodeToString([]byte("<html><body><style>.hidden { display: none; }</style><p>HTML fallback must not replace the plain part.</p></body></html>")),
		"--" + boundary + "--",
		"",
	}, "\r\n")
	message := map[string]any{
		"id":      "mime-invite",
		"from":    "OpenAI <noreply@tm.openai.com>",
		"subject": "=?utf-8?Q?xia=20xia=20has=20invited=20you=20to=20ChatGPT=20Business?=",
		"body":    rawBody,
	}

	summary, ok := teamMailboxShareMessageSummary(message)
	require.True(t, ok)
	require.Equal(t, "xia xia has invited you to ChatGPT Business", summary.Subject)
	require.Contains(t, summary.Preview, "You've been invited")
	require.NotContains(t, summary.Preview, "Content-Transfer-Encoding")
	require.NotContains(t, summary.Preview, boundary)

	detail := teamMailboxShareMessageDetail(message, summary)
	require.Equal(t, plainText, detail.Body)
	require.NotContains(t, detail.Body, "HTML fallback")
	require.NotContains(t, detail.Body, "Content-Type")
	require.Contains(t, detail.HTML, "HTML fallback must not replace the plain part.")
	require.Contains(t, detail.HTML, "<style>.hidden{display:none}</style>")
	require.NotContains(t, detail.HTML, "Content-Transfer-Encoding")
}

func TestTeamMailboxShareKeepsSafeRichEmailLayoutAndDropsActiveContent(t *testing.T) {
	rawHTML := `<style>.shell { background-color: #101214; color: #f8fafc; } @import url(https://unsafe.example.test/mail.css);</style>
<table class="shell" width="600" cellpadding="24"><tr><td><img src="https://cdn.openai.com/logo.png" alt="OpenAI" width="48" height="48"><p id="copy">Your verification code is <strong>418204</strong>.</p><img src="https://tracker.example.test/pixel.gif" width="1" height="1"></td></tr></table>
<script>window.__unsafe = true</script><form action="https://unsafe.example.test"><input name="password"></form><iframe src="https://unsafe.example.test"></iframe>`

	sanitized := teamMailboxSanitizeHTML(rawHTML)
	require.Contains(t, sanitized, `<style>.shell{background-color:#101214; color:#f8fafc}</style>`)
	require.Contains(t, sanitized, `<table class="shell" width="600" cellpadding="24">`)
	require.Contains(t, sanitized, `src="https://cdn.openai.com/logo.png"`)
	require.Contains(t, sanitized, `id="copy"`)
	require.NotContains(t, sanitized, "tracker.example.test")
	require.NotContains(t, sanitized, "<script")
	require.NotContains(t, sanitized, "<form")
	require.NotContains(t, sanitized, "<iframe")
	require.NotContains(t, sanitized, "@import")
	require.NotContains(t, sanitized, "url(")
}

func TestTeamMailboxShareDecodesBase64AndQuotedPrintableTextParts(t *testing.T) {
	const boundary = "00000000000572bf1065a4335e9"
	base64Text := "你好，这是一封完整的中文邮件。验证码是 418204。"
	quotedPrintableText := "欢迎加入 XIASS 邮箱。"
	rawBody := strings.Join([]string{
		"--" + boundary,
		"Content-Type: text/plain; charset=\"UTF-8\"",
		"Content-Transfer-Encoding: base64",
		"",
		base64.StdEncoding.EncodeToString([]byte(base64Text)),
		"--" + boundary,
		"Content-Type: text/plain; charset=utf-8",
		"Content-Transfer-Encoding: quoted-printable",
		"",
		"=E6=AC=A2=E8=BF=8E=E5=8A=A0=E5=85=A5=20XIASS=20=E9=82=AE=E7=AE=B1=E3=80=82",
		"--" + boundary + "--",
		"",
	}, "\r\n")
	message := map[string]any{"id": "mime-base64", "body": rawBody}

	body := teamMailboxShareReadableBody(message)
	require.Contains(t, body, base64Text)
	require.Contains(t, body, quotedPrintableText)
	require.NotContains(t, body, "5L2g5aW9")
	require.NotContains(t, body, "Content-Transfer-Encoding")
	require.Equal(t, "418204", extractTeamMailboxVerificationCodeFromMessage(message))
}

func TestTeamMailboxSharePreviewOmitsFlattenedTemplateCSS(t *testing.T) {
	message := map[string]any{
		"id":      "flattened-template-css",
		"from":    "ChatGPT <noreply@tm.openai.com>",
		"subject": "Your temporary ChatGPT verification code",
		"body": strings.Join([]string{
			"Enter this temporary verification code to continue: 219428",
			"/** Google webfonts. Recommended to include the .woff version for cross-client compatibility. */",
			"@media screen { @font-face { font-family: Colfax; src: url(https://openai-public.s3-us-west-2.amazonaws.com/font.woff2); } }",
		}, "\n"),
	}

	summary, ok := teamMailboxShareMessageSummary(message)
	require.True(t, ok)
	require.Equal(t, "Enter this temporary verification code to continue: 219428", summary.Preview)
	require.NotContains(t, summary.Preview, "Google webfonts")
	require.NotContains(t, summary.Preview, "@font-face")
	require.NotContains(t, summary.Preview, "url(")
}

func TestTeamMailboxShareDecodesCompleteMIMEAndSkipsAttachments(t *testing.T) {
	const boundary = "xiass-message-boundary"
	raw := strings.Join([]string{
		"From: =?utf-8?Q?XIASS_=E9=82=AE=E7=AE=B1?= <mail@example.test>",
		"Subject: =?utf-8?Q?=E6=B4=BB=E5=8A=A8=E9=80=9A=E7=9F=A5?=",
		"MIME-Version: 1.0",
		"Content-Type: multipart/mixed; boundary=\"" + boundary + "\"",
		"",
		"--" + boundary,
		"Content-Type: text/plain; charset=utf-8; name=ignored.txt",
		"",
		"This attachment must not be displayed.",
		"--" + boundary,
		"Content-Type: text/html; charset=utf-8",
		"",
		"<html><body><h1>活动通知</h1><p>请登录查看详情。</p></body></html>",
		"--" + boundary + "--",
		"",
	}, "\r\n")

	decoded, recognized := teamMailboxDecodeMIMEText(raw)
	require.True(t, recognized)
	require.Contains(t, decoded, "活动通知")
	require.Contains(t, decoded, "请登录查看详情")
	require.NotContains(t, decoded, "attachment must not")
	require.NotContains(t, decoded, "<h1>")
	require.Equal(t, "活动通知", teamMailboxDecodeMIMEHeader("=?utf-8?Q?=E6=B4=BB=E5=8A=A8=E9=80=9A=E7=9F=A5?="))
}

func TestTeamMailboxSharePreservesSafeEmailLayoutWithoutActiveContent(t *testing.T) {
	message := map[string]any{
		"id":       "safe-rich-mail",
		"from":     "OpenAI <trustandsafety@tm.openai.com>",
		"subject":  "账号重要通知",
		"received": "2026-08-30T12:00:00Z",
		"html":     `<table width="600" cellpadding="0" cellspacing="0" style="background-color:#111827; color:#f8fafc"><tr><td style="padding:24px"><h1 style="font-size:24px">账号重要通知</h1><p>你的账号需要注意。</p><a href="https://help.openai.com/article" style="color:#60a5fa">查看详情</a><img src="https://cdn.openai.com/logo.png" alt="OpenAI" width="40" height="40"><img src="https://tracker.example.test/pixel.png" width="1" height="1"><script>window.__unsafe = true</script><form action="https://attacker.example.test"><input name="secret"></form></td></tr></table>`,
	}

	summary, ok := teamMailboxShareMessageSummary(message)
	require.True(t, ok)
	require.Contains(t, summary.Preview, "你的账号需要注意")

	detail := teamMailboxShareMessageDetail(message, summary)
	require.Contains(t, detail.Body, "账号重要通知")
	require.Contains(t, detail.HTML, "<table")
	require.Contains(t, detail.HTML, "账号重要通知")
	require.Contains(t, detail.HTML, `href="https://help.openai.com/article"`)
	require.Contains(t, detail.HTML, `target="_blank"`)
	require.Contains(t, detail.HTML, `rel="noopener noreferrer nofollow"`)
	require.Contains(t, detail.HTML, `style="background-color:#111827; color:#f8fafc"`)
	require.Contains(t, detail.HTML, `src="https://cdn.openai.com/logo.png"`)
	require.NotContains(t, detail.HTML, "tracker.example.test")
	require.NotContains(t, detail.HTML, "<script")
	require.NotContains(t, detail.HTML, "__unsafe")
	require.NotContains(t, detail.HTML, "<form")
	require.NotContains(t, detail.HTML, "attacker.example.test")
}

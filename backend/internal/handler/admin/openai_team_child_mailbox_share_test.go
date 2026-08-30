package admin

import (
	"context"
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

	// The persistent registry contains only a digest, never the public bearer
	// token or the mailbox-provider session token.
	persisted, err := os.ReadFile(teamMailboxShareFilePath())
	require.NoError(t, err)
	require.NotContains(t, string(persisted), created.Token)
	require.NotContains(t, string(persisted), "provider-mailbox-jwt")
	fileInfo, err := os.Stat(teamMailboxShareFilePath())
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), fileInfo.Mode().Perm())

	statusRec := performTeamMailboxShareRequest(router, http.MethodGet, "/admin/team-child/accounts/61/mailbox-share", "")
	require.Equal(t, http.StatusOK, statusRec.Code)
	require.NotContains(t, statusRec.Body.String(), created.Token)

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

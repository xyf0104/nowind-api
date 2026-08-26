//go:build unit

package admin

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/domain"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type quotaMonitorHandlerRepository struct {
	service.ChannelMonitorRepository
	created *service.ChannelMonitor
}

func (r *quotaMonitorHandlerRepository) Create(_ context.Context, monitor *service.ChannelMonitor) error {
	monitor.ID = 501
	monitor.CreatedAt = time.Unix(0, 0).UTC()
	monitor.UpdatedAt = time.Unix(0, 0).UTC()
	copy := *monitor
	r.created = &copy
	return nil
}

type quotaMonitorHandlerAccounts struct {
	service.AccountRepository
	account *service.Account
}

func (r *quotaMonitorHandlerAccounts) GetByID(context.Context, int64) (*service.Account, error) {
	return r.account, nil
}

type quotaMonitorHandlerEncryptor struct{}

func (quotaMonitorHandlerEncryptor) Encrypt(plaintext string) (string, error) {
	return "encrypted:" + plaintext, nil
}

func (quotaMonitorHandlerEncryptor) Decrypt(ciphertext string) (string, error) {
	return ciphertext, nil
}

func TestChannelMonitorCreateHandlerAcceptsAntigravityQuotaMonitor(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &quotaMonitorHandlerRepository{}
	monitorService := service.NewChannelMonitorService(repo, quotaMonitorHandlerEncryptor{})
	accounts := &quotaMonitorHandlerAccounts{
		account: &service.Account{ID: 104, Platform: domain.PlatformAntigravity},
	}
	monitorService.SetQuotaFetcher(service.NewChannelMonitorQuotaFetcher(nil, nil, nil, accounts, nil))

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 7})
		c.Next()
	})
	router.POST("/api/v1/admin/channel-monitors", NewChannelMonitorHandler(monitorService).Create)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/channel-monitors", bytes.NewBufferString(`{
		"name":"antigravity quota",
		"provider":"antigravity",
		"check_mode":"quota",
		"account_id":104,
		"interval_seconds":60
	}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusCreated, recorder.Code, recorder.Body.String())
	require.NotNil(t, repo.created)
	require.Equal(t, service.MonitorProviderAntigravity, repo.created.Provider)
	require.Equal(t, service.MonitorCheckModeQuota, repo.created.CheckMode)
	require.NotNil(t, repo.created.AccountID)
	require.Equal(t, int64(104), *repo.created.AccountID)
	require.Empty(t, repo.created.Endpoint)
	require.Equal(t, service.MonitorDefaultQuotaModel, repo.created.PrimaryModel)
	require.Contains(t, recorder.Body.String(), `"check_mode":"quota"`)
	require.Contains(t, recorder.Body.String(), `"account_id":104`)
}

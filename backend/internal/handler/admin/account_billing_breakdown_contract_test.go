//go:build unit

package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type billingContractAccountRepo struct {
	service.AccountRepository
	account *service.Account
}

func (r *billingContractAccountRepo) GetByID(context.Context, int64) (*service.Account, error) {
	return r.account, nil
}

type billingContractUsageRepo struct {
	service.UsageLogRepository
	users        []usagestats.AccountBillingUser
	selectedUser *usagestats.AccountBillingSelectedUser
	models       []usagestats.AccountBillingModel
	startTime    time.Time
	endTime      time.Time
}

func (r *billingContractUsageRepo) GetAccountBillingUsers(_ context.Context, _ int64, startTime, endTime time.Time) ([]usagestats.AccountBillingUser, error) {
	r.startTime = startTime
	r.endTime = endTime
	return r.users, nil
}

func (r *billingContractUsageRepo) GetAccountBillingModels(_ context.Context, _, _ int64, startTime, endTime time.Time) (*usagestats.AccountBillingSelectedUser, []usagestats.AccountBillingModel, error) {
	r.startTime = startTime
	r.endTime = endTime
	return r.selectedUser, r.models, nil
}

func newBillingContractRouter(accountType string, usageRepo *billingContractUsageRepo) *gin.Engine {
	gin.SetMode(gin.TestMode)
	accountRepo := &billingContractAccountRepo{account: &service.Account{ID: 91, Type: accountType}}
	usageService := service.NewAccountUsageService(accountRepo, usageRepo, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	handler := &AccountHandler{accountUsageService: usageService}
	router := gin.New()
	router.GET("/api/v1/admin/accounts/:id/billing-breakdown", handler.GetBillingBreakdown)
	return router
}

func TestAccountBillingBreakdownContractExactTimeTakesPriority(t *testing.T) {
	usageRepo := &billingContractUsageRepo{users: []usagestats.AccountBillingUser{
		{UserID: 7, Username: "alice", Email: "alice@example.com", Requests: 12, Tokens: 3456, AccountCost: 8.25, UserCost: 10.5},
	}}
	router := newBillingContractRouter(service.AccountTypeOAuth, usageRepo)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/admin/accounts/91/billing-breakdown?start_time=2026-08-13T01:15:00Z&end_time=2026-08-13T06:15:00Z&start_date=2026-08-01&end_date=2026-08-31&timezone=Asia%2FShanghai",
		nil,
	)
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, time.Date(2026, 8, 13, 1, 15, 0, 0, time.UTC), usageRepo.startTime)
	require.Equal(t, time.Date(2026, 8, 13, 6, 15, 0, 0, time.UTC), usageRepo.endTime)

	var payload struct {
		Data struct {
			Range struct {
				StartTime string `json:"start_time"`
				EndTime   string `json:"end_time"`
				Timezone  string `json:"timezone"`
			} `json:"range"`
			AccountID int64                                     `json:"account_id"`
			Summary   usagestats.AccountBillingBreakdownSummary `json:"summary"`
			Users     []usagestats.AccountBillingUser           `json:"users"`
			Models    json.RawMessage                           `json:"models"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.Equal(t, int64(91), payload.Data.AccountID)
	require.Equal(t, "2026-08-13T01:15:00Z", payload.Data.Range.StartTime)
	require.Equal(t, "2026-08-13T06:15:00Z", payload.Data.Range.EndTime)
	require.Equal(t, "Asia/Shanghai", payload.Data.Range.Timezone)
	require.Equal(t, int64(12), payload.Data.Summary.Requests)
	require.Len(t, payload.Data.Users, 1)
	require.Nil(t, payload.Data.Models)
}

func TestAccountBillingBreakdownContractUserModelDrilldown(t *testing.T) {
	usageRepo := &billingContractUsageRepo{
		selectedUser: &usagestats.AccountBillingSelectedUser{UserID: 7, Username: "alice", Email: "alice@example.com"},
		models: []usagestats.AccountBillingModel{
			{Model: "gpt-5.6", Requests: 4, Tokens: 800, AccountCost: 2.5, UserCost: 3.0},
		},
	}
	router := newBillingContractRouter(service.AccountTypeOAuth, usageRepo)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/admin/accounts/91/billing-breakdown?start_date=2026-08-12&end_date=2026-08-13&timezone=Asia%2FShanghai&user_id=7",
		nil,
	)
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	shanghai, err := time.LoadLocation("Asia/Shanghai")
	require.NoError(t, err)
	require.Equal(t, time.Date(2026, 8, 12, 0, 0, 0, 0, shanghai), usageRepo.startTime)
	require.Equal(t, time.Date(2026, 8, 14, 0, 0, 0, 0, shanghai), usageRepo.endTime)

	var payload struct {
		Data struct {
			SelectedUser usagestats.AccountBillingSelectedUser `json:"selected_user"`
			Models       []usagestats.AccountBillingModel      `json:"models"`
			Users        json.RawMessage                       `json:"users"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.Equal(t, int64(7), payload.Data.SelectedUser.UserID)
	require.Len(t, payload.Data.Models, 1)
	require.Equal(t, "gpt-5.6", payload.Data.Models[0].Model)
	require.Nil(t, payload.Data.Users)
}

func TestAccountBillingBreakdownContractSupportsNonOAuthAccount(t *testing.T) {
	usageRepo := &billingContractUsageRepo{users: []usagestats.AccountBillingUser{
		{UserID: 8, Username: "bob", Email: "bob@example.com", Requests: 3, Tokens: 900, AccountCost: 1.25, UserCost: 2.5},
	}}
	router := newBillingContractRouter(service.AccountTypeAPIKey, usageRepo)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/admin/accounts/91/billing-breakdown?start_time=2026-08-13T01:15:00Z&end_time=2026-08-13T06:15:00Z",
		nil,
	)
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var payload struct {
		Data struct {
			AccountID int64                                     `json:"account_id"`
			Summary   usagestats.AccountBillingBreakdownSummary `json:"summary"`
			Users     []usagestats.AccountBillingUser           `json:"users"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.Equal(t, int64(91), payload.Data.AccountID)
	require.Equal(t, int64(3), payload.Data.Summary.Requests)
	require.InDelta(t, 1.25, payload.Data.Summary.AccountCost, 0.0001)
	require.InDelta(t, 2.5, payload.Data.Summary.UserCost, 0.0001)
	require.Len(t, payload.Data.Users, 1)
	require.Equal(t, int64(8), payload.Data.Users[0].UserID)
}

func TestAccountBillingBreakdownContractRequiresCompleteRangePair(t *testing.T) {
	usageRepo := &billingContractUsageRepo{}
	router := newBillingContractRouter(service.AccountTypeOAuth, usageRepo)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/admin/accounts/91/billing-breakdown?start_time=2026-08-13T01:15:00Z",
		nil,
	)
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "start_time and end_time must be provided together")
}

package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func nodeReadTestRouter(local, legacy string) *gin.Engine {
	cfg := &config.Config{}
	cfg.Gateway.ExecutionNode = config.GatewayExecutionNodeConfig{
		Enabled: true, ID: local, LegacyUnassignedNodeID: legacy,
	}
	router := gin.New()
	router.Use(middleware.ExecutionNodeReadContext(service.NewSettingService(nil, cfg)))
	return router
}

func TestExecutionNodeUsageFiltersUseSourceOwnerFromEitherPanel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, local := range []string{"api.xiass.com", "api2"} {
		for _, owner := range []string{"api.xiass.com", "api2"} {
			t.Run(local+"/"+owner, func(t *testing.T) {
				repo := &adminUsageRepoCapture{}
				h := NewUsageHandler(service.NewUsageService(repo, nil, nil, nil), nil, nil, nil)
				router := nodeReadTestRouter(local, "api.xiass.com")
				router.GET("/usage", h.List)
				router.GET("/stats", h.Stats)
				for _, path := range []string{"/usage", "/stats"} {
					rec := httptest.NewRecorder()
					router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path+"?nocache=1&execution_node_id="+owner+"&execution_node_legacy_id=forged", nil))
					require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
				}
				for _, filters := range []usagestats.UsageLogFilters{repo.listFilters, repo.statsFilters} {
					require.Equal(t, owner, filters.ExecutionNodeID)
					require.Equal(t, "api.xiass.com", filters.ExecutionNodeLegacyID)
				}
			})
		}
	}
}

type nodeDashboardRepo struct {
	service.UsageLogRepository
	filters []usagestats.UsageLogFilters
}

func (r *nodeDashboardRepo) GetUsageTrendWithUsageFilters(_ context.Context, _, _ time.Time, _ string, f usagestats.UsageLogFilters) ([]usagestats.TrendDataPoint, error) {
	r.filters = append(r.filters, f)
	return []usagestats.TrendDataPoint{}, nil
}

func (r *nodeDashboardRepo) GetModelStatsWithUsageFiltersBySource(_ context.Context, _, _ time.Time, f usagestats.UsageLogFilters, _ string) ([]usagestats.ModelStat, error) {
	r.filters = append(r.filters, f)
	return []usagestats.ModelStat{}, nil
}

func (r *nodeDashboardRepo) GetGroupStatsWithUsageFilters(_ context.Context, _, _ time.Time, f usagestats.UsageLogFilters) ([]usagestats.GroupStat, error) {
	r.filters = append(r.filters, f)
	return []usagestats.GroupStat{}, nil
}

func TestExecutionNodeDashboardQueriesAndCachesKeepLegacyOwner(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &nodeDashboardRepo{}
	h := NewDashboardHandler(service.NewDashboardService(repo, nil, nil, nil), nil)
	// Same selected node and date range, different deployment legacy owner:
	// neither the combined nor per-chart cache may mask the second query.
	for _, legacy := range []string{"source-a.example", "source-b.example"} {
		router := nodeReadTestRouter("peer-test", legacy)
		router.GET("/snapshot", h.GetSnapshotV2)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/snapshot?execution_node_id=source-test&start_date=2041-03-01&end_date=2041-03-02&include_stats=false&include_model_stats=true&include_group_stats=true", nil))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		require.Len(t, repo.filters, 3)
		for _, f := range repo.filters {
			require.Equal(t, "source-test", f.ExecutionNodeID)
			require.Equal(t, legacy, f.ExecutionNodeLegacyID)
		}
		repo.filters = nil
	}
}

func TestExecutionNodeUsageCacheSeparatesLegacyOwners(t *testing.T) {
	a := usagestats.UsageLogFilters{ExecutionNodeID: "source", ExecutionNodeLegacyID: "source"}
	b := a
	b.ExecutionNodeLegacyID = "peer"
	require.NotEqual(t, usageStatsCacheKey(a), usageStatsCacheKey(b))
}

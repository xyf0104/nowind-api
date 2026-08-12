package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type opsSnapshotV2RepoProbe struct {
	service.OpsRepository

	overviewCalls atomic.Int32
	trendCalls    atomic.Int32
	errorCalls    atomic.Int32

	mu            sync.Mutex
	lastStartTime time.Time
	lastEndTime   time.Time

	overviewStarted chan struct{}
	allowOverview   <-chan struct{}
}

func (r *opsSnapshotV2RepoProbe) GetDashboardOverview(_ context.Context, filter *service.OpsDashboardFilter) (*service.OpsDashboardOverview, error) {
	r.overviewCalls.Add(1)
	r.mu.Lock()
	r.lastStartTime = filter.StartTime
	r.lastEndTime = filter.EndTime
	r.mu.Unlock()
	if r.overviewStarted != nil {
		select {
		case r.overviewStarted <- struct{}{}:
		default:
		}
	}
	if r.allowOverview != nil {
		<-r.allowOverview
	}
	return &service.OpsDashboardOverview{
		StartTime: filter.StartTime,
		EndTime:   filter.EndTime,
	}, nil
}

func (r *opsSnapshotV2RepoProbe) GetThroughputTrend(_ context.Context, _ *service.OpsDashboardFilter, _ int) (*service.OpsThroughputTrendResponse, error) {
	r.trendCalls.Add(1)
	return &service.OpsThroughputTrendResponse{}, nil
}

func (r *opsSnapshotV2RepoProbe) GetErrorTrend(_ context.Context, _ *service.OpsDashboardFilter, _ int) (*service.OpsErrorTrendResponse, error) {
	r.errorCalls.Add(1)
	return &service.OpsErrorTrendResponse{}, nil
}

func (r *opsSnapshotV2RepoProbe) GetLatestSystemMetrics(_ context.Context, _ int) (*service.OpsSystemMetricsSnapshot, error) {
	return &service.OpsSystemMetricsSnapshot{}, nil
}

func (r *opsSnapshotV2RepoProbe) ListJobHeartbeats(_ context.Context) ([]*service.OpsJobHeartbeat, error) {
	return []*service.OpsJobHeartbeat{}, nil
}

func (r *opsSnapshotV2RepoProbe) lastRange() (time.Time, time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lastStartTime, r.lastEndTime
}

func newOpsSnapshotV2TestRouter(t *testing.T, repo service.OpsRepository) *gin.Engine {
	t.Helper()
	previousCache := opsDashboardSnapshotV2Cache
	opsDashboardSnapshotV2Cache = newSnapshotCache(opsDashboardSnapshotV2CacheTTL)
	t.Cleanup(func() {
		opsDashboardSnapshotV2Cache = previousCache
	})

	g := gin.New()
	h := NewOpsHandler(service.NewOpsService(repo, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil))
	g.GET("/admin/ops/dashboard/snapshot-v2", h.GetDashboardSnapshotV2)
	return g
}

func TestOpsSnapshotV2_RollingRangeUsesCacheBucket(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &opsSnapshotV2RepoProbe{}
	router := newOpsSnapshotV2TestRouter(t, repo)

	req1 := httptest.NewRequest(http.MethodGet, "/admin/ops/dashboard/snapshot-v2?time_range=1h", nil)
	rec1 := httptest.NewRecorder()
	router.ServeHTTP(rec1, req1)
	require.Equal(t, http.StatusOK, rec1.Code)
	require.Equal(t, "miss", rec1.Header().Get("X-Snapshot-Cache"))

	req2 := httptest.NewRequest(http.MethodGet, "/admin/ops/dashboard/snapshot-v2?time_range=1h", nil)
	rec2 := httptest.NewRecorder()
	router.ServeHTTP(rec2, req2)
	require.Equal(t, http.StatusOK, rec2.Code)
	require.Equal(t, "hit", rec2.Header().Get("X-Snapshot-Cache"))
	require.Equal(t, int32(1), repo.overviewCalls.Load())
	require.Equal(t, int32(1), repo.trendCalls.Load())
	require.Equal(t, int32(1), repo.errorCalls.Load())
}

func TestOpsSnapshotV2_ExplicitTimesRemainExact(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &opsSnapshotV2RepoProbe{}
	router := newOpsSnapshotV2TestRouter(t, repo)

	const startRaw = "2026-08-01T10:00:01.123456789Z"
	const endRaw = "2026-08-01T11:00:02.987654321Z"
	req := httptest.NewRequest(http.MethodGet, "/admin/ops/dashboard/snapshot-v2?start_time="+startRaw+"&end_time="+endRaw, nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	start, end := repo.lastRange()
	expectedStart, err := time.Parse(time.RFC3339Nano, startRaw)
	require.NoError(t, err)
	expectedEnd, err := time.Parse(time.RFC3339Nano, endRaw)
	require.NoError(t, err)
	require.Equal(t, expectedStart, start)
	require.Equal(t, expectedEnd, end)
}

func TestOpsSnapshotV2_CoalescesConcurrentBuilds(t *testing.T) {
	gin.SetMode(gin.TestMode)
	allowOverview := make(chan struct{})
	repo := &opsSnapshotV2RepoProbe{
		overviewStarted: make(chan struct{}, 1),
		allowOverview:   allowOverview,
	}
	router := newOpsSnapshotV2TestRouter(t, repo)

	serve := func(done chan<- *httptest.ResponseRecorder) {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/ops/dashboard/snapshot-v2?time_range=1h", nil))
		done <- rec
	}
	firstDone := make(chan *httptest.ResponseRecorder, 1)
	secondDone := make(chan *httptest.ResponseRecorder, 1)
	go serve(firstDone)
	select {
	case <-repo.overviewStarted:
	case <-time.After(time.Second):
		t.Fatal("first snapshot build did not start")
	}
	go serve(secondDone)
	time.Sleep(25 * time.Millisecond)
	require.Equal(t, int32(1), repo.overviewCalls.Load())

	close(allowOverview)
	first := <-firstDone
	second := <-secondDone
	require.Equal(t, http.StatusOK, first.Code)
	require.Equal(t, http.StatusOK, second.Code)
	require.Equal(t, int32(1), repo.overviewCalls.Load())
	require.Equal(t, int32(1), repo.trendCalls.Load())
	require.Equal(t, int32(1), repo.errorCalls.Load())
}

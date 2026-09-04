package routes

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestReadinessRouteReportsDependenciesAndExecutionNode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	registerReadinessRoute(router, []readinessProbe{
		{name: "postgres", run: func(context.Context) error { return nil }},
		{name: "redis", run: func(context.Context) error { return nil }},
	}, "api2", time.Second)

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
	require.Equal(t, "api2", recorder.Header().Get("X-XIASS-Execution-Node"))
	var payload map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
	require.Equal(t, "ok", payload["status"])
	require.Equal(t, "api2", payload["execution_node"])
	require.Equal(t, map[string]any{"postgres": "ok", "redis": "ok"}, payload["checks"])
}

func TestReadinessRouteFailsClosedWithoutExposingDependencyError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	registerReadinessRoute(router, []readinessProbe{
		{name: "postgres", run: func(context.Context) error { return errors.New("secret database address") }},
		{name: "redis", run: func(context.Context) error { return nil }},
	}, "", time.Second)

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	require.NotContains(t, recorder.Body.String(), "secret database address")
	var payload map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
	require.Equal(t, "unavailable", payload["status"])
	require.Equal(t, map[string]any{"postgres": "unavailable", "redis": "ok"}, payload["checks"])
}

func TestReadinessRouteHonorsOverallTimeout(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	registerReadinessRoute(router, []readinessProbe{
		{name: "postgres", run: func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		}},
		{name: "redis", run: func(context.Context) error { return nil }},
	}, "", 20*time.Millisecond)

	recorder := httptest.NewRecorder()
	started := time.Now()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	require.Less(t, time.Since(started), 500*time.Millisecond)
}

func TestExecutionNodeReadinessIgnoresDisabledSharedPolicy(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mock.ExpectQuery("SELECT value FROM settings").
		WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow("false"))

	cfg := &config.Config{}
	err = checkExecutionNodeReadiness(context.Background(), db, nil, cfg)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestExecutionNodeReadinessFailsWhenSharedPolicyCannotBeRead(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mock.ExpectQuery("SELECT value FROM settings").
		WillReturnError(errors.New("settings unavailable"))

	err = checkExecutionNodeReadiness(context.Background(), db, nil, &config.Config{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "settings unavailable")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestExecutionNodeReadinessRejectsMissingLocalProxy(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mock.ExpectQuery("SELECT value FROM settings").
		WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow("true"))
	mock.ExpectQuery("SELECT key, value FROM settings").
		WillReturnRows(sqlmock.NewRows([]string{"key", "value"}).
			AddRow("execution_node_weights", `{"api2":1}`).
			AddRow("execution_node_proxy_ids", `{"api2":83}`))
	mock.ExpectQuery("SELECT id FROM proxies").
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))

	cfg := &config.Config{}
	cfg.Gateway.ExecutionNode = config.GatewayExecutionNodeConfig{
		Enabled:                 true,
		ID:                      "api2",
		DefaultProxyID:          83,
		LegacyUnassignedNodeID:  "api2",
		LegacyUnassignedProxyID: 83,
	}
	err = checkExecutionNodeReadiness(context.Background(), db, nil, cfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "proxy")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestExecutionNodeReadinessChecksLocalProxyWhenSharedPolicyIsActive(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mock.ExpectQuery("SELECT value FROM settings").
		WithArgs().
		WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow("true"))
	mock.ExpectQuery("SELECT key, value FROM settings").
		WillReturnRows(sqlmock.NewRows([]string{"key", "value"}).
			AddRow("execution_node_weights", `{"api":1,"api2":1}`).
			AddRow("execution_node_proxy_ids", `{"api":84,"api2":83}`))
	mock.ExpectQuery("SELECT id FROM proxies").
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(84).AddRow(83))

	cfg := &config.Config{}
	cfg.Gateway.ExecutionNode = config.GatewayExecutionNodeConfig{
		Enabled:                 true,
		ID:                      "api2",
		DefaultProxyID:          83,
		LegacyUnassignedNodeID:  "api",
		LegacyUnassignedProxyID: 84,
	}
	err = checkExecutionNodeReadiness(context.Background(), db, nil, cfg)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestExecutionNodeReadinessAllowsSharedPoolWithoutLocalAccounts(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mock.ExpectQuery("SELECT value FROM settings").
		WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow("true"))
	mock.ExpectQuery("SELECT key, value FROM settings").
		WillReturnRows(sqlmock.NewRows([]string{"key", "value"}).
			AddRow("execution_node_weights", `{"api":5,"api2":1}`).
			AddRow("execution_node_proxy_ids", `{"api":84,"api2":83}`))
	mock.ExpectQuery("SELECT id FROM proxies").
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(84).AddRow(83))

	cfg := &config.Config{}
	cfg.Gateway.ExecutionNode = config.GatewayExecutionNodeConfig{
		Enabled:                 true,
		ID:                      "api2",
		DefaultProxyID:          83,
		LegacyUnassignedNodeID:  "api",
		LegacyUnassignedProxyID: 84,
	}

	// api2 may have zero locally owned accounts and still accept ingress: the
	// shared scheduler can execute an api-owned account through proxy 84.
	require.NoError(t, checkExecutionNodeReadiness(context.Background(), db, nil, cfg))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestExecutionNodeReadinessAllowsDrainedLocalExecutionNodeAsIngress(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	redisServer := miniredis.RunT(t)
	redisClient := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() { _ = redisClient.Close() })
	require.NoError(t, redisClient.Set(context.Background(), "xiass:execution_node:heartbeat:api", "test-owner", time.Minute).Err())
	mock.ExpectQuery("SELECT value FROM settings").
		WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow("true"))
	mock.ExpectQuery("SELECT key, value FROM settings").
		WillReturnRows(sqlmock.NewRows([]string{"key", "value"}).
			AddRow("execution_node_weights", `{"api":1,"api2":0}`).
			AddRow("execution_node_proxy_ids", `{"api":84,"api2":83}`))
	mock.ExpectQuery("SELECT id FROM proxies").
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(84))

	cfg := &config.Config{}
	cfg.Gateway.ExecutionNode = config.GatewayExecutionNodeConfig{
		Enabled:                 true,
		ID:                      "api2",
		DefaultProxyID:          83,
		LegacyUnassignedNodeID:  "api",
		LegacyUnassignedProxyID: 84,
	}

	require.NoError(t, checkExecutionNodeReadiness(context.Background(), db, redisClient, cfg))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestExecutionNodeReadinessAllowsOneUnavailablePositiveNodeWhenAnotherCanServe(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mock.ExpectQuery("SELECT value FROM settings").
		WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow("true"))
	mock.ExpectQuery("SELECT key, value FROM settings").
		WillReturnRows(sqlmock.NewRows([]string{"key", "value"}).
			AddRow("execution_node_weights", `{"api":1,"api2":1}`).
			AddRow("execution_node_proxy_ids", `{"api":84,"api2":83}`))
	mock.ExpectQuery("SELECT id FROM proxies").
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(83))

	cfg := &config.Config{}
	cfg.Gateway.ExecutionNode = config.GatewayExecutionNodeConfig{
		Enabled:                 true,
		ID:                      "api2",
		DefaultProxyID:          83,
		LegacyUnassignedNodeID:  "api",
		LegacyUnassignedProxyID: 84,
	}

	require.NoError(t, checkExecutionNodeReadiness(context.Background(), db, nil, cfg))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestExecutionNodeReadinessRejectsDuplicateNormalizedNodeIDs(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mock.ExpectQuery("SELECT value FROM settings").
		WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow("true"))
	mock.ExpectQuery("SELECT key, value FROM settings").
		WillReturnRows(sqlmock.NewRows([]string{"key", "value"}).
			AddRow("execution_node_weights", `{"api":1," api ":1}`).
			AddRow("execution_node_proxy_ids", `{"api":84}`))

	cfg := &config.Config{}
	cfg.Gateway.ExecutionNode = config.GatewayExecutionNodeConfig{
		Enabled:                 true,
		ID:                      "api",
		DefaultProxyID:          84,
		LegacyUnassignedNodeID:  "api",
		LegacyUnassignedProxyID: 84,
	}
	err = checkExecutionNodeReadiness(context.Background(), db, nil, cfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "duplicate")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestExecutionNodeReadinessRejectsNegativeWeight(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mock.ExpectQuery("SELECT value FROM settings").
		WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow("true"))
	mock.ExpectQuery("SELECT key, value FROM settings").
		WillReturnRows(sqlmock.NewRows([]string{"key", "value"}).
			AddRow("execution_node_weights", `{"api":-1,"api2":1}`).
			AddRow("execution_node_proxy_ids", `{"api":84,"api2":83}`))

	cfg := &config.Config{}
	cfg.Gateway.ExecutionNode = config.GatewayExecutionNodeConfig{
		Enabled:                 true,
		ID:                      "api2",
		DefaultProxyID:          83,
		LegacyUnassignedNodeID:  "api",
		LegacyUnassignedProxyID: 84,
	}
	err = checkExecutionNodeReadiness(context.Background(), db, nil, cfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid")
	require.NoError(t, mock.ExpectationsWereMet())
}

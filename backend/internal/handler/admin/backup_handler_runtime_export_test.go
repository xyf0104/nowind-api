package admin

import (
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type runtimeExportHandlerStub struct {
	record       service.RuntimeExportRecord
	archivePath  string
	sourceOrigin string
	deletedID    string
}

func (s *runtimeExportHandlerStub) Start(_ context.Context, sourceOrigin string) (*service.RuntimeExportRecord, error) {
	s.sourceOrigin = sourceOrigin
	return &s.record, nil
}

func (s *runtimeExportHandlerStub) List(context.Context) ([]service.RuntimeExportRecord, error) {
	return []service.RuntimeExportRecord{s.record}, nil
}

func (s *runtimeExportHandlerStub) Open(_ context.Context, id string) (*service.RuntimeExportDownload, error) {
	if id != s.record.ID {
		return nil, service.ErrRuntimeExportNotFound
	}
	file, err := os.Open(s.archivePath)
	if err != nil {
		return nil, err
	}
	return &service.RuntimeExportDownload{Record: s.record, Reader: file}, nil
}

func (s *runtimeExportHandlerStub) Delete(_ context.Context, id string) error {
	s.deletedID = id
	return nil
}

func TestRuntimeExportSourceOriginUsesForwardedHTTPS(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest("POST", "http://internal:8080/api/v1/admin/backups/runtime-exports", nil)
	context.Request.Host = "api.xiass.test"
	context.Request.Header.Set("X-Forwarded-Proto", "https, http")

	require.Equal(t, "https://api.xiass.test", runtimeExportSourceOrigin(context))
}

func TestRuntimeExportSourceOriginUsesDirectTLS(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest("GET", "https://api.xiass.test/api/v1/admin/backups/runtime-exports", nil)
	context.Request.TLS = &tls.ConnectionState{}

	require.Equal(t, "https://api.xiass.test", runtimeExportSourceOrigin(context))
}

func TestRuntimeExportHandlersCreateAndDownloadProtectedArchive(t *testing.T) {
	gin.SetMode(gin.TestMode)
	archivePath := filepath.Join(t.TempDir(), "migration.tar.gz")
	require.NoError(t, os.WriteFile(archivePath, []byte("migration-payload"), 0o600))
	stub := &runtimeExportHandlerStub{record: service.RuntimeExportRecord{
		ID:        "migration-" + strings.Repeat("a", 32),
		Status:    "completed",
		FileName:  "migration-package.tar.gz",
		SizeBytes: int64(len("migration-payload")),
		StartedAt: "2026-08-31T00:00:00Z",
	}, archivePath: archivePath}
	handler := NewBackupHandler(nil, nil, nil, stub)
	router := gin.New()
	router.POST("/runtime-exports", handler.CreateRuntimeExport)
	router.GET("/runtime-exports/:id/download", handler.DownloadRuntimeExport)
	router.DELETE("/runtime-exports/:id", handler.DeleteRuntimeExport)

	createRequest := httptest.NewRequest(http.MethodPost, "http://api.xiass.test/runtime-exports", nil)
	createRequest.Header.Set("X-Forwarded-Proto", "https")
	createRecorder := httptest.NewRecorder()
	router.ServeHTTP(createRecorder, createRequest)
	require.Equal(t, http.StatusAccepted, createRecorder.Code)
	require.Equal(t, "https://api.xiass.test", stub.sourceOrigin)

	downloadRecorder := httptest.NewRecorder()
	router.ServeHTTP(downloadRecorder, httptest.NewRequest(http.MethodGet, "/runtime-exports/"+stub.record.ID+"/download", nil))
	require.Equal(t, http.StatusOK, downloadRecorder.Code)
	require.Equal(t, "private, no-store, max-age=0", downloadRecorder.Header().Get("Cache-Control"))
	require.Equal(t, "no-referrer", downloadRecorder.Header().Get("Referrer-Policy"))
	require.Contains(t, downloadRecorder.Header().Get("Content-Disposition"), "migration-package.tar.gz")
	contents, err := io.ReadAll(downloadRecorder.Result().Body)
	require.NoError(t, err)
	require.Equal(t, "migration-payload", string(contents))

	deleteRecorder := httptest.NewRecorder()
	router.ServeHTTP(deleteRecorder, httptest.NewRequest(http.MethodDelete, "/runtime-exports/"+stub.record.ID, nil))
	require.Equal(t, http.StatusOK, deleteRecorder.Code)
	require.Equal(t, stub.record.ID, stub.deletedID)
}

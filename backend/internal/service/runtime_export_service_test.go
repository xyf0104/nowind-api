package service

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRuntimeExportServiceLifecycle(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	started := make(chan struct{})
	release := make(chan struct{})
	launcher := func(_ context.Context, outputPath, sourceOrigin string) error {
		require.Equal(t, "https://api.xiass.test", sourceOrigin)
		close(started)
		<-release
		return os.WriteFile(outputPath, []byte("portable-xiass-snapshot"), 0o600)
	}
	svc := newRuntimeExportService(directory, launcher, time.Now)

	created, err := svc.Start(context.Background(), "https://api.xiass.test/admin/settings")
	require.NoError(t, err)
	require.Equal(t, "queued", created.Status)
	require.True(t, validRuntimeExportID(created.ID))

	<-started
	_, err = svc.Start(context.Background(), "https://api.xiass.test")
	require.ErrorIs(t, err, ErrRuntimeExportInProgress)
	close(release)

	require.Eventually(t, func() bool {
		records, listErr := svc.List(context.Background())
		return listErr == nil && len(records) == 1 && records[0].Status == "completed"
	}, time.Second, 10*time.Millisecond)

	download, err := svc.Open(context.Background(), created.ID)
	require.NoError(t, err)
	contents, err := io.ReadAll(download.Reader)
	require.NoError(t, err)
	require.NoError(t, download.Reader.Close())
	require.Equal(t, "portable-xiass-snapshot", string(contents))
	require.NotEmpty(t, download.Record.SHA256)

	info, err := os.Stat(filepath.Join(directory, runtimeExportIndexFileName))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	require.NoError(t, svc.Delete(context.Background(), created.ID))
	records, err := svc.List(context.Background())
	require.NoError(t, err)
	require.Empty(t, records)
}

func TestRuntimeExportServiceReconcilesACompletedArchiveAfterRestart(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	id := "migration-" + strings.Repeat("a", 32)
	record := RuntimeExportRecord{
		ID:        id,
		Status:    "running",
		FileName:  runtimeExportFileName(id),
		StartedAt: "2026-08-31T00:00:00Z",
	}
	payload, err := json.Marshal([]RuntimeExportRecord{record})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(directory, runtimeExportIndexFileName), payload, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(directory, record.FileName), []byte("finished after restart"), 0o600))

	svc := newRuntimeExportService(directory, func(context.Context, string, string) error { return nil }, func() time.Time {
		return time.Date(2026, 8, 31, 0, 1, 0, 0, time.UTC)
	})
	records, err := svc.List(context.Background())
	require.NoError(t, err)
	require.Len(t, records, 1)
	require.Equal(t, "completed", records[0].Status)
	require.Equal(t, int64(len("finished after restart")), records[0].SizeBytes)
	require.NotEmpty(t, records[0].SHA256)
}

func TestRuntimeExportSourceOriginValidation(t *testing.T) {
	t.Parallel()

	require.Equal(t, "https://api.xiass.test", normalizeRuntimeExportSourceOrigin("https://api.xiass.test/a/b"))
	require.Empty(t, normalizeRuntimeExportSourceOrigin("ssh://api.xiass.test"))
}

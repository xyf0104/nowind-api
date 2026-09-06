package service

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestRuntimeExportServiceLifecycle(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	started := make(chan struct{})
	release := make(chan struct{})
	launcher := func(_ context.Context, outputPath, sourceOrigin, snapshot, runtimeContext string) error {
		require.Equal(t, "https://api.xiass.test", sourceOrigin)
		contextInfo, err := os.Stat(runtimeContext)
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0600), contextInfo.Mode().Perm())
		file, err := os.Open(snapshot)
		require.NoError(t, err)
		defer func() { _ = file.Close() }()
		info, err := file.Stat()
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
		reader, err := gzip.NewReader(file)
		require.NoError(t, err)
		data, err := io.ReadAll(reader)
		require.NoError(t, err)
		require.NoError(t, reader.Close())
		require.Equal(t, "current-authoritative-database", string(data))
		close(started)
		<-release
		return os.WriteFile(outputPath, []byte("portable-xiass-snapshot"), 0o600)
	}
	svc := newRuntimeExportService(directory, runtimeExportDumper{stream: &runtimeExportDumpStream{Reader: strings.NewReader("current-authoritative-database")}}, &config.Config{}, launcher, time.Now)

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
	require.Eventually(t, func() bool {
		files, err := filepath.Glob(filepath.Join(directory, ".postgres-*"))
		contexts, contextErr := filepath.Glob(filepath.Join(directory, ".runtime-context-*"))
		return err == nil && contextErr == nil && len(files) == 0 && len(contexts) == 0
	}, time.Second, time.Millisecond)
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

	svc := newRuntimeExportService(directory, nil, nil, func(context.Context, string, string, string, string) error { return nil }, func() time.Time {
		return time.Date(2026, 8, 31, 0, 1, 0, 0, time.UTC)
	})
	records, err := svc.List(context.Background())
	require.NoError(t, err)
	require.Len(t, records, 1)
	require.Equal(t, "completed", records[0].Status)
	require.Equal(t, int64(len("finished after restart")), records[0].SizeBytes)
	require.NotEmpty(t, records[0].SHA256)
}

type runtimeExportDumpStream struct {
	io.Reader
	closeErr error
	closed   bool
}

func (s *runtimeExportDumpStream) Close() error {
	s.closed = true
	return s.closeErr
}

type runtimeExportDumper struct {
	DBDumper
	stream io.ReadCloser
	err    error
}

func (d runtimeExportDumper) Dump(context.Context) (io.ReadCloser, error) { return d.stream, d.err }

type runtimeExportReadFailure struct{}

func (runtimeExportReadFailure) Read([]byte) (int, error) {
	return 0, errors.New("synthetic source read failed")
}

func TestRuntimeExportRejectsIncompleteDatabase(t *testing.T) {
	for _, failure := range []string{"start", "read", "process-exit", "empty", "archive"} {
		t.Run(failure, func(t *testing.T) {
			directory := t.TempDir()
			stream := &runtimeExportDumpStream{Reader: strings.NewReader("partial SQL")}
			dumper := runtimeExportDumper{stream: stream}
			switch failure {
			case "start":
				dumper.err = errors.New("synthetic connection failure")
			case "read":
				stream.Reader = runtimeExportReadFailure{}
			case "process-exit":
				stream.closeErr = errors.New("synthetic pg_dump exit failure")
			case "empty":
				stream.Reader = strings.NewReader("")
			}
			launched := false
			svc := newRuntimeExportService(directory, dumper, &config.Config{}, func(context.Context, string, string, string, string) error {
				launched = true
				return errors.New("synthetic archive failure")
			}, time.Now)
			id := "migration-" + strings.Repeat("b", 32)
			record := RuntimeExportRecord{ID: id, Status: "queued", FileName: runtimeExportFileName(id)}
			require.NoError(t, svc.saveRecordsLocked([]RuntimeExportRecord{record}))
			svc.execute(record, "https://example.invalid")
			require.Equal(t, failure == "archive", launched, "failed dumps must never launch a local fallback")
			records, err := svc.List(context.Background())
			require.NoError(t, err)
			require.Equal(t, "failed", records[0].Status)
			_, err = svc.Open(context.Background(), id)
			require.ErrorIs(t, err, ErrRuntimeExportNotReady)
			files, err := filepath.Glob(filepath.Join(directory, ".postgres-*"))
			require.NoError(t, err)
			require.Empty(t, files)
			require.Equal(t, failure != "start", stream.closed)
		})
	}
}

func TestRuntimeExportSourceOriginValidation(t *testing.T) {
	t.Parallel()

	require.Equal(t, "https://api.xiass.test", normalizeRuntimeExportSourceOrigin("https://api.xiass.test/a/b"))
	require.Empty(t, normalizeRuntimeExportSourceOrigin("ssh://api.xiass.test"))
}

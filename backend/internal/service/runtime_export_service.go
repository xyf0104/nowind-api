package service

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
)

const (
	defaultRuntimeExportDirectory = "/app/data/runtime-exports"
	runtimeExportIndexFileName    = "index.json"
	runtimeExportContainerPrefix  = "xiass-api-runtime-export-"
	runtimeExportTimeout          = 45 * time.Minute
)

var (
	ErrRuntimeExportInProgress = infraerrors.Conflict(
		"RUNTIME_EXPORT_IN_PROGRESS",
		"a full migration export is already running",
	)
	ErrRuntimeExportNotFound = infraerrors.NotFound(
		"RUNTIME_EXPORT_NOT_FOUND",
		"migration export was not found",
	)
	ErrRuntimeExportNotReady = infraerrors.Conflict(
		"RUNTIME_EXPORT_NOT_READY",
		"migration export is not ready for download",
	)
	ErrRuntimeExportUnsupported = infraerrors.ServiceUnavailable(
		"RUNTIME_EXPORT_UNAVAILABLE",
		"full migration export requires a Docker deployment with the Docker socket mounted",
	)
)

// RuntimeExportRecord is a locally stored, full XIASS migration package.
// The archive contains secrets, so it is deliberately downloadable only by an
// authenticated, step-up verified administrator.
type RuntimeExportRecord struct {
	ID           string `json:"id"`
	Status       string `json:"status"` // queued, running, completed, failed
	FileName     string `json:"file_name"`
	SizeBytes    int64  `json:"size_bytes"`
	SHA256       string `json:"sha256,omitempty"`
	StartedAt    string `json:"started_at"`
	FinishedAt   string `json:"finished_at,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
}

// RuntimeExportDownload owns an opened migration archive. Callers must close
// Reader after the HTTP response has been streamed.
type RuntimeExportDownload struct {
	Record RuntimeExportRecord
	Reader *os.File
}

// RuntimeExportManager is the narrowly scoped contract used by the admin
// handler. Keeping it separate from ordinary database backups makes the
// high-sensitivity migration package testable without granting handlers any
// direct Docker or filesystem access.
type RuntimeExportManager interface {
	Start(context.Context, string) (*RuntimeExportRecord, error)
	List(context.Context) ([]RuntimeExportRecord, error)
	Open(context.Context, string) (*RuntimeExportDownload, error)
	Delete(context.Context, string) error
}

type runtimeExportLauncher func(context.Context, string, string, string, string) error

// RuntimeExportService creates a portable logical snapshot without stopping
// the application. The short-lived updater container performs host-level
// Docker reads; the API uses its effective database connection to supply the
// logical snapshot, never a possibly stale local container selected by name.
type RuntimeExportService struct {
	directory string
	launcher  runtimeExportLauncher
	dumper    DBDumper
	cfg       *config.Config
	now       func() time.Time

	mu sync.Mutex
}

func NewRuntimeExportService(dumper DBDumper, cfg *config.Config) *RuntimeExportService {
	return newRuntimeExportService(defaultRuntimeExportDirectory, dumper, cfg, launchDockerRuntimeExport, time.Now)
}

func newRuntimeExportService(directory string, dumper DBDumper, cfg *config.Config, launcher runtimeExportLauncher, now func() time.Time) *RuntimeExportService {
	if strings.TrimSpace(directory) == "" {
		directory = defaultRuntimeExportDirectory
	}
	if now == nil {
		now = time.Now
	}
	return &RuntimeExportService{
		directory: filepath.Clean(directory),
		launcher:  launcher,
		dumper:    dumper,
		cfg:       cfg,
		now:       now,
	}
}

// Start launches one asynchronous export. It returns immediately so long
// database dumps and large browser profiles never hold an admin HTTP request
// open. The resulting package is retained locally until an administrator
// downloads or deletes it.
func (s *RuntimeExportService) Start(ctx context.Context, sourceOrigin string) (*RuntimeExportRecord, error) {
	if s == nil || s.launcher == nil || s.dumper == nil || s.cfg == nil {
		return nil, ErrRuntimeExportUnsupported
	}
	if err := s.ensureDirectory(); err != nil {
		return nil, fmt.Errorf("prepare migration export directory: %w", err)
	}

	s.mu.Lock()
	records, err := s.loadRecordsLocked()
	if err == nil {
		err = s.reconcileRecordsLocked(&records)
	}
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	for _, record := range records {
		if record.Status == "queued" || record.Status == "running" {
			s.mu.Unlock()
			return nil, ErrRuntimeExportInProgress
		}
	}

	createdAt := s.now().UTC()
	id := "migration-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	record := RuntimeExportRecord{
		ID:        id,
		Status:    "queued",
		FileName:  runtimeExportFileName(id),
		StartedAt: createdAt.Format(time.RFC3339),
	}
	records = append(records, record)
	if err := s.saveRecordsLocked(records); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	s.mu.Unlock()

	// The caller's request context may be canceled as soon as the response is
	// sent. The export itself has its own bounded lifecycle instead.
	go s.execute(record, normalizeRuntimeExportSourceOrigin(sourceOrigin))
	return &record, nil
}

func (s *RuntimeExportService) List(ctx context.Context) ([]RuntimeExportRecord, error) {
	_ = ctx
	if s == nil {
		return nil, ErrRuntimeExportUnsupported
	}
	if err := s.ensureDirectory(); err != nil {
		return nil, fmt.Errorf("prepare migration export directory: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	records, err := s.loadRecordsLocked()
	if err != nil {
		return nil, err
	}
	if err := s.reconcileRecordsLocked(&records); err != nil {
		return nil, err
	}
	sort.SliceStable(records, func(i, j int) bool {
		return records[i].StartedAt > records[j].StartedAt
	})
	return append([]RuntimeExportRecord(nil), records...), nil
}

func (s *RuntimeExportService) Open(ctx context.Context, id string) (*RuntimeExportDownload, error) {
	_ = ctx
	if s == nil {
		return nil, ErrRuntimeExportUnsupported
	}
	if !validRuntimeExportID(id) {
		return nil, ErrRuntimeExportNotFound
	}
	if err := s.ensureDirectory(); err != nil {
		return nil, fmt.Errorf("prepare migration export directory: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	records, err := s.loadRecordsLocked()
	if err != nil {
		return nil, err
	}
	if err := s.reconcileRecordsLocked(&records); err != nil {
		return nil, err
	}
	for _, record := range records {
		if record.ID != id {
			continue
		}
		if record.Status != "completed" {
			return nil, ErrRuntimeExportNotReady
		}
		file, err := os.Open(s.filePath(record))
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrRuntimeExportNotFound
		}
		if err != nil {
			return nil, fmt.Errorf("open migration export: %w", err)
		}
		return &RuntimeExportDownload{Record: record, Reader: file}, nil
	}
	return nil, ErrRuntimeExportNotFound
}

func (s *RuntimeExportService) Delete(ctx context.Context, id string) error {
	_ = ctx
	if s == nil {
		return ErrRuntimeExportUnsupported
	}
	if !validRuntimeExportID(id) {
		return ErrRuntimeExportNotFound
	}
	if err := s.ensureDirectory(); err != nil {
		return fmt.Errorf("prepare migration export directory: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	records, err := s.loadRecordsLocked()
	if err != nil {
		return err
	}
	if err := s.reconcileRecordsLocked(&records); err != nil {
		return err
	}
	for index, record := range records {
		if record.ID != id {
			continue
		}
		if record.Status == "queued" || record.Status == "running" {
			return ErrRuntimeExportInProgress
		}
		if err := os.Remove(s.filePath(record)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove migration export: %w", err)
		}
		records = append(records[:index], records[index+1:]...)
		return s.saveRecordsLocked(records)
	}
	return ErrRuntimeExportNotFound
}

func (s *RuntimeExportService) execute(record RuntimeExportRecord, sourceOrigin string) {
	s.updateRecord(record.ID, func(current *RuntimeExportRecord) {
		current.Status = "running"
		current.ErrorMessage = ""
	})

	ctx, cancel := context.WithTimeout(context.Background(), runtimeExportTimeout)
	snapshot, err := s.dumpDatabase(ctx)
	if err == nil {
		defer func() { _ = os.Remove(snapshot) }()
		var runtimeContext string
		runtimeContext, err = s.writeRuntimeContext()
		if err == nil {
			defer func() { _ = os.Remove(runtimeContext) }()
			err = s.launcher(ctx, s.filePath(record), sourceOrigin, snapshot, runtimeContext)
		}
	}
	cancel()
	if err != nil {
		s.updateRecord(record.ID, func(current *RuntimeExportRecord) {
			current.Status = "failed"
			current.FinishedAt = s.now().UTC().Format(time.RFC3339)
			current.ErrorMessage = "导出未完成，请检查当前数据库连接和服务器 Docker 状态后重试"
		})
		return
	}

	info, err := os.Stat(s.filePath(record))
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		s.updateRecord(record.ID, func(current *RuntimeExportRecord) {
			current.Status = "failed"
			current.FinishedAt = s.now().UTC().Format(time.RFC3339)
			current.ErrorMessage = "迁移包未生成，请检查服务器 Docker 状态后重试"
		})
		return
	}
	checksum, err := runtimeExportSHA256(s.filePath(record))
	if err != nil {
		s.updateRecord(record.ID, func(current *RuntimeExportRecord) {
			current.Status = "failed"
			current.FinishedAt = s.now().UTC().Format(time.RFC3339)
			current.ErrorMessage = "迁移包校验失败，请重新创建"
		})
		return
	}
	s.updateRecord(record.ID, func(current *RuntimeExportRecord) {
		current.Status = "completed"
		current.SizeBytes = info.Size()
		current.SHA256 = checksum
		current.FinishedAt = s.now().UTC().Format(time.RFC3339)
		current.ErrorMessage = ""
	})
}

func (s *RuntimeExportService) dumpDatabase(ctx context.Context) (path string, err error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	file, err := os.CreateTemp(s.directory, ".postgres-*.sql.gz")
	if err != nil {
		return "", err
	}
	path = file.Name()
	defer func() {
		err = errors.Join(err, file.Close())
		if err != nil {
			_ = os.Remove(path)
		}
	}()
	stream, err := s.dumper.Dump(ctx)
	if err != nil {
		return path, err
	}
	compressed := gzip.NewWriter(file)
	size, copyErr := io.Copy(compressed, stream)
	if copyErr != nil {
		cancel()
	}
	// pg_dump can emit partial stdout and fail afterwards. Close waits for its
	// exit status; neither a nonempty file nor a valid gzip proves completion.
	err = errors.Join(copyErr, stream.Close(), compressed.Close())
	if err == nil && size == 0 {
		err = errors.New("database export returned an empty snapshot")
	}
	if err == nil {
		err = file.Sync()
	}
	return path, err
}

func (s *RuntimeExportService) updateRecord(id string, update func(*RuntimeExportRecord)) {
	if s == nil || update == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	records, err := s.loadRecordsLocked()
	if err != nil {
		return
	}
	for index := range records {
		if records[index].ID != id {
			continue
		}
		update(&records[index])
		_ = s.saveRecordsLocked(records)
		return
	}
}

func (s *RuntimeExportService) reconcileRecordsLocked(records *[]RuntimeExportRecord) error {
	if records == nil {
		return nil
	}
	changed := false
	now := s.now().UTC()
	for index := range *records {
		record := &(*records)[index]
		if !validRuntimeExportID(record.ID) || record.FileName != runtimeExportFileName(record.ID) {
			record.Status = "failed"
			record.ErrorMessage = "迁移包记录无效"
			record.FinishedAt = now.Format(time.RFC3339)
			changed = true
			continue
		}

		info, statErr := os.Stat(s.filePath(*record))
		if statErr == nil && info.Mode().IsRegular() && info.Size() > 0 && (record.Status == "queued" || record.Status == "running") {
			checksum, checksumErr := runtimeExportSHA256(s.filePath(*record))
			if checksumErr == nil {
				record.Status = "completed"
				record.SizeBytes = info.Size()
				record.SHA256 = checksum
				record.FinishedAt = now.Format(time.RFC3339)
				record.ErrorMessage = ""
				changed = true
			}
			continue
		}

		if record.Status == "completed" && (errors.Is(statErr, os.ErrNotExist) || statErr == nil && (!info.Mode().IsRegular() || info.Size() == 0)) {
			record.Status = "failed"
			record.ErrorMessage = "迁移包文件已不存在"
			record.FinishedAt = now.Format(time.RFC3339)
			changed = true
			continue
		}
		if record.Status == "queued" || record.Status == "running" {
			startedAt, parseErr := time.Parse(time.RFC3339, record.StartedAt)
			if parseErr != nil || now.Sub(startedAt) > runtimeExportTimeout {
				record.Status = "failed"
				record.FinishedAt = now.Format(time.RFC3339)
				record.ErrorMessage = "导出超时，请重新创建"
				changed = true
			}
		}
	}
	if changed {
		return s.saveRecordsLocked(*records)
	}
	return nil
}

func (s *RuntimeExportService) ensureDirectory() error {
	if err := os.MkdirAll(s.directory, 0o700); err != nil {
		return err
	}
	return os.Chmod(s.directory, 0o700)
}

func (s *RuntimeExportService) indexPath() string {
	return filepath.Join(s.directory, runtimeExportIndexFileName)
}

func (s *RuntimeExportService) filePath(record RuntimeExportRecord) string {
	return filepath.Join(s.directory, runtimeExportFileName(record.ID))
}

func (s *RuntimeExportService) loadRecordsLocked() ([]RuntimeExportRecord, error) {
	payload, err := os.ReadFile(s.indexPath())
	if errors.Is(err, os.ErrNotExist) {
		return []RuntimeExportRecord{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read migration export index: %w", err)
	}
	var records []RuntimeExportRecord
	if err := json.Unmarshal(payload, &records); err != nil {
		return nil, fmt.Errorf("decode migration export index: %w", err)
	}
	return records, nil
}

func (s *RuntimeExportService) saveRecordsLocked(records []RuntimeExportRecord) error {
	payload, err := json.Marshal(records)
	if err != nil {
		return fmt.Errorf("encode migration export index: %w", err)
	}
	temporary, err := os.CreateTemp(s.directory, ".runtime-exports-*.tmp")
	if err != nil {
		return fmt.Errorf("create migration export index: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("secure migration export index: %w", err)
	}
	if _, err := temporary.Write(payload); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write migration export index: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close migration export index: %w", err)
	}
	if err := os.Rename(temporaryPath, s.indexPath()); err != nil {
		return fmt.Errorf("replace migration export index: %w", err)
	}
	return nil
}

func runtimeExportFileName(id string) string {
	return id + ".tar.gz"
}

func validRuntimeExportID(id string) bool {
	if !strings.HasPrefix(id, "migration-") || len(id) != len("migration-")+32 {
		return false
	}
	for _, character := range id[len("migration-"):] {
		if (character < 'a' || character > 'f') && (character < '0' || character > '9') {
			return false
		}
	}
	return true
}

func runtimeExportSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func normalizeRuntimeExportSourceOrigin(value string) string {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed == nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Host
}

func launchDockerRuntimeExport(ctx context.Context, outputPath, sourceOrigin, snapshot, runtimeContext string) error {
	return launchDockerRuntimeExportWithClient(ctx, newDockerUpdateClient(), outputPath, sourceOrigin, snapshot, runtimeContext)
}

func launchDockerRuntimeExportWithClient(ctx context.Context, client *dockerUpdateClient, outputPath, sourceOrigin, snapshot, runtimeContext string) error {
	if client == nil || strings.TrimSpace(outputPath) == "" {
		return ErrRuntimeExportUnsupported
	}
	if _, err := os.Stat(dockerUpdateSocketPath); err != nil {
		return ErrRuntimeExportUnsupported.WithCause(err)
	}
	fileName := filepath.Base(outputPath)
	if fileName != runtimeExportFileName(strings.TrimSuffix(fileName, ".tar.gz")) || !validRuntimeExportID(strings.TrimSuffix(fileName, ".tar.gz")) {
		return fmt.Errorf("invalid migration export path")
	}
	if !filepath.IsAbs(snapshot) || filepath.Dir(snapshot) != filepath.Dir(outputPath) || !strings.HasPrefix(filepath.Base(snapshot), ".postgres-") {
		return fmt.Errorf("invalid database snapshot path")
	}
	if !filepath.IsAbs(runtimeContext) || filepath.Dir(runtimeContext) != filepath.Dir(outputPath) || !strings.HasPrefix(filepath.Base(runtimeContext), ".runtime-context-") {
		return fmt.Errorf("invalid runtime context path")
	}

	appContainerName, appContainer, err := client.findAppContainer(ctx)
	if err != nil || appContainer == nil || !appContainer.State.Running {
		return ErrRuntimeExportUnsupported
	}
	installDir, err := client.discoverInstallDir(ctx)
	if err != nil {
		return ErrRuntimeExportUnsupported.WithCause(err)
	}
	if err := client.pullImage(ctx, updaterImage()); err != nil {
		return fmt.Errorf("prepare runtime exporter: %w", err)
	}

	containerName := runtimeExportContainerPrefix + strings.TrimSuffix(fileName, ".tar.gz")
	if existing, inspectErr := client.inspect(ctx, containerName); inspectErr == nil {
		if existing.State.Running {
			return ErrRuntimeExportInProgress
		}
		_, _ = client.requestOK(ctx, http.MethodDelete, "/containers/"+url.PathEscape(containerName)+"?force=1", nil, http.StatusNoContent, http.StatusNotFound)
	}

	create := dockerUpdateContainerCreateRequest{
		Image: updaterImage(),
		Cmd: []string{
			"runtime-export",
			"--output",
			"/tmp/" + fileName,
			"--postgres-from-container",
			appContainerName + ":" + snapshot,
			"--runtime-context-from-container",
			appContainerName + ":" + runtimeContext,
			"--publish-to-container",
			appContainerName + ":/app/data/runtime-exports/" + fileName,
		},
		Env: []string{
			"INSTALL_DIR=" + installDir,
			"XIASS_SOURCE_ORIGIN=" + normalizeRuntimeExportSourceOrigin(sourceOrigin),
		},
		WorkingDir: installDir,
		Labels: map[string]string{
			"com.xiass.role":              "runtime-export",
			"com.xiass.runtime_export_id": strings.TrimSuffix(fileName, ".tar.gz"),
		},
	}
	create.HostConfig.Binds = []string{
		dockerUpdateSocketPath + ":" + dockerUpdateSocketPath,
		installDir + ":" + installDir + ":ro",
	}
	create.HostConfig.NetworkMode = "none"

	createdPayload, err := client.requestOK(ctx, http.MethodPost, "/containers/create?name="+url.QueryEscape(containerName), &create, http.StatusCreated)
	if err != nil {
		return fmt.Errorf("create runtime exporter: %w", err)
	}
	var created struct {
		ID string `json:"Id"`
	}
	if err := json.Unmarshal(createdPayload, &created); err != nil || strings.TrimSpace(created.ID) == "" {
		return fmt.Errorf("runtime exporter creation returned no id")
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_, _ = client.requestOK(cleanupCtx, http.MethodDelete, "/containers/"+url.PathEscape(created.ID)+"?force=1", nil, http.StatusNoContent, http.StatusNotFound)
	}()
	if _, err := client.requestOK(ctx, http.MethodPost, "/containers/"+url.PathEscape(created.ID)+"/start", nil, http.StatusNoContent, http.StatusNotModified); err != nil {
		return fmt.Errorf("start runtime exporter: %w", err)
	}

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		info, err := client.inspect(ctx, created.ID)
		if err != nil {
			return fmt.Errorf("inspect runtime exporter: %w", err)
		}
		if !info.State.Running {
			if info.State.ExitCode != 0 {
				return fmt.Errorf("runtime exporter exited with status %d", info.State.ExitCode)
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (c *dockerUpdateClient) findAppContainer(ctx context.Context) (string, *dockerUpdateContainerInfo, error) {
	candidates := []string{}
	if hostname, err := os.Hostname(); err == nil && strings.TrimSpace(hostname) != "" {
		candidates = append(candidates, hostname)
	}
	candidates = append(candidates, "xiass-api", "nowind-api", "sub2api")
	for _, candidate := range candidates {
		info, err := c.inspect(ctx, candidate)
		if err == nil {
			return candidate, info, nil
		}
	}
	return "", nil, fmt.Errorf("could not discover XIASS application container")
}

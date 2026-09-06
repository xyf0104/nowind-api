package main

import (
	"crypto/rand"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed web/*.html
var webFiles embed.FS

type helperServer struct {
	manager                    *ConfigManager
	applyConfig                func(ApplyConfig) (ApplyResult, error)
	restoreConfig              func(string) (RestoreResult, error)
	repairHistory              func() (HistoryRepairResult, error)
	repairCompatibilityHistory func() (HistoryRepairResult, error)
	restoreHistory             func(string) error
	listHistoryBackups         func() ([]HistoryBackupInfo, error)
	deleteConfigBackup         func(string) error
	deleteHistoryBackup        func(string) error
	listModels                 func(string, string) (discoveredModelCatalog, error)
	modelCatalogMu             sync.Mutex
	modelCatalogKey            string
	modelCatalogFetchedAt      time.Time
	modelCatalogCache          discoveredModelCatalog
	state                      string
	operationMu                sync.Mutex
	siteMu                     sync.RWMutex
	siteURL                    *url.URL
	contextMu                  sync.RWMutex
	pendingContext             *ContextSettings
	codexMu                    sync.RWMutex
	selectedCodex              *CodexInstallation
	index                      *template.Template
	callback                   []byte
	shutdown                   chan struct{}
	shutdownOnce               sync.Once
	detect                     func() CodexInstallation
	selectApp                  func() (CodexInstallation, error)
	selectAppPath              func(string) (CodexInstallation, error)
	prepare                    func() error
	stop                       func(CodexInstallation) error
	start                      func(CodexInstallation) error
}

type statusResponse struct {
	Version                    string            `json:"version"`
	ConfigPath                 string            `json:"config_path"`
	Codex                      CodexInstallation `json:"codex"`
	ConnectURL                 string            `json:"connect_url"`
	SiteURL                    string            `json:"site_url"`
	ModelContextWindow         int64             `json:"model_context_window"`
	ModelAutoCompactTokenLimit int64             `json:"model_auto_compact_token_limit"`
	ContextSettingsWarning     string            `json:"context_settings_warning,omitempty"`
}

type operationResponse struct {
	OK             bool                 `json:"ok"`
	Message        string               `json:"message"`
	BackupID       string               `json:"backup_id,omitempty"`
	SafetyBackupID string               `json:"safety_backup_id,omitempty"`
	Restarted      bool                 `json:"restarted"`
	ConfigVerified bool                 `json:"config_verified"`
	History        *HistoryRepairResult `json:"history,omitempty"`
}

type historyBackupsResponse struct {
	Items []HistoryBackupInfo `json:"items"`
}

const xiassCodexEnabledModel = "gpt-6-astra"

func newHelperServer(manager *ConfigManager, site string, state string) (*helperServer, error) {
	var parsedSite *url.URL
	if strings.TrimSpace(site) != "" {
		var err error
		parsedSite, err = parseSiteURL(site)
		if err != nil {
			return nil, err
		}
	}
	indexBytes, err := webFiles.ReadFile("web/index.html")
	if err != nil {
		return nil, err
	}
	callback, err := webFiles.ReadFile("web/callback.html")
	if err != nil {
		return nil, err
	}
	index, err := template.New("index").Parse(string(indexBytes))
	if err != nil {
		return nil, err
	}
	return &helperServer{
		manager:       manager,
		applyConfig:   manager.ApplyWithoutBackup,
		listModels:    discoverCompatibleModelCatalog,
		state:         state,
		siteURL:       parsedSite,
		index:         index,
		callback:      callback,
		shutdown:      make(chan struct{}),
		detect:        detectCodexInstallation,
		selectApp:     selectCodexInstallation,
		selectAppPath: selectCodexInstallationPath,
		prepare:       prepareCodexOperation,
		stop:          stopCodex,
		start:         startCodex,
	}, nil
}

func (s *helperServer) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", s.handleIndex)
	mux.HandleFunc("GET /callback", s.handleCallback)
	mux.HandleFunc("GET /api/status", s.handleStatus)
	mux.HandleFunc("POST /api/models", s.handleModels)
	mux.HandleFunc("POST /api/site", s.handleSite)
	mux.HandleFunc("POST /api/select-app", s.handleSelectApp)
	mux.HandleFunc("POST /api/apply", s.handleApply)
	mux.HandleFunc("POST /api/apply-manual", s.handleManualApply)
	mux.HandleFunc("POST /api/shutdown", s.handleShutdown)
	mux.HandleFunc("POST /api/browser-closed", s.handleBrowserClosed)
	return s.localOnly(s.securityHeaders(mux))
}

func (s *helperServer) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	siteURL := defaultXIASSAPIURL
	if configured := s.currentSiteURL(); configured != nil {
		siteURL = configured.String()
	}
	_ = s.index.Execute(w, map[string]string{
		"State":   s.state,
		"SiteURL": siteURL,
	})
}

func (s *helperServer) handleCallback(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(s.callback)
}

func (s *helperServer) handleStatus(w http.ResponseWriter, r *http.Request) {
	connectURL, siteURL := s.connectionDetails(r.Host)
	contextSettings := defaultContextSettings()
	contextWarning := ""
	if s.manager.UsesXIASSProvider() {
		configured, contextErr := s.manager.ReadContextSettings()
		if contextErr != nil {
			contextWarning = contextErr.Error()
		} else {
			contextSettings = configured
		}
	}
	if pending := s.pendingContextSettings(); pending != nil {
		contextSettings = *pending
		contextWarning = ""
	}
	writeJSON(w, http.StatusOK, statusResponse{
		Version:                    version,
		ConfigPath:                 s.manager.ConfigPath,
		Codex:                      s.codexInstallation(),
		ConnectURL:                 connectURL,
		SiteURL:                    siteURL,
		ModelContextWindow:         contextSettings.ModelContextWindow,
		ModelAutoCompactTokenLimit: contextSettings.ModelAutoCompactTokenLimit,
		ContextSettingsWarning:     contextWarning,
	})
}

func (s *helperServer) handleSite(w http.ResponseWriter, r *http.Request) {
	if !s.validState(r) {
		writeError(w, http.StatusForbidden, errors.New("invalid local helper session"))
		return
	}
	var request struct {
		SiteURL                    string `json:"site_url"`
		ModelContextWindow         int64  `json:"model_context_window,omitempty"`
		ModelAutoCompactTokenLimit int64  `json:"model_auto_compact_token_limit,omitempty"`
	}
	if err := decodeJSONBody(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	parsed, err := parseSiteURL(request.SiteURL)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	contextSettings, err := s.resolveRequestedContext(ContextSettings{
		ModelContextWindow:         request.ModelContextWindow,
		ModelAutoCompactTokenLimit: request.ModelAutoCompactTokenLimit,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	s.siteMu.Lock()
	s.siteURL = parsed
	s.siteMu.Unlock()
	s.setPendingContext(contextSettings)
	connectURL, siteURL := s.connectionDetails(r.Host)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":          true,
		"site_url":    siteURL,
		"connect_url": connectURL,
	})
}

func (s *helperServer) handleSelectApp(w http.ResponseWriter, r *http.Request) {
	if !s.validState(r) {
		writeError(w, http.StatusForbidden, errors.New("invalid local helper session"))
		return
	}
	var request struct {
		Path string `json:"path"`
	}
	if err := decodeJSONBody(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	var (
		installation CodexInstallation
		err          error
	)
	if strings.TrimSpace(request.Path) != "" {
		installation, err = s.selectAppPath(request.Path)
	} else {
		installation, err = s.selectApp()
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	s.codexMu.Lock()
	s.selectedCodex = &installation
	s.codexMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":    true,
		"codex": installation,
	})
}

func (s *helperServer) codexInstallation() CodexInstallation {
	s.codexMu.RLock()
	if s.selectedCodex != nil {
		installation := *s.selectedCodex
		s.codexMu.RUnlock()
		return installation
	}
	s.codexMu.RUnlock()
	return s.detect()
}

func (s *helperServer) handleModels(w http.ResponseWriter, r *http.Request) {
	if !s.validState(r) {
		writeError(w, http.StatusForbidden, errors.New("invalid local helper session"))
		return
	}
	var request struct {
		BaseURL string `json:"base_url"`
		APIKey  string `json:"api_key"`
	}
	if err := decodeJSONBody(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	normalized, err := normalizeApplyConfig(ApplyConfig{
		BaseURL: request.BaseURL,
		APIKey:  request.APIKey,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	catalog, err := s.loadModelCatalog(normalized.BaseURL, normalized.APIKey)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	models := s.augmentConfiguredXIASSModels(normalized.BaseURL, catalog.IDs)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "models": models})
}

// augmentConfiguredXIASSModels keeps the local helper compatible with an older
// XIASS API deployment whose Codex catalog predates a model enabled for the
// canonical XIASS site. Arbitrary compatible APIs remain authoritative for
// their own model catalog and are never augmented.
func (s *helperServer) augmentConfiguredXIASSModels(baseURL string, models []string) []string {
	site := s.currentSiteURL()
	if site == nil {
		return models
	}
	canonical, err := url.Parse(defaultXIASSAPIURL)
	if err != nil || !strings.EqualFold(site.Hostname(), canonical.Hostname()) {
		return models
	}
	parsedBase, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsedBase.Scheme != site.Scheme || !strings.EqualFold(parsedBase.Host, site.Host) {
		return models
	}
	basePath := strings.TrimRight(parsedBase.Path, "/")
	sitePath := strings.TrimRight(site.Path, "/")
	if basePath != sitePath && basePath != sitePath+"/v1" {
		return models
	}

	seen := make(map[string]struct{}, len(models)+1)
	augmented := make([]string, 0, len(models)+1)
	for _, raw := range models {
		model := strings.TrimSpace(raw)
		if model == "" {
			continue
		}
		if _, exists := seen[model]; exists {
			continue
		}
		seen[model] = struct{}{}
		augmented = append(augmented, model)
	}
	if _, exists := seen[xiassCodexEnabledModel]; !exists {
		augmented = append(augmented, xiassCodexEnabledModel)
	}
	sort.Strings(augmented)
	return augmented
}

func (s *helperServer) handleApply(w http.ResponseWriter, r *http.Request) {
	s.handleApplyRequest(w, r, false)
}

func (s *helperServer) handleManualApply(w http.ResponseWriter, r *http.Request) {
	s.handleApplyRequest(w, r, true)
}

func (s *helperServer) handleApplyRequest(w http.ResponseWriter, r *http.Request, manual bool) {
	if !s.validState(r) {
		writeError(w, http.StatusForbidden, errors.New("invalid local helper session"))
		return
	}
	var input ApplyConfig
	if err := decodeJSONBody(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if manual {
		input.ProviderName = "Custom API"
	} else {
		// Website-assisted XIASS setup always takes ownership of the active
		// provider. Existing unrelated settings and inactive provider definitions
		// remain in the configuration.
		input.ProviderName = providerName
		input.ForceCanonicalProvider = true
	}
	input = s.fillMissingContext(input)
	normalized, err := normalizeApplyConfig(input)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	input = normalized
	if !manual {
		site := s.currentSiteURL()
		if site == nil {
			writeError(w, http.StatusBadRequest, errors.New("XIASS API site has not been configured"))
			return
		}
		parsedBase, err := url.Parse(input.BaseURL)
		if err != nil || parsedBase.Scheme != "https" || !strings.EqualFold(parsedBase.Host, site.Host) {
			writeError(w, http.StatusBadRequest, errors.New("configuration does not belong to this XIASS API site"))
			return
		}
	}
	// Picker IDs are not a metadata source. Reuse helper-side discovery for the
	// same connection. A failed fetch falls back
	// to native metadata without manufacturing known-model instructions.
	if catalog, discoveryErr := s.loadModelCatalog(input.BaseURL, input.APIKey); discoveryErr == nil {
		input.ModelCatalogDescriptors = catalog.Descriptors
		if len(input.ModelCatalogModels) == 0 {
			input.ModelCatalogModels = s.augmentConfiguredXIASSModels(input.BaseURL, catalog.IDs)
		}
	}

	if !s.beginOperation(w) {
		return
	}
	defer s.operationMu.Unlock()
	releaseLifecycle, err := acquireLifecycleLock(filepath.Dir(s.manager.ConfigPath))
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	defer releaseLifecycle()

	if err := s.prepare(); err != nil {
		writeError(w, http.StatusConflict, fmt.Errorf("无法安全退出会改写配置的第三方管理工具：%w", err))
		return
	}
	installation := s.codexInstallation()
	if err := s.stop(installation); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("无法安全关闭 Codex，配置未修改：%w", err))
		return
	}
	result, err := s.applyConfig(input)
	if err != nil {
		if configRollbackFailed(err) {
			writeJSON(w, http.StatusInternalServerError, operationResponse{
				OK:             false,
				Message:        fmt.Sprintf("配置写入失败，内存回滚也未能确认成功：%v。Codex 保持关闭，请先检查 config.toml 后再重新打开。", err),
				ConfigVerified: false,
			})
			return
		}
		startErr := s.startWithRetry(installation)
		writeJSON(w, http.StatusInternalServerError, operationResponse{
			OK:             false,
			Message:        operationFailure("配置未写入，原配置保持不变", err, startErr).Error(),
			Restarted:      startErr == nil,
			ConfigVerified: true,
		})
		return
	}
	if err := s.startWithRetry(installation); err != nil {
		writeJSON(w, http.StatusInternalServerError, operationResponse{
			OK:             false,
			Message:        fmt.Sprintf("配置已写入并校验，但 Codex 自动启动失败：%v。请手动打开 Codex。", err),
			Restarted:      false,
			ConfigVerified: true,
		})
		return
	}
	configurationName := "XIASS API"
	if manual {
		configurationName = "自定义 API"
	}
	catalogNotice := ""
	if result.CatalogSHA == "" {
		catalogNotice = "已知模型的完整元数据暂不可用，未覆盖 Codex 原生模型目录；自定义模型列表可能暂不可见。"
	}
	writeJSON(w, http.StatusOK, operationResponse{
		OK:             true,
		Message:        configurationName + " 配置已写入并校验（上下文 " + formatTokenCount(input.ModelContextWindow) + "，自动压缩 " + formatTokenCount(input.ModelAutoCompactTokenLimit) + "）；" + catalogNotice + "没有扫描、备份或改写任何会话、索引和数据库。Codex 已自动重新启动。",
		Restarted:      true,
		ConfigVerified: true,
	})
	s.clearPendingContext()
}

func parseSiteURL(value string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil {
		return nil, errors.New("site must be a valid HTTPS URL")
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return parsed, nil
}

// Keep one bounded, short-lived catalog in memory. Templates never cross the
// browser picker/callback boundary and cannot leak across URLs or API keys.
func (s *helperServer) loadModelCatalog(baseURL, apiKey string) (discoveredModelCatalog, error) {
	s.modelCatalogMu.Lock()
	defer s.modelCatalogMu.Unlock()
	key := sha256Hex([]byte(baseURL + "\x00" + apiKey))
	if key == s.modelCatalogKey && time.Since(s.modelCatalogFetchedAt) < 5*time.Minute {
		return s.modelCatalogCache, nil
	}
	catalog, err := s.listModels(baseURL, apiKey)
	if err != nil {
		return discoveredModelCatalog{}, err
	}
	s.modelCatalogKey, s.modelCatalogFetchedAt, s.modelCatalogCache = key, time.Now(), catalog
	return catalog, nil
}

func discoverCompatibleModels(baseURL, apiKey string) ([]string, error) {
	catalog, err := discoverCompatibleModelCatalog(baseURL, apiKey)
	return catalog.IDs, err
}

func discoverCompatibleModelCatalog(baseURL, apiKey string) (discoveredModelCatalog, error) {
	endpoint, err := url.Parse(baseURL)
	if err != nil || endpoint.Scheme == "" || endpoint.Host == "" {
		return discoveredModelCatalog{}, errors.New("invalid compatible API base URL")
	}
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/models"
	query := endpoint.Query()
	// XIASS uses the Codex manifest when client_version is present. A generic
	// OpenAI-compatible endpoint may ignore this parameter and still return the
	// standard data[].id catalog, which is handled below as well.
	query.Set("client_version", "0.146.0")
	endpoint.RawQuery = query.Encode()
	endpoint.Fragment = ""

	request, err := http.NewRequest(http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return discoveredModelCatalog{}, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+apiKey)
	client := &http.Client{
		Timeout: 8 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	response, err := client.Do(request)
	if err != nil {
		return discoveredModelCatalog{}, errors.New("request compatible API models failed")
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return discoveredModelCatalog{}, fmt.Errorf("compatible API model list returned HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, modelCatalogMaxBytes+1))
	if err != nil || len(body) > modelCatalogMaxBytes {
		return discoveredModelCatalog{}, errors.New("compatible API catalog exceeds the size limit or could not be read")
	}
	return parseDiscoveredModelCatalog(body)
}

func (s *helperServer) currentSiteURL() *url.URL {
	s.siteMu.RLock()
	defer s.siteMu.RUnlock()
	if s.siteURL == nil {
		return nil
	}
	copy := *s.siteURL
	return &copy
}

func (s *helperServer) resolveRequestedContext(requested ContextSettings) (ContextSettings, error) {
	current := defaultContextSettings()
	if s.manager.UsesXIASSProvider() {
		if configured, err := s.manager.ReadContextSettings(); err == nil {
			current = configured
		}
	}
	if requested.ModelContextWindow == 0 {
		requested.ModelContextWindow = current.ModelContextWindow
		if requested.ModelAutoCompactTokenLimit == 0 {
			requested.ModelAutoCompactTokenLimit = current.ModelAutoCompactTokenLimit
		}
	}
	return normalizeContextSettings(requested)
}

func (s *helperServer) fillMissingContext(input ApplyConfig) ApplyConfig {
	if input.ModelContextWindow != 0 && input.ModelAutoCompactTokenLimit != 0 {
		return input
	}
	if input.ModelContextWindow != 0 {
		// Preserve the zero compact limit so normalization derives a 90% limit.
		return input
	}
	settings := s.pendingContextSettings()
	if settings == nil {
		resolved := defaultContextSettings()
		if s.manager.UsesXIASSProvider() {
			if configured, err := s.manager.ReadContextSettings(); err == nil {
				resolved = configured
			}
		}
		settings = &resolved
	}
	if input.ModelContextWindow == 0 {
		input.ModelContextWindow = settings.ModelContextWindow
	}
	if input.ModelAutoCompactTokenLimit == 0 {
		input.ModelAutoCompactTokenLimit = settings.ModelAutoCompactTokenLimit
	}
	return input
}

func (s *helperServer) setPendingContext(settings ContextSettings) {
	s.contextMu.Lock()
	copy := settings
	s.pendingContext = &copy
	s.contextMu.Unlock()
}

func (s *helperServer) pendingContextSettings() *ContextSettings {
	s.contextMu.RLock()
	defer s.contextMu.RUnlock()
	if s.pendingContext == nil {
		return nil
	}
	copy := *s.pendingContext
	return &copy
}

func (s *helperServer) clearPendingContext() {
	s.contextMu.Lock()
	s.pendingContext = nil
	s.contextMu.Unlock()
}

func formatTokenCount(value int64) string {
	formatted := strconv.FormatInt(value, 10)
	for index := len(formatted) - 3; index > 0; index -= 3 {
		formatted = formatted[:index] + "," + formatted[index:]
	}
	return formatted
}

func (s *helperServer) connectionDetails(callbackHost string) (string, string) {
	site := s.currentSiteURL()
	if site == nil {
		return "", ""
	}
	connect := *site
	connect.Path = "/codex-helper/connect"
	query := connect.Query()
	query.Set("callback", "http://"+callbackHost+"/callback")
	query.Set("state", s.state)
	connect.RawQuery = query.Encode()
	connect.Fragment = ""
	return connect.String(), site.String()
}

func (s *helperServer) handleRestore(w http.ResponseWriter, r *http.Request) {
	if !s.validState(r) {
		writeError(w, http.StatusForbidden, errors.New("invalid local helper session"))
		return
	}
	var request struct {
		BackupID string `json:"backup_id"`
	}
	if err := decodeJSONBody(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if !s.beginOperation(w) {
		return
	}
	defer s.operationMu.Unlock()
	releaseLifecycle, err := acquireLifecycleLock(filepath.Dir(s.manager.ConfigPath))
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	defer releaseLifecycle()

	if err := s.prepare(); err != nil {
		writeError(w, http.StatusConflict, fmt.Errorf("无法安全退出会改写会话索引的第三方管理工具：%w", err))
		return
	}
	installation := s.codexInstallation()
	if err := s.stop(installation); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("Codex could not be stopped safely; no configuration was restored: %w", err))
		return
	}
	previousHistoryProvider := historyProviderForConfig(s.manager.ConfigPath)
	result, err := s.restoreConfig(request.BackupID)
	if err != nil {
		if configRollbackFailed(err) {
			writeJSON(w, http.StatusInternalServerError, operationResponse{
				OK:             false,
				Message:        fmt.Sprintf("配置恢复失败，自动回滚也未能确认成功：%v。Codex 保持关闭，避免使用不确定的配置启动。", err),
				ConfigVerified: false,
			})
			return
		}
		startErr := s.startWithRetry(installation)
		writeError(w, http.StatusInternalServerError, operationFailure("configuration was not restored", err, startErr))
		return
	}
	_, upgradedLegacyProvider, err := s.manager.UpgradeLegacyProvider()
	if err != nil {
		_, rollbackErr := s.restoreConfig(result.SafetyBackupID)
		if rollbackErr != nil {
			writeJSON(w, http.StatusInternalServerError, operationResponse{
				OK:             false,
				Message:        fmt.Sprintf("旧版 XIASS 配置升级失败：%v；恢复前配置回滚也失败：%v。Codex 保持关闭。", err, rollbackErr),
				BackupID:       result.RestoredBackupID,
				SafetyBackupID: result.SafetyBackupID,
			})
			return
		}
		startErr := s.startWithRetry(installation)
		writeJSON(w, http.StatusInternalServerError, operationResponse{
			OK:             false,
			Message:        operationFailure("旧版 XIASS 配置无法安全升级，已恢复到操作前状态", err, startErr).Error(),
			BackupID:       result.RestoredBackupID,
			SafetyBackupID: result.SafetyBackupID,
			Restarted:      startErr == nil,
			ConfigVerified: true,
		})
		return
	}
	var history HistoryRepairResult
	if upgradedLegacyProvider {
		history, err = s.repairHistory()
	} else {
		history, err = s.repairHistoryForConfigChange(previousHistoryProvider)
	}
	if err != nil {
		historyRollbackUnsafe := historyRollbackFailed(err)
		_, rollbackErr := s.restoreConfig(result.SafetyBackupID)
		if rollbackErr != nil {
			writeJSON(w, http.StatusInternalServerError, operationResponse{
				OK:             false,
				Message:        fmt.Sprintf("History repair failed: %v. Configuration rollback also failed: %v. Codex was left closed to protect the existing conversations.", err, rollbackErr),
				BackupID:       result.RestoredBackupID,
				SafetyBackupID: result.SafetyBackupID,
			})
			return
		}
		if historyRollbackUnsafe {
			writeJSON(w, http.StatusInternalServerError, operationResponse{
				OK:             false,
				Message:        "历史会话修复和自动回滚均失败。恢复前配置已还原，但 Codex 保持关闭，避免在会话索引不一致时启动。",
				BackupID:       result.RestoredBackupID,
				SafetyBackupID: result.SafetyBackupID,
				ConfigVerified: true,
			})
			return
		}
		startErr := s.startWithRetry(installation)
		writeJSON(w, http.StatusInternalServerError, operationResponse{
			OK:             false,
			Message:        operationFailure("History repair failed, so the pre-restore configuration and conversations were restored", err, startErr).Error(),
			BackupID:       result.RestoredBackupID,
			SafetyBackupID: result.SafetyBackupID,
			Restarted:      startErr == nil,
			ConfigVerified: true,
		})
		return
	}
	if err := s.startWithRetry(installation); err != nil {
		if stopErr := s.stopBeforeRollback(installation); stopErr != nil {
			writeJSON(w, http.StatusInternalServerError, operationResponse{
				OK:             false,
				Message:        fmt.Sprintf("恢复状态启动检测失败：%v；无法再次确认 Codex 已退出：%v。为避免在线覆盖数据库，未执行回滚。", err, stopErr),
				BackupID:       result.RestoredBackupID,
				SafetyBackupID: result.SafetyBackupID,
				ConfigVerified: true,
				History:        &history,
			})
			return
		}
		historyRollbackErr := s.restoreHistory(history.BackupID)
		_, configRollbackErr := s.restoreConfig(result.SafetyBackupID)
		if historyRollbackErr != nil || configRollbackErr != nil {
			writeJSON(w, http.StatusInternalServerError, operationResponse{
				OK:             false,
				Message:        fmt.Sprintf("恢复后的配置无法启动：%v；恢复到操作前状态也未完整完成（历史：%v，配置：%v）。Codex 保持关闭。", err, historyRollbackErr, configRollbackErr),
				BackupID:       result.RestoredBackupID,
				SafetyBackupID: result.SafetyBackupID,
				ConfigVerified: configRollbackErr == nil,
			})
			return
		}
		recoveryStartErr := s.startWithRetry(installation)
		writeJSON(w, http.StatusInternalServerError, operationResponse{
			OK:             false,
			Message:        operationFailure("所选恢复状态无法启动，已回到恢复前配置和历史会话", err, recoveryStartErr).Error(),
			BackupID:       result.RestoredBackupID,
			SafetyBackupID: result.SafetyBackupID,
			Restarted:      recoveryStartErr == nil,
			ConfigVerified: true,
		})
		return
	}
	writeJSON(w, http.StatusOK, operationResponse{
		OK:             true,
		Message:        "原配置已恢复；" + historySummary(history) + " Codex 已重新启动。",
		BackupID:       result.RestoredBackupID,
		SafetyBackupID: result.SafetyBackupID,
		Restarted:      true,
		ConfigVerified: true,
		History:        &history,
	})
}

func (s *helperServer) handleRepairHistory(w http.ResponseWriter, r *http.Request) {
	if !s.validState(r) {
		writeError(w, http.StatusForbidden, errors.New("invalid local helper session"))
		return
	}
	if !s.beginOperation(w) {
		return
	}
	defer s.operationMu.Unlock()
	releaseLifecycle, err := acquireLifecycleLock(filepath.Dir(s.manager.ConfigPath))
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	defer releaseLifecycle()

	if err := s.prepare(); err != nil {
		writeError(w, http.StatusConflict, fmt.Errorf("无法安全退出会改写会话索引的第三方管理工具：%w", err))
		return
	}
	installation := s.codexInstallation()
	if err := s.stop(installation); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("Codex could not be stopped safely; no conversation data was changed: %w", err))
		return
	}
	history, err := s.repairCompatibilityHistory()
	if err != nil {
		if historyRollbackFailed(err) {
			writeJSON(w, http.StatusInternalServerError, operationResponse{
				OK:      false,
				Message: "历史会话修复和自动回滚均失败。Codex 保持关闭，请使用已创建的历史备份恢复后再启动。",
			})
			return
		}
		startErr := s.startWithRetry(installation)
		writeJSON(w, http.StatusInternalServerError, operationResponse{
			OK:        false,
			Message:   operationFailure("Conversation history repair failed and all attempted changes were rolled back", err, startErr).Error(),
			Restarted: startErr == nil,
		})
		return
	}
	if err := s.startWithRetry(installation); err != nil {
		if stopErr := s.stopBeforeRollback(installation); stopErr != nil {
			writeJSON(w, http.StatusInternalServerError, operationResponse{
				OK:      false,
				Message: fmt.Sprintf("历史修复后的启动检测失败：%v；无法再次确认 Codex 已退出：%v。为避免在线覆盖数据库，未执行回滚。", err, stopErr),
				History: &history,
			})
			return
		}
		if rollbackErr := s.restoreHistory(history.BackupID); rollbackErr != nil {
			writeJSON(w, http.StatusInternalServerError, operationResponse{
				OK:      false,
				Message: fmt.Sprintf("历史修复后 Codex 无法启动：%v；历史快照回滚也失败：%v。Codex 保持关闭。", err, rollbackErr),
			})
			return
		}
		recoveryStartErr := s.startWithRetry(installation)
		writeJSON(w, http.StatusInternalServerError, operationResponse{
			OK:        false,
			Message:   operationFailure("历史修复后 Codex 无法启动，已恢复修复前历史", err, recoveryStartErr).Error(),
			Restarted: recoveryStartErr == nil,
		})
		return
	}
	writeJSON(w, http.StatusOK, operationResponse{
		OK:        true,
		Message:   historySummary(history) + " Codex 已重新启动。",
		Restarted: true,
		History:   &history,
	})
}

func (s *helperServer) handleRestoreHistory(w http.ResponseWriter, r *http.Request) {
	if !s.validState(r) {
		writeError(w, http.StatusForbidden, errors.New("invalid local helper session"))
		return
	}
	var request struct {
		BackupID string `json:"backup_id"`
	}
	if err := decodeJSONBody(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(request.BackupID) == "" {
		writeError(w, http.StatusBadRequest, errors.New("select a history repair backup first"))
		return
	}
	if !s.beginOperation(w) {
		return
	}
	defer s.operationMu.Unlock()
	releaseLifecycle, err := acquireLifecycleLock(filepath.Dir(s.manager.ConfigPath))
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	defer releaseLifecycle()

	if err := s.prepare(); err != nil {
		writeError(w, http.StatusConflict, fmt.Errorf("无法安全退出会改写会话索引的第三方管理工具：%w", err))
		return
	}
	installation := s.codexInstallation()
	if err := s.stop(installation); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("Codex could not be stopped safely; no conversation data was restored: %w", err))
		return
	}
	if err := s.restoreHistory(request.BackupID); err != nil {
		startErr := s.startWithRetry(installation)
		writeError(w, http.StatusInternalServerError, operationFailure("conversation history was not restored", err, startErr))
		return
	}
	if err := s.startWithRetry(installation); err != nil {
		writeJSON(w, http.StatusInternalServerError, operationResponse{
			OK:        false,
			Message:   fmt.Sprintf("历史会话已恢复，但 Codex 未能自动重新启动：%v。请手动启动 Codex。", err),
			Restarted: false,
		})
		return
	}
	writeJSON(w, http.StatusOK, operationResponse{
		OK:        true,
		Message:   "已恢复所选历史会话备份，Codex 已重新启动。",
		BackupID:  request.BackupID,
		Restarted: true,
	})
}

func operationFailure(prefix string, operationErr, startErr error) error {
	if startErr == nil {
		return fmt.Errorf("%s: %w; Codex was restarted with the previous safe state", prefix, operationErr)
	}
	return fmt.Errorf("%s: %v; Codex also could not be restarted: %w", prefix, operationErr, startErr)
}

func (s *helperServer) startWithRetry(installation CodexInstallation) error {
	firstErr := s.start(installation)
	if firstErr == nil {
		return nil
	}
	time.Sleep(300 * time.Millisecond)
	redetected := s.detect()
	if !redetected.Found {
		redetected = installation
	}
	if secondErr := s.start(redetected); secondErr != nil {
		return fmt.Errorf("first launch attempt failed: %v; retry failed: %w", firstErr, secondErr)
	}
	return nil
}

func (s *helperServer) stopBeforeRollback(fallback CodexInstallation) error {
	installation := s.codexInstallation()
	if !installation.Found {
		installation = fallback
	}
	return s.stop(installation)
}

func historyRollbackFailed(err error) bool {
	var repairErr *HistoryRepairApplyError
	return errors.As(err, &repairErr) && repairErr.RollbackErr != nil
}

func configRollbackFailed(err error) bool {
	var mutationErr *ConfigMutationError
	return errors.As(err, &mutationErr) && mutationErr.RollbackErr != nil
}

func historyProviderForConfig(configPath string) string {
	provider, err := readCurrentProvider(configPath)
	if err != nil {
		return ""
	}
	return provider
}

func (s *helperServer) repairHistoryForConfigChange(previousProvider string) (HistoryRepairResult, error) {
	targetProvider := historyProviderForConfig(s.manager.ConfigPath)
	if previousProvider != "" && targetProvider != "" && previousProvider == targetProvider {
		targetModel, _ := readCurrentModel(s.manager.ConfigPath)
		return HistoryRepairResult{
			TargetProvider: targetProvider,
			TargetModel:    targetModel,
			Skipped:        true,
			SkipReason:     "provider_unchanged",
		}, nil
	}
	return s.repairHistory()
}

func historySummary(result HistoryRepairResult) string {
	if result.Skipped {
		return "当前 provider 未变化，已跳过历史迁移；默认模型用于新会话，已有会话和索引保持原样。"
	}
	workspaceSummary := "项目映射校验通过；"
	if result.WorkspaceState != nil && result.WorkspaceState.Updated {
		workspaceSummary = fmt.Sprintf("已修复 %d 个项目的路径映射；", result.WorkspaceState.ProjectCount)
	}
	compatibilitySummary := ""
	if result.SanitizedRecords > 0 {
		compatibilitySummary = fmt.Sprintf("已清理 %d 条无法续接的内部协议记录；", result.SanitizedRecords)
	}
	modelSummary := ""
	if result.TargetModel != "" {
		modelSummary = fmt.Sprintf("已将 %d 个已有普通会话切换到 %s；", result.UpdatedModelRows, result.TargetModel)
		if result.UnsupportedModelDBs > 0 {
			modelSummary += fmt.Sprintf("%d 个旧版会话数据库不含模型字段，已保持原样；", result.UnsupportedModelDBs)
		}
	}
	return fmt.Sprintf(
		"%s%s%s已扫描 %d 个会话文件和 %d 个会话数据库，校验 %d 行会话索引，修复 %d 个文件和 %d 行 provider 索引；可见会话、审查会话和正文未删除。",
		workspaceSummary,
		compatibilitySummary,
		modelSummary,
		result.ScannedSessionFiles,
		result.ScannedDatabases,
		result.ThreadCount,
		result.UpdatedSessionFiles,
		result.UpdatedDatabaseRows,
	)
}

func (s *helperServer) beginOperation(w http.ResponseWriter) bool {
	if s.operationMu.TryLock() {
		return true
	}
	writeError(w, http.StatusConflict, errors.New("another Codex configuration operation is already running"))
	return false
}

func (s *helperServer) handleShutdown(w http.ResponseWriter, r *http.Request) {
	if !s.validState(r) {
		writeError(w, http.StatusForbidden, errors.New("invalid local helper session"))
		return
	}
	if !s.beginOperation(w) {
		return
	}
	defer s.operationMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	go func() {
		time.Sleep(150 * time.Millisecond)
		s.requestShutdown()
	}()
}

// handleBrowserClosed releases the local listener when the helper page closes.
// Authorization navigation is explicitly excluded by the page so that the
// local callback remains available until the website returns configuration data.
func (s *helperServer) handleBrowserClosed(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("state") != s.state {
		writeError(w, http.StatusForbidden, errors.New("invalid local helper session"))
		return
	}
	s.requestShutdown()
	w.WriteHeader(http.StatusNoContent)
}

func (s *helperServer) validState(r *http.Request) bool {
	return r.Header.Get("X-XIASS-Helper-State") == s.state
}

func (s *helperServer) requestShutdown() {
	s.shutdownOnce.Do(func() { close(s.shutdown) })
}

func (s *helperServer) localOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host
		}
		ip := net.ParseIP(strings.Trim(host, "[]"))
		if !strings.EqualFold(host, "localhost") && (ip == nil || !ip.IsLoopback()) {
			http.Error(w, "loopback access only", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *helperServer) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'; connect-src 'self'; img-src 'self' data:; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		next.ServeHTTP(w, r)
	})
}

func decodeJSONBody(r *http.Request, destination any) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, 64*1024))
	if err != nil {
		return err
	}
	if len(body) == 0 {
		body = []byte("{}")
	}
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("invalid request: %w", err)
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]any{"ok": false, "message": err.Error()})
}

func randomState() (string, error) {
	data := make([]byte, 32)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

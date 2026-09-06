package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

const (
	providerID                   = "codex_local_access"
	legacyProviderID             = "xiass"
	providerName                 = "XIASS API"
	defaultModel                 = "gpt-5.6-sol"
	defaultContextWindow         = int64(372000)
	defaultAutoCompactTokenLimit = defaultContextWindow * 9 / 10
	minimumContextWindow         = int64(64000)
	maximumContextWindow         = int64(1050000)
	minimumAutoCompactTokenLimit = int64(16000)
)

var managedTopLevelKeys = map[string]struct{}{
	"model_provider":                 {},
	"model":                          {},
	"review_model":                   {},
	"model_context_window":           {},
	"model_auto_compact_token_limit": {},
	"model_catalog_json":             {},
	"web_search":                     {},
}

type ApplyConfig struct {
	BaseURL                    string   `json:"base_url"`
	APIKey                     string   `json:"api_key"`
	KeyName                    string   `json:"key_name"`
	Model                      string   `json:"model,omitempty"`
	ReviewModel                string   `json:"review_model,omitempty"`
	ModelCatalogModels         []string `json:"model_catalog_models,omitempty"`
	ProviderName               string   `json:"provider_name,omitempty"`
	ModelContextWindow         int64    `json:"model_context_window,omitempty"`
	ModelAutoCompactTokenLimit int64    `json:"model_auto_compact_token_limit,omitempty"`
	ModelCatalogPath           string   `json:"-"`
	// Descriptors come only from helper-side discovery, never callback JSON.
	ModelCatalogDescriptors map[string]json.RawMessage `json:"-"`
	// ForceCanonicalProvider is only set by the trusted local XIASS helper
	// endpoint. It is deliberately not accepted from JSON payloads.
	ForceCanonicalProvider bool `json:"-"`
}

type ContextSettings struct {
	ModelContextWindow         int64 `json:"model_context_window"`
	ModelAutoCompactTokenLimit int64 `json:"model_auto_compact_token_limit"`
}

type BackupManifest struct {
	Version         int       `json:"version"`
	ID              string    `json:"id"`
	Reason          string    `json:"reason"`
	CreatedAt       time.Time `json:"created_at"`
	ConfigPath      string    `json:"config_path"`
	OriginalExisted bool      `json:"original_existed"`
	OriginalMode    uint32    `json:"original_mode,omitempty"`
	OriginalSHA256  string    `json:"original_sha256,omitempty"`
	AppliedSHA256   string    `json:"applied_sha256,omitempty"`
}

type BackupInfo struct {
	ID              string    `json:"id"`
	Reason          string    `json:"reason"`
	CreatedAt       time.Time `json:"created_at"`
	OriginalExisted bool      `json:"original_existed"`
}

type ApplyResult struct {
	BackupID   string `json:"backup_id"`
	ConfigSHA  string `json:"config_sha256"`
	CatalogSHA string `json:"catalog_sha256"`
	ProviderID string `json:"provider_id"`
}

type RestoreResult struct {
	RestoredBackupID string `json:"restored_backup_id"`
	SafetyBackupID   string `json:"safety_backup_id"`
}

type ConfigMutationError struct {
	Cause       error
	RollbackErr error
}

func (e *ConfigMutationError) Error() string {
	if e.RollbackErr != nil {
		return fmt.Sprintf("configuration mutation failed: %v; automatic rollback also failed: %v", e.Cause, e.RollbackErr)
	}
	return fmt.Sprintf("configuration mutation failed and was rolled back safely: %v", e.Cause)
}

func (e *ConfigMutationError) Unwrap() error {
	return e.Cause
}

type ConfigManager struct {
	ConfigPath       string
	ModelCatalogRoot string
	BackupRoot       string
	LockPath         string
}

func NewConfigManager(codexHome string) *ConfigManager {
	return &ConfigManager{
		ConfigPath:       filepath.Join(codexHome, "config.toml"),
		ModelCatalogRoot: filepath.Join(codexHome, "xiass-helper", "model-catalogs"),
		BackupRoot:       filepath.Join(codexHome, "xiass-helper", "backups"),
		LockPath:         filepath.Join(codexHome, "xiass-helper", "operation.lock"),
	}
}

func (m *ConfigManager) Apply(input ApplyConfig) (ApplyResult, error) {
	return m.apply(input, true)
}

// ApplyWithoutBackup is the one-shot helper path. It keeps the original bytes
// in memory for atomic rollback, but never creates a persistent backup record.
func (m *ConfigManager) ApplyWithoutBackup(input ApplyConfig) (ApplyResult, error) {
	return m.apply(input, false)
}

func (m *ConfigManager) apply(input ApplyConfig, persistBackup bool) (ApplyResult, error) {
	var result ApplyResult
	err := m.withLock(func() error {
		normalized, err := normalizeApplyConfig(input)
		if err != nil {
			return err
		}
		nativeModels := readNativeModelDescriptors(filepath.Join(filepath.Dir(m.ConfigPath), "models_cache.json"))
		catalogData, err := buildModelCatalogJSON(normalized.BaseURL, normalized.Model, normalized.ModelCatalogModels, normalized.ModelCatalogDescriptors, nativeModels)
		if err != nil {
			return fmt.Errorf("prepare local model catalog: %w", err)
		}
		if len(catalogData) > 0 {
			var catalog any
			if err := json.Unmarshal(catalogData, &catalog); err != nil {
				return errors.New("generated model catalog is invalid")
			}
			if containsCatalogSensitiveData(catalog, normalized.APIKey) {
				return errors.New("generated model catalog unexpectedly contains sensitive connection data")
			}
		}
		catalogSHA := ""
		normalized.ModelCatalogPath = ""
		if len(catalogData) > 0 {
			catalogSHA = sha256Hex(catalogData)
			normalized.ModelCatalogPath = filepath.Join(m.ModelCatalogRoot, "model-catalog-"+catalogSHA[:16]+".json")
		}

		original, existed, mode, err := readConfigFile(m.ConfigPath)
		if err != nil {
			return err
		}
		originalValid := true
		if existed && len(strings.TrimSpace(string(original))) > 0 {
			if err := validateTOML(original); err != nil {
				if !normalized.ForceCanonicalProvider {
					return fmt.Errorf("existing config.toml is invalid; no changes made: %w", err)
				}
				originalValid = false
			}
		}

		var manifest BackupManifest
		if persistBackup {
			reason := "apply"
			if normalized.ForceCanonicalProvider {
				reason = "force_apply"
				if !originalValid {
					reason = "force_apply_invalid_config"
				}
			}
			manifest, err = m.createBackup(original, existed, mode, reason)
			if err != nil {
				return fmt.Errorf("create backup: %w", err)
			}
		}

		managedProviderID := managedProviderIDForConfig(original)
		var updated []byte
		if normalized.ForceCanonicalProvider {
			managedProviderID = providerID
			if originalValid {
				updated, err = patchCanonicalXIASSConfig(original, normalized)
				if err != nil {
					return fmt.Errorf("prepare canonical XIASS config: %w", err)
				}
			} else {
				updated = patchConfig(nil, normalized, managedProviderID)
			}
		} else {
			updated = patchConfig(original, normalized, managedProviderID)
		}
		if err := verifyManagedConfig(updated, normalized, managedProviderID); err != nil {
			return fmt.Errorf("generated config verification failed: %w", err)
		}
		if len(catalogData) > 0 {
			if err := validateModelCatalog(catalogData); err != nil {
				return fmt.Errorf("generated model catalog verification failed: %w", err)
			}
		}

		if err := os.MkdirAll(filepath.Dir(m.ConfigPath), 0o700); err != nil {
			return fmt.Errorf("create Codex config directory: %w", err)
		}
		if err := ensureConfigUnchanged(m.ConfigPath, original, existed); err != nil {
			return err
		}
		if len(catalogData) > 0 {
			existingCatalog, catalogExisted, _, err := readManagedFile(normalized.ModelCatalogPath)
			if err != nil {
				return err
			}
			if catalogExisted && !bytes.Equal(existingCatalog, catalogData) {
				return errors.New("model catalog checksum collision; no changes were made")
			}
			if !catalogExisted {
				if err := writeFileAtomic(normalized.ModelCatalogPath, catalogData, 0o600); err != nil {
					return fmt.Errorf("write model catalog: %w", err)
				}
			}
		}
		if err := writeFileAtomic(m.ConfigPath, updated, secureMode(mode)); err != nil {
			return fmt.Errorf("write config.toml: %w", err)
		}

		written, err := os.ReadFile(m.ConfigPath)
		if err != nil {
			return rollbackConfigError(
				fmt.Errorf("read back config.toml: %w", err),
				m.ConfigPath, original, existed, mode,
			)
		}
		if err := verifyManagedConfig(written, normalized, managedProviderID); err != nil {
			return rollbackConfigError(
				fmt.Errorf("written config verification failed: %w", err),
				m.ConfigPath, original, existed, mode,
			)
		}
		if len(catalogData) > 0 {
			writtenCatalog, err := os.ReadFile(normalized.ModelCatalogPath)
			if err != nil {
				return rollbackConfigError(
					fmt.Errorf("read back model catalog: %w", err),
					m.ConfigPath, original, existed, mode,
				)
			}
			if !bytes.Equal(writtenCatalog, catalogData) {
				return rollbackConfigError(
					errors.New("model catalog read-back verification failed"),
					m.ConfigPath, original, existed, mode,
				)
			}
		}

		configSHA := sha256Hex(written)
		if persistBackup {
			manifest.AppliedSHA256 = configSHA
			if err := m.writeManifest(manifest); err != nil {
				return rollbackConfigError(
					fmt.Errorf("record verified backup metadata: %w", err),
					m.ConfigPath, original, existed, mode,
				)
			}
		}

		result = ApplyResult{ConfigSHA: configSHA, CatalogSHA: catalogSHA, ProviderID: managedProviderID}
		if persistBackup {
			result.BackupID = manifest.ID
		}
		return nil
	})
	return result, err
}

func (m *ConfigManager) UpgradeLegacyProvider() (ApplyResult, bool, error) {
	data, existed, _, err := readConfigFile(m.ConfigPath)
	if err != nil {
		return ApplyResult{}, false, err
	}
	if !existed {
		return ApplyResult{}, false, nil
	}
	var root map[string]any
	if err := toml.Unmarshal(data, &root); err != nil {
		return ApplyResult{}, false, fmt.Errorf("read restored legacy config: %w", err)
	}
	current, _ := root["model_provider"].(string)
	if strings.TrimSpace(current) != legacyProviderID {
		return ApplyResult{}, false, nil
	}
	providers, _ := root["model_providers"].(map[string]any)
	legacy, _ := providers[legacyProviderID].(map[string]any)
	baseURL, _ := legacy["base_url"].(string)
	apiKey, _ := legacy["experimental_bearer_token"].(string)
	if strings.TrimSpace(baseURL) == "" || strings.TrimSpace(apiKey) == "" {
		return ApplyResult{}, false, errors.New("restored legacy XIASS provider is incomplete and cannot be upgraded safely")
	}
	contextSettings, err := readContextSettingsFromTOML(data)
	if err != nil {
		return ApplyResult{}, false, fmt.Errorf("read restored legacy context settings: %w", err)
	}
	result, err := m.Apply(ApplyConfig{
		BaseURL:                    baseURL,
		APIKey:                     apiKey,
		ModelContextWindow:         contextSettings.ModelContextWindow,
		ModelAutoCompactTokenLimit: contextSettings.ModelAutoCompactTokenLimit,
	})
	return result, true, err
}

func (m *ConfigManager) Restore(backupID string) (RestoreResult, error) {
	var result RestoreResult
	err := m.withLock(func() error {
		backups, err := m.ListBackups()
		if err != nil {
			return err
		}
		if len(backups) == 0 {
			return errors.New("no XIASS Codex configuration backup was found")
		}
		if backupID == "" {
			backupID = backups[0].ID
		}
		if filepath.Base(backupID) != backupID {
			return errors.New("invalid backup ID")
		}

		target, err := m.readManifest(backupID)
		if err != nil {
			return err
		}
		if filepath.Clean(target.ConfigPath) != filepath.Clean(m.ConfigPath) {
			return errors.New("backup belongs to a different Codex config path")
		}
		current, currentExisted, currentMode, err := readConfigFile(m.ConfigPath)
		if err != nil {
			return err
		}
		safety, err := m.createBackup(current, currentExisted, currentMode, "pre_restore")
		if err != nil {
			return fmt.Errorf("create pre-restore safety backup: %w", err)
		}

		var backupBytes []byte
		if target.OriginalExisted {
			backupBytes, err = os.ReadFile(m.originalPath(target.ID))
			if err != nil {
				return fmt.Errorf("read backup: %w", err)
			}
			if sha256Hex(backupBytes) != target.OriginalSHA256 {
				return errors.New("backup checksum mismatch; restore cancelled")
			}
			if len(strings.TrimSpace(string(backupBytes))) > 0 {
				if err := validateTOML(backupBytes); err != nil {
					return fmt.Errorf("backup TOML is invalid; restore cancelled: %w", err)
				}
			}
		}
		if err := ensureConfigUnchanged(m.ConfigPath, current, currentExisted); err != nil {
			return err
		}

		if target.OriginalExisted {
			targetMode := fs.FileMode(target.OriginalMode)
			if err := writeFileAtomic(m.ConfigPath, backupBytes, secureMode(targetMode)); err != nil {
				return rollbackConfigError(
					fmt.Errorf("restore config.toml: %w", err),
					m.ConfigPath, current, currentExisted, currentMode,
				)
			}
		} else if err := os.Remove(m.ConfigPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return rollbackConfigError(
				fmt.Errorf("remove helper-created config.toml: %w", err),
				m.ConfigPath, current, currentExisted, currentMode,
			)
		}

		if target.OriginalExisted {
			restored, err := os.ReadFile(m.ConfigPath)
			if err != nil || sha256Hex(restored) != target.OriginalSHA256 {
				cause := errors.New("restore read-back verification failed")
				if err != nil {
					cause = fmt.Errorf("restore read-back verification failed: %w", err)
				}
				return rollbackConfigError(cause, m.ConfigPath, current, currentExisted, currentMode)
			}
		} else if _, err := os.Lstat(m.ConfigPath); !errors.Is(err, fs.ErrNotExist) {
			return rollbackConfigError(
				errors.New("restore verification failed; config.toml still exists"),
				m.ConfigPath, current, currentExisted, currentMode,
			)
		}

		result = RestoreResult{RestoredBackupID: target.ID, SafetyBackupID: safety.ID}
		return nil
	})
	return result, err
}

func (m *ConfigManager) ListBackups() ([]BackupInfo, error) {
	entries, err := os.ReadDir(m.BackupRoot)
	if errors.Is(err, fs.ErrNotExist) {
		return []BackupInfo{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list backups: %w", err)
	}

	backups := make([]BackupInfo, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		manifest, err := m.readManifest(entry.Name())
		if err != nil {
			continue
		}
		backups = append(backups, BackupInfo{
			ID:              manifest.ID,
			Reason:          manifest.Reason,
			CreatedAt:       manifest.CreatedAt,
			OriginalExisted: manifest.OriginalExisted,
		})
	}
	sort.Slice(backups, func(i, j int) bool { return backups[i].CreatedAt.After(backups[j].CreatedAt) })
	return backups, nil
}

func (m *ConfigManager) DeleteBackup(backupID string) error {
	return m.withLock(func() error {
		if filepath.Base(backupID) != backupID || backupID == "." {
			return errors.New("invalid backup ID")
		}
		manifest, err := m.readManifest(backupID)
		if err != nil {
			return err
		}
		if filepath.Clean(manifest.ConfigPath) != filepath.Clean(m.ConfigPath) {
			return errors.New("backup belongs to a different Codex config path")
		}
		return removeManagedBackupDirectory(m.BackupRoot, backupID)
	})
}

func (m *ConfigManager) createBackup(data []byte, existed bool, mode fs.FileMode, reason string) (BackupManifest, error) {
	id, err := newBackupID()
	if err != nil {
		return BackupManifest{}, err
	}
	manifest := BackupManifest{
		Version:         1,
		ID:              id,
		Reason:          reason,
		CreatedAt:       time.Now().UTC(),
		ConfigPath:      m.ConfigPath,
		OriginalExisted: existed,
		OriginalMode:    uint32(mode.Perm()),
	}
	if existed {
		manifest.OriginalSHA256 = sha256Hex(data)
	}

	dir := filepath.Join(m.BackupRoot, id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return BackupManifest{}, err
	}
	if existed {
		if err := os.WriteFile(m.originalPath(id), data, 0o600); err != nil {
			return BackupManifest{}, err
		}
	}
	if err := m.writeManifest(manifest); err != nil {
		return BackupManifest{}, err
	}
	return manifest, nil
}

func (m *ConfigManager) writeManifest(manifest BackupManifest) error {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeFileAtomic(filepath.Join(m.BackupRoot, manifest.ID, "manifest.json"), data, 0o600)
}

func (m *ConfigManager) readManifest(id string) (BackupManifest, error) {
	var manifest BackupManifest
	data, err := os.ReadFile(filepath.Join(m.BackupRoot, id, "manifest.json"))
	if err != nil {
		return manifest, fmt.Errorf("read backup manifest: %w", err)
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return manifest, fmt.Errorf("decode backup manifest: %w", err)
	}
	if manifest.Version != 1 || manifest.ID != id {
		return manifest, errors.New("unsupported or mismatched backup manifest")
	}
	return manifest, nil
}

func (m *ConfigManager) originalPath(id string) string {
	return filepath.Join(m.BackupRoot, id, "config.toml")
}

func removeManagedBackupDirectory(root, backupID string) error {
	if strings.TrimSpace(backupID) == "" || filepath.Base(backupID) != backupID || backupID == "." {
		return errors.New("invalid backup ID")
	}
	root = filepath.Clean(root)
	directory := filepath.Join(root, backupID)
	relative, err := filepath.Rel(root, directory)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return errors.New("invalid backup path")
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("backup is not a managed directory")
	}
	return os.RemoveAll(directory)
}

func (m *ConfigManager) withLock(fn func() error) error {
	release, err := acquireProcessLock(m.LockPath, "another XIASS Codex configuration operation is already running")
	if err != nil {
		return err
	}
	defer release()
	return fn()
}

func normalizeApplyConfig(input ApplyConfig) (ApplyConfig, error) {
	input.APIKey = strings.TrimSpace(input.APIKey)
	input.BaseURL = strings.TrimSpace(input.BaseURL)
	input.KeyName = strings.TrimSpace(input.KeyName)
	input.Model = strings.TrimSpace(input.Model)
	// review_model is intentionally ignored. Leaving it unset preserves Codex's
	// official default review behavior, including future client changes.
	input.ReviewModel = ""
	input.ProviderName = strings.TrimSpace(input.ProviderName)
	if input.APIKey == "" || len(input.APIKey) > 8192 || strings.ContainsAny(input.APIKey, "\r\n\x00") {
		return input, errors.New("invalid API key")
	}
	if input.Model == "" {
		input.Model = defaultModel
	}
	if input.ProviderName == "" {
		input.ProviderName = providerName
	}
	if len(input.Model) > 200 || strings.ContainsAny(input.Model, "\r\n\x00") {
		return input, errors.New("invalid model name")
	}
	var err error
	input.ModelCatalogModels, err = normalizeModelIDs(input.ModelCatalogModels)
	if err != nil {
		return input, err
	}
	if len(input.ProviderName) > 200 || strings.ContainsAny(input.ProviderName, "\r\n\x00") {
		return input, errors.New("invalid provider name")
	}
	contextSettings, err := normalizeContextSettings(ContextSettings{
		ModelContextWindow:         input.ModelContextWindow,
		ModelAutoCompactTokenLimit: input.ModelAutoCompactTokenLimit,
	})
	if err != nil {
		return input, err
	}
	input.ModelContextWindow = contextSettings.ModelContextWindow
	input.ModelAutoCompactTokenLimit = contextSettings.ModelAutoCompactTokenLimit

	if !strings.Contains(input.BaseURL, "://") {
		loopbackCandidate, err := url.Parse("http://" + input.BaseURL)
		if err == nil && isLoopbackHostname(loopbackCandidate.Hostname()) {
			input.BaseURL = loopbackCandidate.String()
		}
	}
	parsed, err := url.Parse(input.BaseURL)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil {
		return input, errors.New("base URL must be a valid HTTPS URL or a loopback HTTP URL")
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	if parsed.Scheme != "https" && (parsed.Scheme != "http" || !isLoopbackHostname(parsed.Hostname())) {
		return input, errors.New("base URL must use HTTPS; HTTP is allowed only for localhost, 127.0.0.0/8, or ::1")
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	if !strings.EqualFold(parsed.Path[strings.LastIndex(parsed.Path, "/")+1:], "v1") {
		parsed.Path += "/v1"
	}
	input.BaseURL = strings.TrimRight(parsed.String(), "/")
	return input, nil
}

func defaultContextSettings() ContextSettings {
	return ContextSettings{
		ModelContextWindow:         defaultContextWindow,
		ModelAutoCompactTokenLimit: defaultAutoCompactTokenLimit,
	}
}

func normalizeContextSettings(input ContextSettings) (ContextSettings, error) {
	contextWasProvided := input.ModelContextWindow != 0
	if input.ModelContextWindow == 0 {
		input.ModelContextWindow = defaultContextWindow
	}
	if input.ModelAutoCompactTokenLimit == 0 {
		if contextWasProvided {
			input.ModelAutoCompactTokenLimit = input.ModelContextWindow * 9 / 10
		} else {
			input.ModelAutoCompactTokenLimit = defaultAutoCompactTokenLimit
		}
	}
	if input.ModelContextWindow < minimumContextWindow || input.ModelContextWindow > maximumContextWindow {
		return input, fmt.Errorf("model context window must be between %d and %d tokens", minimumContextWindow, maximumContextWindow)
	}
	if input.ModelAutoCompactTokenLimit < minimumAutoCompactTokenLimit || input.ModelAutoCompactTokenLimit > input.ModelContextWindow {
		return input, fmt.Errorf("automatic compact token limit must be between %d and the context window (%d) tokens", minimumAutoCompactTokenLimit, input.ModelContextWindow)
	}
	return input, nil
}

func (m *ConfigManager) ReadContextSettings() (ContextSettings, error) {
	data, existed, _, err := readConfigFile(m.ConfigPath)
	if err != nil {
		return defaultContextSettings(), err
	}
	if !existed || len(strings.TrimSpace(string(data))) == 0 {
		return defaultContextSettings(), nil
	}
	var root map[string]any
	if err := toml.Unmarshal(data, &root); err != nil {
		return defaultContextSettings(), fmt.Errorf("read config.toml: %w", err)
	}
	return contextSettingsFromTOMLRoot(root)
}

func (m *ConfigManager) UsesXIASSProvider() bool {
	data, existed, _, err := readConfigFile(m.ConfigPath)
	if err != nil || !existed || len(strings.TrimSpace(string(data))) == 0 {
		return false
	}
	var root map[string]any
	if err := toml.Unmarshal(data, &root); err != nil {
		return false
	}
	provider, _ := root["model_provider"].(string)
	provider = strings.TrimSpace(provider)
	return provider == providerID || provider == legacyProviderID
}

func readContextSettingsFromTOML(data []byte) (ContextSettings, error) {
	var root map[string]any
	if err := toml.Unmarshal(data, &root); err != nil {
		return defaultContextSettings(), err
	}
	return contextSettingsFromTOMLRoot(root)
}

func contextSettingsFromTOMLRoot(root map[string]any) (ContextSettings, error) {
	settings := defaultContextSettings()
	if value, present := root["model_context_window"]; present {
		parsed, ok := integerValue(value)
		if !ok {
			return defaultContextSettings(), errors.New("model_context_window is not a positive integer")
		}
		settings.ModelContextWindow = parsed
	}
	if value, present := root["model_auto_compact_token_limit"]; present {
		parsed, ok := integerValue(value)
		if !ok {
			return defaultContextSettings(), errors.New("model_auto_compact_token_limit is not a positive integer")
		}
		settings.ModelAutoCompactTokenLimit = parsed
	}
	return normalizeContextSettings(settings)
}

func isLoopbackHostname(hostname string) bool {
	if strings.EqualFold(strings.TrimSuffix(hostname, "."), "localhost") {
		return true
	}
	ip := net.ParseIP(hostname)
	return ip != nil && ip.IsLoopback()
}

func managedProviderIDForConfig(original []byte) string {
	if len(strings.TrimSpace(string(original))) == 0 {
		return providerID
	}
	var root map[string]any
	if err := toml.Unmarshal(original, &root); err != nil {
		return providerID
	}
	current, _ := root["model_provider"].(string)
	current = strings.TrimSpace(current)
	if current == legacyProviderID || current == providerID {
		return providerID
	}
	if !validHistoryProviderID(current) {
		return providerID
	}
	providers, _ := root["model_providers"].(map[string]any)
	if _, ok := providers[current].(map[string]any); ok {
		return current
	}
	return providerID
}

func patchConfig(original []byte, input ApplyConfig, managedProviderID string) []byte {
	text := strings.ReplaceAll(string(original), "\r\n", "\n")
	lines := strings.Split(text, "\n")
	body := make([]string, 0, len(lines))
	inTop := true
	skipProvider := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if section, ok := tomlSectionName(trimmed); ok {
			if isManagedProviderSection(section, managedProviderID) {
				skipProvider = true
				inTop = false
				continue
			}
			skipProvider = false
			inTop = false
		}
		if skipProvider {
			continue
		}
		if inTop {
			if key, ok := tomlAssignmentKey(trimmed); ok {
				if _, managed := managedTopLevelKeys[key]; managed {
					continue
				}
			}
		}
		body = append(body, line)
	}

	bodyText := strings.Trim(strings.Join(body, "\n"), "\n")
	top := []string{
		`model_provider = "` + managedProviderID + `"`,
		`model = "` + escapeTOML(input.Model) + `"`,
		"model_context_window = " + strconv.FormatInt(input.ModelContextWindow, 10),
		"model_auto_compact_token_limit = " + strconv.FormatInt(input.ModelAutoCompactTokenLimit, 10),
		`web_search = "live"`,
	}
	if input.ModelCatalogPath != "" {
		top = append(top, `model_catalog_json = "`+escapeTOML(input.ModelCatalogPath)+`"`)
	}
	provider := []string{
		"[model_providers." + managedProviderID + "]",
		`name = "` + escapeTOML(input.ProviderName) + `"`,
		`base_url = "` + escapeTOML(input.BaseURL) + `"`,
		`wire_api = "responses"`,
		"requires_openai_auth = false",
		`experimental_bearer_token = "` + escapeTOML(input.APIKey) + `"`,
		`http_headers = { "x-openai-actor-authorization" = "` + escapeTOML(actorAuthorization(input.BaseURL)) + `" }`,
		"supports_websockets = false",
	}

	parts := []string{strings.Join(top, "\n")}
	if strings.TrimSpace(bodyText) != "" {
		parts = append(parts, bodyText)
	}
	parts = append(parts, strings.Join(provider, "\n"))
	return []byte(strings.Join(parts, "\n\n") + "\n")
}

func patchCanonicalXIASSConfig(original []byte, input ApplyConfig) ([]byte, error) {
	if len(strings.TrimSpace(string(original))) == 0 {
		return patchConfig(nil, input, providerID), nil
	}

	var root map[string]any
	if err := toml.Unmarshal(original, &root); err != nil {
		return nil, err
	}
	for key := range managedTopLevelKeys {
		delete(root, key)
	}
	if providers, ok := root["model_providers"].(map[string]any); ok {
		delete(providers, providerID)
		delete(providers, legacyProviderID)
		if len(providers) == 0 {
			delete(root, "model_providers")
		}
	} else {
		// A scalar or array under this key cannot coexist with a TOML provider
		// table. Drop it so the canonical provider section can be written.
		delete(root, "model_providers")
	}

	preserved, err := toml.Marshal(root)
	if err != nil {
		return nil, err
	}
	return patchConfig(preserved, input, providerID), nil
}

func isManagedProviderSection(section, managedProviderID string) bool {
	for _, id := range []string{managedProviderID, providerID, legacyProviderID} {
		if section == "model_providers."+id || strings.HasPrefix(section, "model_providers."+id+".") {
			return true
		}
	}
	return false
}

func verifyManagedConfig(data []byte, expected ApplyConfig, managedProviderID string) error {
	var err error
	expected, err = normalizeApplyConfig(expected)
	if err != nil {
		return err
	}
	var root map[string]any
	if err := toml.Unmarshal(data, &root); err != nil {
		return err
	}
	checks := map[string]string{
		"model_provider": managedProviderID,
		"model":          expected.Model,
		"web_search":     "live",
	}
	for key, want := range checks {
		if got, _ := root[key].(string); got != want {
			return fmt.Errorf("%s mismatch", key)
		}
	}
	if _, exists := root["review_model"]; exists {
		return errors.New("review_model must be omitted so Codex uses its official default")
	}
	if expected.ModelCatalogPath != "" {
		if got, _ := root["model_catalog_json"].(string); filepath.Clean(got) != filepath.Clean(expected.ModelCatalogPath) {
			return errors.New("model_catalog_json mismatch")
		}
	} else if _, exists := root["model_catalog_json"]; exists {
		return errors.New("model_catalog_json must be omitted when native metadata is unavailable")
	}
	if !numberEquals(root["model_context_window"], expected.ModelContextWindow) || !numberEquals(root["model_auto_compact_token_limit"], expected.ModelAutoCompactTokenLimit) {
		return errors.New("context window settings mismatch")
	}

	providers, ok := root["model_providers"].(map[string]any)
	if !ok {
		return errors.New("model_providers table missing")
	}
	provider, ok := providers[managedProviderID].(map[string]any)
	if !ok {
		return errors.New("XIASS provider table missing")
	}
	// The provider name is only a Codex display label. Customers may already
	// have a localized or legacy label for the same provider, so it must not
	// block an otherwise verified connection update.
	if name, ok := provider["name"].(string); !ok || strings.TrimSpace(name) == "" {
		return errors.New("provider display name missing")
	}

	providerChecks := map[string]string{
		"base_url":                  expected.BaseURL,
		"wire_api":                  "responses",
		"experimental_bearer_token": expected.APIKey,
	}
	for key, want := range providerChecks {
		if got, _ := provider[key].(string); got != want {
			return fmt.Errorf("provider %s mismatch", key)
		}
	}
	if got, ok := provider["requires_openai_auth"].(bool); !ok || got {
		return errors.New("requires_openai_auth must be false")
	}
	if got, ok := provider["supports_websockets"].(bool); !ok || got {
		return errors.New("supports_websockets must be false")
	}
	headers, ok := provider["http_headers"].(map[string]any)
	if !ok || headers["x-openai-actor-authorization"] != actorAuthorization(expected.BaseURL) {
		return errors.New("actor authorization header mismatch")
	}
	return nil
}

func actorAuthorization(baseURL string) string {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}

func validateTOML(data []byte) error {
	var root map[string]any
	return toml.Unmarshal(data, &root)
}

func tomlSectionName(line string) (string, bool) {
	if len(line) < 3 || line[0] != '[' || line[len(line)-1] != ']' {
		return "", false
	}
	return strings.TrimSpace(strings.Trim(line, "[]")), true
}

func tomlAssignmentKey(line string) (string, bool) {
	if line == "" || strings.HasPrefix(line, "#") {
		return "", false
	}
	idx := strings.IndexByte(line, '=')
	if idx <= 0 {
		return "", false
	}
	return strings.TrimSpace(line[:idx]), true
}

func escapeTOML(value string) string {
	replacer := strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "\r", "\\r", "\n", "\\n")
	return replacer.Replace(value)
}

func numberEquals(value any, want int64) bool {
	parsed, ok := integerValue(value)
	return ok && parsed == want
}

func integerValue(value any) (int64, bool) {
	switch typed := value.(type) {
	case int64:
		return typed, true
	case int:
		return int64(typed), true
	case float64:
		if typed != float64(int64(typed)) {
			return 0, false
		}
		return int64(typed), true
	case string:
		parsed, err := strconv.ParseInt(typed, 10, 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func readConfigFile(path string) ([]byte, bool, fs.FileMode, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, 0o600, nil
	}
	if err != nil {
		return nil, false, 0, fmt.Errorf("inspect config.toml: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, false, 0, errors.New("config.toml is a symbolic link; no changes were made")
	}
	if !info.Mode().IsRegular() {
		return nil, false, 0, errors.New("config.toml is not a regular file; no changes were made")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false, 0, fmt.Errorf("read config.toml: %w", err)
	}
	return data, true, info.Mode().Perm(), nil
}

func readManagedFile(path string) ([]byte, bool, fs.FileMode, error) {
	if strings.TrimSpace(path) == "" {
		return nil, false, 0o600, errors.New("managed file path is empty")
	}
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, 0o600, nil
	}
	if err != nil {
		return nil, false, 0, fmt.Errorf("inspect managed file: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, false, 0, errors.New("managed file is a symbolic link; no changes were made")
	}
	if !info.Mode().IsRegular() {
		return nil, false, 0, errors.New("managed file is not a regular file; no changes were made")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false, 0, fmt.Errorf("read managed file: %w", err)
	}
	return data, true, info.Mode().Perm(), nil
}

func restoreOriginal(path string, data []byte, existed bool, mode fs.FileMode) error {
	if !existed {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	return writeFileAtomic(path, data, secureMode(mode))
}

func restoreOriginalVerified(path string, data []byte, existed bool, mode fs.FileMode) error {
	if err := restoreOriginal(path, data, existed, mode); err != nil {
		return err
	}
	if !existed {
		if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
			return nil
		} else if err != nil {
			return fmt.Errorf("verify removed config.toml: %w", err)
		}
		return errors.New("verify removed config.toml: file still exists")
	}
	restored, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("verify restored config.toml: %w", err)
	}
	if !bytes.Equal(restored, data) {
		return errors.New("verify restored config.toml: content mismatch")
	}
	return nil
}

func rollbackConfigError(cause error, path string, data []byte, existed bool, mode fs.FileMode) error {
	if rollbackErr := restoreOriginalVerified(path, data, existed, mode); rollbackErr != nil {
		return &ConfigMutationError{Cause: cause, RollbackErr: rollbackErr}
	}
	return &ConfigMutationError{Cause: cause}
}

func ensureConfigUnchanged(path string, expected []byte, expectedExisted bool) error {
	current, existed, _, err := readConfigFile(path)
	if err != nil {
		return err
	}
	if existed != expectedExisted || !bytes.Equal(current, expected) {
		return errors.New("config.toml changed during this operation; no changes were made")
	}
	return nil
}

func secureMode(mode fs.FileMode) fs.FileMode {
	if mode == 0 {
		return 0o600
	}
	return mode.Perm() & 0o700
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func newBackupID() (string, error) {
	random := make([]byte, 4)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return time.Now().UTC().Format("20060102T150405.000000000Z") + "-" + hex.EncodeToString(random), nil
}

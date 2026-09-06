package service

import (
	"encoding/json"
	"errors"
	"os"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/go-viper/mapstructure/v2"
)

// This private, administrator-only payload records effective values, including
// generated signing keys and environment overrides absent from installation .env.
type runtimeExportContext struct {
	Version     int               `json:"version"`
	Config      map[string]any    `json:"config"`
	Environment map[string]string `json:"environment"`
}

func makeRuntimeExportContext(cfg *config.Config, environment []string) (*runtimeExportContext, error) {
	if cfg == nil {
		return nil, errors.New("effective application configuration is unavailable")
	}
	result := &runtimeExportContext{Version: 2, Environment: make(map[string]string)}
	if err := mapstructure.Decode(cfg, &result.Config); err != nil {
		return nil, errors.New("cannot encode effective application configuration")
	}
	for _, entry := range environment {
		if name, value, ok := strings.Cut(entry, "="); ok {
			result.Environment[name] = value
		}
	}
	return result, nil
}

func (s *RuntimeExportService) writeRuntimeContext() (path string, err error) {
	snapshot, err := makeRuntimeExportContext(s.cfg, os.Environ())
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(snapshot)
	if err != nil || len(payload) > 4<<20 {
		return "", errors.New("effective runtime configuration exceeds export bounds")
	}
	file, err := os.CreateTemp(s.directory, ".runtime-context-*.json")
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
	if _, err = file.Write(payload); err == nil {
		err = file.Sync()
	}
	return path, err
}

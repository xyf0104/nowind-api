package setup

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bmatcuk/doublestar"
	"github.com/moby/patternmatcher"
	"github.com/moby/patternmatcher/ignorefile"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestReleaseArchivesKeepRuntimeAndExcludeLocalState(t *testing.T) {
	root, err := filepath.Abs("../../..")
	require.NoError(t, err)
	var baseline []string
	for _, name := range []string{".goreleaser.yaml", ".goreleaser.simple.yaml"} {
		t.Run(name, func(t *testing.T) {
			content, err := os.ReadFile(filepath.Join(root, name))
			require.NoError(t, err)
			var cfg struct {
				Archives []struct {
					Files []string `yaml:"files"`
				} `yaml:"archives"`
			}
			require.NoError(t, yaml.Unmarshal(content, &cfg))
			require.Len(t, cfg.Archives, 1)
			patterns := cfg.Archives[0].Files
			if baseline == nil {
				baseline = patterns
			} else {
				require.Equal(t, baseline, patterns, "all architectures must ship the same runtime files")
			}
			matches := func(path string) bool {
				for _, pattern := range patterns {
					matched, err := doublestar.Match(pattern, path)
					require.NoError(t, err)
					if matched {
						return true
					}
				}
				return false
			}
			for _, path := range []string{
				"deploy/.env.example", "deploy/config.example.yaml", "deploy/Caddyfile",
				"deploy/docker-compose.yml", "deploy/docker-compose.local.yml",
				"deploy/docker-compose.standalone.yml", "deploy/docker-compose.xiass.yml",
				"deploy/xiass-install.sh", "deploy/xiass-update.sh", "deploy/nowind-update.sh",
				"deploy/xiass-backup.sh", "deploy/xiass-restore.sh", "deploy/xiass-runtime-start.sh",
				"deploy/xiass-runtime-export.sh", "deploy/xiass-runtime-restore.sh",
				"deploy/xiass-cluster-runtime.sh", "deploy/xiass-cluster-join.sh",
				"deploy/xiass-frps-migrate.sh", "deploy/frps-soft-router-install.sh",
				"deploy/team-child-automation/package.json", "deploy/team-child-automation/server.mjs",
				"deploy/team-child-automation/Dockerfile", "deploy/xiass-updater/Dockerfile",
				"deploy/xiass-updater/xiass-updater-entrypoint.sh",
				"deploy/openwrt/xiass-soft-router-agent/install.sh",
				"deploy/openwrt/xiass-soft-router-agent/files/usr/bin/xiass-soft-router-agent",
				"deploy/openwrt/xiass-soft-router-agent/files/etc/init.d/xiass-soft-router-agent",
			} {
				require.FileExists(t, filepath.Join(root, path))
				require.True(t, matches(path), "runtime file missing: %s", path)
			}
			for _, path := range []string{
				"deploy/.env", "deploy/config.yaml", "deploy/docker-compose.override.yml",
				"deploy/data/dump.sql", "deploy/postgres_data/base/1", "deploy/redis_data/dump.rdb",
				"deploy/ha/secrets/pgpass", "deploy/backups/runtime.tar.gz",
				"deploy/xiass-update.sh.bak", "deploy/tests/test.sh", "deploy/test-caddyfile-cache.sh",
				"deploy/team-child-automation/server.test.mjs",
				"deploy/team-child-automation/node_modules/playwright/package.json",
				"deploy/openwrt/xiass-soft-router-agent/tests/fake_uci.py",
				"artifacts/report.json", "operations/rehearsal.sh", "backend/server", "frontend/node_modules/a.js",
			} {
				require.False(t, matches(path), "non-runtime file would enter archive: %s", path)
			}
			files := make(map[string]bool)
			var size int64
			for _, pattern := range patterns {
				paths, err := doublestar.Glob(filepath.Join(root, pattern))
				require.NoError(t, err)
				require.NotEmpty(t, paths, "stale runtime pattern: %s", pattern)
				for _, path := range paths {
					info, err := os.Stat(path)
					require.NoError(t, err)
					if info.Mode().IsRegular() && !files[path] {
						files[path] = true
						size += info.Size()
					}
				}
			}
			t.Logf("runtime support payload: %d files, %d bytes (binary excluded)", len(files), size)
		})
	}
}

func TestDockerBuildContextKeepsSourcesNotGeneratedState(t *testing.T) {
	file, err := os.Open("../../../.dockerignore")
	require.NoError(t, err)
	defer file.Close()
	patterns, err := ignorefile.ReadAll(file)
	require.NoError(t, err)
	matcher, err := patternmatcher.New(patterns)
	require.NoError(t, err)
	for _, path := range []string{
		"backend/cmd/server/main.go", "backend/cmd/server/VERSION", "backend/go.mod", "backend/go.sum",
		"backend/scripts/resolve-version.sh", "backend/migrations/240_refresh_token_authority_transition.sql",
		"backend/resources/model-pricing/model_prices_and_context_window.json",
		"frontend/src/main.ts", "frontend/package.json", "frontend/pnpm-lock.yaml",
		"frontend/.npmrc", "frontend/pnpm-workspace.yaml", "docs/legal/privacy.md",
		"deploy/docker-entrypoint.sh",
	} {
		ignored, err := matcher.MatchesOrParentMatches(path)
		require.NoError(t, err)
		require.False(t, ignored, "build input excluded: %s", path)
	}
	for _, path := range []string{
		"artifacts/verification", "operations/rehearsal.sh", "historical/old/server",
		"backups/runtime.tar.gz", "infrastructure/ha/config.yml", "backend/.gocache/00/test",
		"backend/bin/server", "backend/internal/web/dist/assets/stale.js",
		"backend/internal/service/example_test.go", "frontend/src/__tests__/example.spec.ts",
		"frontend/node_modules/a.js", "frontend/coverage/index.html", "frontend/tsconfig.tsbuildinfo",
		"deploy/.env", "deploy/ha/secrets/pgpass", "deploy/backups/data.tar.gz",
		"deploy/postgres_data/base/1", "deploy/redis_data/dump.rdb", "backend/data/config.yaml",
	} {
		ignored, err := matcher.MatchesOrParentMatches(path)
		require.NoError(t, err)
		require.True(t, ignored, "non-build file included: %s", path)
	}
}

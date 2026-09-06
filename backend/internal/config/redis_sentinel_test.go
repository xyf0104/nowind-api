package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestLoadRedisSentinelDefaultsStandalone(t *testing.T) {
	resetViperWithJWTSecret(t)
	cfg, err := Load()
	require.NoError(t, err)
	require.Empty(t, cfg.Redis.SentinelAddrs)
	require.Empty(t, cfg.Redis.SentinelMasterName)
	require.Empty(t, cfg.Redis.SentinelUsername)
	require.Empty(t, cfg.Redis.SentinelPassword)
	require.Equal(t, "localhost:6379", cfg.Redis.Address())
	require.False(t, cfg.Redis.EnableTLS)
	require.Equal(t, 5, cfg.Redis.DialTimeoutSeconds)
	require.Equal(t, 3, cfg.Redis.ReadTimeoutSeconds)
	require.Equal(t, 3, cfg.Redis.WriteTimeoutSeconds)
	require.Equal(t, 1024, cfg.Redis.PoolSize)
	require.Equal(t, 128, cfg.Redis.MinIdleConns)
}

func TestLoadRedisSentinelFromEnvironment(t *testing.T) {
	resetViperWithJWTSecret(t)
	t.Setenv("REDIS_SENTINEL_ADDRS", " sentinel-1.example.net:26379 , [::1]:26380 ")
	t.Setenv("REDIS_SENTINEL_MASTER_NAME", " cache-primary ")
	t.Setenv("REDIS_SENTINEL_USERNAME", "discovery-user")
	t.Setenv("REDIS_SENTINEL_PASSWORD", " sentinel-secret ")
	t.Setenv("REDIS_USERNAME", "data-user")
	t.Setenv("REDIS_PASSWORD", " data-secret ")
	t.Setenv("REDIS_DB", "2")
	t.Setenv("REDIS_ENABLE_TLS", "true")
	t.Setenv("REDIS_DIAL_TIMEOUT_SECONDS", "7")
	t.Setenv("REDIS_READ_TIMEOUT_SECONDS", "8")
	t.Setenv("REDIS_WRITE_TIMEOUT_SECONDS", "9")
	t.Setenv("REDIS_POOL_SIZE", "31")
	t.Setenv("REDIS_MIN_IDLE_CONNS", "3")

	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, []string{"sentinel-1.example.net:26379", "[::1]:26380"}, cfg.Redis.SentinelAddrs)
	require.Equal(t, "cache-primary", cfg.Redis.SentinelMasterName)
	require.Equal(t, "discovery-user", cfg.Redis.SentinelUsername)
	require.Equal(t, " sentinel-secret ", cfg.Redis.SentinelPassword)
	require.Equal(t, "data-user", cfg.Redis.Username)
	require.Equal(t, " data-secret ", cfg.Redis.Password)
	require.Equal(t, 2, cfg.Redis.DB)
	require.True(t, cfg.Redis.EnableTLS)
	require.Equal(t, 7, cfg.Redis.DialTimeoutSeconds)
	require.Equal(t, 8, cfg.Redis.ReadTimeoutSeconds)
	require.Equal(t, 9, cfg.Redis.WriteTimeoutSeconds)
	require.Equal(t, 31, cfg.Redis.PoolSize)
	require.Equal(t, 3, cfg.Redis.MinIdleConns)
}

func TestLoadRedisSentinelFromYAML(t *testing.T) {
	for _, disable := range []bool{false, true} {
		t.Run(map[bool]string{false: "enabled", true: "explicit empty env disables"}[disable], func(t *testing.T) {
			resetViperWithJWTSecret(t)
			path := filepath.Join(t.TempDir(), "config.yaml")
			require.NoError(t, os.WriteFile(path, []byte(`redis:
  sentinel_addrs: [" sentinel-1.example.net:26379 ", "127.0.0.1:26380"]
  sentinel_master_name: " cache-primary "
  sentinel_username: "discovery-user"
  sentinel_password: "sentinel-secret"
`), 0600))
			t.Setenv("CONFIG_FILE", path)
			if disable {
				t.Setenv("REDIS_SENTINEL_ADDRS", "")
				t.Setenv("REDIS_SENTINEL_MASTER_NAME", "")
				t.Setenv("REDIS_SENTINEL_USERNAME", "")
				t.Setenv("REDIS_SENTINEL_PASSWORD", "")
			}
			cfg, err := Load()
			require.NoError(t, err)
			if disable {
				require.Empty(t, cfg.Redis.SentinelAddrs)
				require.Empty(t, cfg.Redis.SentinelMasterName)
				require.Empty(t, cfg.Redis.SentinelUsername)
				require.Empty(t, cfg.Redis.SentinelPassword)
			} else {
				require.Equal(t, []string{"sentinel-1.example.net:26379", "127.0.0.1:26380"}, cfg.Redis.SentinelAddrs)
				require.Equal(t, "cache-primary", cfg.Redis.SentinelMasterName)
				require.Equal(t, "discovery-user", cfg.Redis.SentinelUsername)
				require.Equal(t, "sentinel-secret", cfg.Redis.SentinelPassword)
			}
		})
	}
}

func TestLoadRedisSentinelRejectsInvalidEnvironment(t *testing.T) {
	for _, tc := range []struct{ name, addrs, master, want string }{
		{"addresses only", "127.0.0.1:26379", "", "configured together"},
		{"master only", "", "cache-primary", "configured together"},
		{"blank addresses", "  ", "cache-primary", "configured together"},
		{"blank master", "127.0.0.1:26379", "  ", "configured together"},
		{"empty list entry", "127.0.0.1:26379,", "cache-primary", "redis.sentinel_addrs[1]"},
		{"URL with credentials", "redis://user:do-not-log@localhost:26379", "cache-primary", "redis.sentinel_addrs[0]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetViperWithJWTSecret(t)
			t.Setenv("REDIS_SENTINEL_ADDRS", tc.addrs)
			t.Setenv("REDIS_SENTINEL_MASTER_NAME", tc.master)
			_, err := Load()
			require.ErrorContains(t, err, tc.want)
			require.NotContains(t, err.Error(), "do-not-log")
		})
	}
}

func TestValidateRedisSentinel(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*RedisConfig)
		want   string
	}{
		{"valid IPv6", func(r *RedisConfig) { r.SentinelAddrs = []string{"[::1]:26379"} }, ""},
		{"missing port", func(r *RedisConfig) { r.SentinelAddrs = []string{"localhost"} }, "host:port"},
		{"missing host", func(r *RedisConfig) { r.SentinelAddrs = []string{":26379"} }, "host:port"},
		{"zero port", func(r *RedisConfig) { r.SentinelAddrs = []string{"localhost:0"} }, "host:port"},
		{"out of range port", func(r *RedisConfig) { r.SentinelAddrs = []string{"localhost:65536"} }, "host:port"},
		{"named port", func(r *RedisConfig) { r.SentinelAddrs = []string{"localhost:redis"} }, "host:port"},
		{"whitespace host", func(r *RedisConfig) { r.SentinelAddrs = []string{"local host:26379"} }, "host:port"},
		{"whitespace master", func(r *RedisConfig) { r.SentinelMasterName = " " }, "whitespace"},
		{"orphan auth", func(r *RedisConfig) {
			r.SentinelAddrs = nil
			r.SentinelMasterName = ""
			r.SentinelPassword = "do-not-log"
		}, "require Sentinel discovery"},
		{"password-only auth", func(r *RedisConfig) { r.SentinelPassword = "sentinel-secret" }, ""},
		{"data auth only", func(r *RedisConfig) { r.Username = "data-user"; r.Password = "data-secret" }, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetViperWithJWTSecret(t)
			viper.Set("redis.sentinel_addrs", []string{"sentinel.example.net:26379"})
			viper.Set("redis.sentinel_master_name", "cache-primary")
			cfg, err := Load()
			require.NoError(t, err)
			tc.mutate(&cfg.Redis)
			err = cfg.Validate()
			if tc.want == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tc.want)
				require.NotContains(t, err.Error(), "do-not-log")
			}
		})
	}
}

package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadRefreshTokenStore(t *testing.T) {
	for _, value := range []string{"", "redis", "postgres", "POSTGRES", "other"} {
		t.Run(value, func(t *testing.T) {
			resetViperWithJWTSecret(t)
			if value != "" {
				t.Setenv("JWT_REFRESH_TOKEN_STORE", value)
			}
			cfg, err := Load()
			if value == "POSTGRES" || value == "other" {
				require.ErrorContains(t, err, "jwt.refresh_token_store")
				return
			}
			require.NoError(t, err)
			if value == "" {
				value = "redis"
			}
			require.Equal(t, value, cfg.JWT.RefreshTokenStore)
		})
	}
}

//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type settingPublicRepoStub struct {
	values map[string]string
	err    error
}

func (s *settingPublicRepoStub) Get(ctx context.Context, key string) (*Setting, error) {
	panic("unexpected Get call")
}

func (s *settingPublicRepoStub) GetValue(ctx context.Context, key string) (string, error) {
	panic("unexpected GetValue call")
}

func (s *settingPublicRepoStub) Set(ctx context.Context, key, value string) error {
	panic("unexpected Set call")
}

func (s *settingPublicRepoStub) GetMultiple(ctx context.Context, keys []string) (map[string]string, error) {
	if s.err != nil {
		return nil, s.err
	}
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := s.values[key]; ok {
			out[key] = value
		}
	}
	return out, nil
}

func (s *settingPublicRepoStub) SetMultiple(ctx context.Context, settings map[string]string) error {
	panic("unexpected SetMultiple call")
}

func (s *settingPublicRepoStub) GetAll(ctx context.Context) (map[string]string, error) {
	panic("unexpected GetAll call")
}

func (s *settingPublicRepoStub) Delete(ctx context.Context, key string) error {
	panic("unexpected Delete call")
}

func TestSettingService_GetPublicSettings_ExposesRegistrationEmailSuffixWhitelist(t *testing.T) {
	repo := &settingPublicRepoStub{
		values: map[string]string{
			SettingKeyRegistrationEnabled:              "true",
			SettingKeyEmailVerifyEnabled:               "true",
			SettingKeyRegistrationEmailSuffixWhitelist: `["@EXAMPLE.com"," @foo.bar ","*.EDU.CN","@invalid_domain",""]`,
		},
	}
	svc := NewSettingService(repo, &config.Config{})

	settings, err := svc.GetPublicSettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"@example.com", "@foo.bar", "*.edu.cn"}, settings.RegistrationEmailSuffixWhitelist)
}

func TestSettingService_GetModelPricingRuntimeIsIndependentAndOptOut(t *testing.T) {
	tests := []struct {
		name   string
		values map[string]string
		wantOn bool
	}{
		{name: "missing setting keeps pricing enabled", values: map[string]string{}, wantOn: true},
		{name: "available channels do not disable pricing", values: map[string]string{
			SettingKeyAvailableChannelsEnabled: "false",
		}, wantOn: true},
		{name: "explicit false disables pricing", values: map[string]string{
			SettingKeyModelPricingEnabled: "false",
		}, wantOn: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewSettingService(&settingPublicRepoStub{values: tt.values}, &config.Config{})
			runtime := svc.GetModelPricingRuntime(context.Background())
			require.Equal(t, tt.wantOn, runtime.Enabled)
		})
	}
}

func TestSettingService_GetPublicSettings_ExposesTablePreferences(t *testing.T) {
	repo := &settingPublicRepoStub{
		values: map[string]string{
			SettingKeyTableDefaultPageSize: "50",
			SettingKeyTablePageSizeOptions: "[20,50,100]",
		},
	}
	svc := NewSettingService(repo, &config.Config{})

	settings, err := svc.GetPublicSettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, 50, settings.TableDefaultPageSize)
	require.Equal(t, []int{20, 50, 100}, settings.TablePageSizeOptions)
}

func TestSettingService_GetPublicSettings_ExposesForceEmailOnThirdPartySignup(t *testing.T) {
	repo := &settingPublicRepoStub{
		values: map[string]string{
			SettingKeyForceEmailOnThirdPartySignup: "true",
		},
	}
	svc := NewSettingService(repo, &config.Config{})

	settings, err := svc.GetPublicSettings(context.Background())
	require.NoError(t, err)
	require.True(t, settings.ForceEmailOnThirdPartySignup)
}

func TestSettingService_GetPublicSettings_ExposesAllowUserViewErrorRequests(t *testing.T) {
	repo := &settingPublicRepoStub{
		values: map[string]string{
			SettingKeyAllowUserViewErrorRequests: "true",
		},
	}
	svc := NewSettingService(repo, &config.Config{})

	settings, err := svc.GetPublicSettings(context.Background())
	require.NoError(t, err)
	require.True(t, settings.AllowUserViewErrorRequests)
}

func TestSettingService_GetPublicSettings_ModelPricingIsIndependentFromAvailableChannels(t *testing.T) {
	tests := []struct {
		name                    string
		values                  map[string]string
		wantModelPricingEnabled bool
		wantAvailableChannelsOn bool
	}{
		{
			name:                    "missing settings preserve XIASS model pricing default",
			values:                  map[string]string{},
			wantModelPricingEnabled: true,
		},
		{
			name: "explicit model pricing disable leaves channels disabled",
			values: map[string]string{
				SettingKeyModelPricingEnabled: "false",
			},
			wantModelPricingEnabled: false,
		},
		{
			name: "available channels opt in does not hide model pricing",
			values: map[string]string{
				SettingKeyAvailableChannelsEnabled: "true",
			},
			wantModelPricingEnabled: true,
			wantAvailableChannelsOn: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewSettingService(&settingPublicRepoStub{values: tt.values}, &config.Config{})

			settings, err := svc.GetPublicSettings(context.Background())
			require.NoError(t, err)
			require.Equal(t, tt.wantModelPricingEnabled, settings.ModelPricingEnabled)
			require.Equal(t, tt.wantAvailableChannelsOn, settings.AvailableChannelsEnabled)
		})
	}
}

func TestSettingService_GetPublicSettingsForInjection_ExposesModelPricingAndPublicMonitorSettings(t *testing.T) {
	svc := NewSettingService(&settingPublicRepoStub{values: map[string]string{
		SettingKeyRegistrationEmailDomainQuotaEnabled: "true",
		SettingKeyTencentCaptchaRegion:                TencentCaptchaRegionINTL,
		SettingKeyCompactHomeEnabled:                  "true",
		SettingKeyChannelMonitorMode:                  ChannelMonitorModeV2,
		SettingKeyChannelMonitorHideThroughput:        "false",
		SettingKeyChannelMonitorShowQuota:             "true",
		SettingKeyAvailableChannelsEnabled:            "true",
		SettingKeyModelPricingEnabled:                 "false",
		SettingKeyModelPlazaEnabled:                   "true",
		SettingKeyModelPlazaRequireAuth:               "true",
	}}, &config.Config{})

	injected, err := svc.GetPublicSettingsForInjection(context.Background())
	require.NoError(t, err)
	payload, ok := injected.(*PublicSettingsInjectionPayload)
	require.True(t, ok)
	require.True(t, payload.RegistrationEmailDomainQuotaEnabled)
	require.Equal(t, TencentCaptchaRegionINTL, payload.TencentCaptchaRegion)
	require.True(t, payload.CompactHomeEnabled)
	require.Equal(t, ChannelMonitorModeV1, payload.ChannelMonitorMode)
	require.False(t, payload.ChannelMonitorHideThroughput)
	require.True(t, payload.ChannelMonitorShowQuota)
	require.True(t, payload.AvailableChannelsEnabled)
	require.False(t, payload.ModelPricingEnabled)
	require.True(t, payload.ModelPlazaEnabled)
	require.True(t, payload.ModelPlazaRequireAuth)
}

func TestSettingService_GetPublicSettings_ExposesWeChatOAuthModeCapabilities(t *testing.T) {
	svc := NewSettingService(&settingPublicRepoStub{
		values: map[string]string{
			SettingKeyWeChatConnectEnabled:             "true",
			SettingKeyWeChatConnectAppID:               "wx-mp-app",
			SettingKeyWeChatConnectAppSecret:           "wx-mp-secret",
			SettingKeyWeChatConnectMode:                "mp",
			SettingKeyWeChatConnectScopes:              "snsapi_base",
			SettingKeyWeChatConnectOpenEnabled:         "true",
			SettingKeyWeChatConnectMPEnabled:           "true",
			SettingKeyWeChatConnectRedirectURL:         "https://api.example.com/api/v1/auth/oauth/wechat/callback",
			SettingKeyWeChatConnectFrontendRedirectURL: "/auth/wechat/callback",
		},
	}, &config.Config{})

	settings, err := svc.GetPublicSettings(context.Background())
	require.NoError(t, err)
	require.True(t, settings.WeChatOAuthEnabled)
	require.True(t, settings.WeChatOAuthOpenEnabled)
	require.True(t, settings.WeChatOAuthMPEnabled)
}

func TestSettingService_GetPublicSettings_DoesNotExposeMobileOnlyWeChatAsWebOAuthAvailable(t *testing.T) {
	svc := NewSettingService(&settingPublicRepoStub{
		values: map[string]string{
			SettingKeyWeChatConnectEnabled:             "true",
			SettingKeyWeChatConnectMobileEnabled:       "true",
			SettingKeyWeChatConnectMode:                "mobile",
			SettingKeyWeChatConnectMobileAppID:         "wx-mobile-app",
			SettingKeyWeChatConnectMobileAppSecret:     "wx-mobile-secret",
			SettingKeyWeChatConnectFrontendRedirectURL: "/auth/wechat/callback",
		},
	}, &config.Config{})

	settings, err := svc.GetPublicSettings(context.Background())
	require.NoError(t, err)
	require.False(t, settings.WeChatOAuthEnabled)
	require.False(t, settings.WeChatOAuthOpenEnabled)
	require.False(t, settings.WeChatOAuthMPEnabled)
	require.True(t, settings.WeChatOAuthMobileEnabled)
}

func TestSettingService_GetPublicSettings_FallsBackToConfigForWeChatOAuthCapabilities(t *testing.T) {
	svc := NewSettingService(&settingPublicRepoStub{values: map[string]string{}}, &config.Config{
		WeChat: config.WeChatConnectConfig{
			Enabled:             true,
			OpenEnabled:         true,
			OpenAppID:           "wx-open-config",
			OpenAppSecret:       "wx-open-secret",
			FrontendRedirectURL: "/auth/wechat/config-callback",
		},
	})

	settings, err := svc.GetPublicSettings(context.Background())
	require.NoError(t, err)
	require.True(t, settings.WeChatOAuthEnabled)
	require.True(t, settings.WeChatOAuthOpenEnabled)
	require.False(t, settings.WeChatOAuthMPEnabled)
	require.False(t, settings.WeChatOAuthMobileEnabled)
}

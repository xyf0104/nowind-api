//go:build unit

package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
	"github.com/stretchr/testify/require"
)

type grokBaseURLSettingRepoStub struct{ values map[string]string }

func (r *grokBaseURLSettingRepoStub) GetValue(_ context.Context, key string) (string, error) {
	if value, ok := r.values[key]; ok {
		return value, nil
	}
	return "", fmt.Errorf("setting %s not found", key)
}
func (r *grokBaseURLSettingRepoStub) Get(context.Context, string) (*Setting, error) {
	return nil, fmt.Errorf("unused")
}
func (r *grokBaseURLSettingRepoStub) Set(context.Context, string, string) error { return nil }
func (r *grokBaseURLSettingRepoStub) GetMultiple(context.Context, []string) (map[string]string, error) {
	return r.values, nil
}
func (r *grokBaseURLSettingRepoStub) SetMultiple(context.Context, map[string]string) error {
	return nil
}
func (r *grokBaseURLSettingRepoStub) GetAll(context.Context) (map[string]string, error) {
	return r.values, nil
}
func (r *grokBaseURLSettingRepoStub) Delete(context.Context, string) error { return nil }

func TestGrokBaseURLForMode(t *testing.T) {
	for _, testCase := range []struct {
		mode string
		want string
	}{
		{"api", xai.DefaultBaseURL},
		{"us-east-1", xai.DefaultUSEast1BaseURL},
		{"us-west-2", xai.DefaultUSWest2BaseURL},
		{"eu-west-1", xai.DefaultEUWest1BaseURL},
		{"cli", xai.DefaultCLIBaseURL},
		{"invalid", xai.DefaultCLIBaseURL},
	} {
		t.Run(testCase.mode, func(t *testing.T) {
			require.Equal(t, testCase.want, GrokBaseURLForMode(testCase.mode))
		})
	}
}

func TestSettingServiceResolveGrokBaseURLHonorsModeAndXIASSExplicitOptIn(t *testing.T) {
	repo := &grokBaseURLSettingRepoStub{values: map[string]string{
		SettingKeyGrokDefaultBaseURLMode: GrokDefaultBaseURLModeUSWest2,
	}}
	service := NewSettingService(repo, nil)
	account := &Account{Platform: PlatformGrok, Type: AccountTypeOAuth, Credentials: map[string]any{}}
	require.Equal(t, xai.DefaultUSWest2BaseURL, service.ResolveGrokBaseURL(context.Background(), account))

	// Historical stored values remain inert until the XIASS opt-in flag is set.
	account.Credentials["base_url"] = xai.DefaultBaseURL
	require.Equal(t, xai.DefaultUSWest2BaseURL, service.ResolveGrokBaseURL(context.Background(), account))
	account.Credentials[grokCustomBaseURLEnabledCredentialKey] = true
	require.Equal(t, xai.DefaultBaseURL, service.ResolveGrokBaseURL(context.Background(), account))

	account.Credentials["base_url"] = xai.DefaultEUWest1BaseURL
	require.Equal(t, xai.DefaultEUWest1BaseURL, service.ResolveGrokBaseURL(context.Background(), account))
}

func TestAccountGetGrokBaseURLOrPreservesOptedInCustomOAuthURLForPolicyValidation(t *testing.T) {
	account := &Account{Platform: PlatformGrok, Type: AccountTypeOAuth, Credentials: map[string]any{
		"base_url":                            "https://relay.example/v1",
		grokCustomBaseURLEnabledCredentialKey: true,
	}}
	require.Equal(t, "https://relay.example/v1", account.GetGrokBaseURLOr(xai.DefaultCLIBaseURL))
}

func TestBuildGrokResponsesRequestUsesAdminDefaultWithoutOverridingXIASSExplicitAccountURL(t *testing.T) {
	repo := &grokBaseURLSettingRepoStub{values: map[string]string{
		SettingKeyGrokDefaultBaseURLMode: GrokDefaultBaseURLModeEUWest1,
	}}
	settings := NewSettingService(repo, nil)
	account := &Account{Platform: PlatformGrok, Type: AccountTypeOAuth, Credentials: map[string]any{}}

	req, err := buildGrokResponsesRequest(context.Background(), nil, account, []byte(`{"model":"grok-4.5"}`), "token", "", nil, settings)
	require.NoError(t, err)
	require.Equal(t, xai.DefaultEUWest1BaseURL+"/responses", req.URL.String())
	require.Empty(t, req.Header.Get("X-Grok-Client-Version"), "regional API traffic must not impersonate the CLI gateway")

	account.Credentials["base_url"] = "https://relay.example/v1"
	account.Credentials[grokCustomBaseURLEnabledCredentialKey] = true
	req, err = buildGrokResponsesRequest(context.Background(), nil, account, []byte(`{"model":"grok-4.5"}`), "token", "", nil, settings)
	require.NoError(t, err)
	require.Equal(t, "https://relay.example/v1/responses", req.URL.String())
}

func TestSettingServiceGrokRuntimeSettingsAreCachedAndUseStableDefaults(t *testing.T) {
	repo := &grokBaseURLSettingRepoStub{values: map[string]string{
		SettingKeyGrokDefaultTextModel:           "grok-4.1",
		SettingKeyGrokCrossClientModelMapEnabled: "false",
		SettingKeyGrokDefaultBaseURLMode:         GrokDefaultBaseURLModeAPI,
	}}
	settings := NewSettingService(repo, nil)
	got := settings.GetGrokRuntimeSettings(context.Background())
	require.Equal(t, "grok-4.1", got.DefaultTextModel)
	require.False(t, got.CrossClientMapEnabled)
	require.Equal(t, GrokDefaultBaseURLModeAPI, got.DefaultBaseURLMode)

	// A second read is served from the per-service cache.  Mutating the backing
	// map does not change the already observed runtime policy until an explicit
	// settings refresh/update occurs.
	repo.values[SettingKeyGrokDefaultTextModel] = "grok-4.6"
	require.Equal(t, "grok-4.1", settings.GetGrokDefaultTextModel(context.Background()))
}

func TestOpenAIGatewayGrokRuntimeModelResolutionHonorsAdminSwitchAndAccountMapping(t *testing.T) {
	newGateway := func(values map[string]string) *OpenAIGatewayService {
		return &OpenAIGatewayService{settingService: NewSettingService(&grokBaseURLSettingRepoStub{values: values}, nil)}
	}
	account := &Account{Platform: PlatformGrok, Type: AccountTypeOAuth, Credentials: map[string]any{}}

	withMapping := newGateway(map[string]string{
		SettingKeyGrokDefaultTextModel:           "grok-4.1",
		SettingKeyGrokCrossClientModelMapEnabled: "true",
	})
	require.Equal(t, "grok-4.1", withMapping.resolveGrokTextModel(context.Background(), account, "gpt-5.6-sol"))
	require.Equal(t, "grok-4.1", withMapping.resolveGrokTextModel(context.Background(), account, "grok"))

	off := newGateway(map[string]string{
		SettingKeyGrokDefaultTextModel:           "grok-4.1",
		SettingKeyGrokCrossClientModelMapEnabled: "false",
	})
	require.Equal(t, "gpt-5.6-sol", off.resolveGrokTextModel(context.Background(), account, "gpt-5.6-sol"))

	account.Credentials["model_mapping"] = map[string]any{"gpt-5.6-sol": "grok-4.6"}
	require.Equal(t, "grok-4.6", off.resolveGrokTextModel(context.Background(), account, "gpt-5.6-sol"))
}

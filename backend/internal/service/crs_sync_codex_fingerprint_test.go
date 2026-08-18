//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

const crsExternalCodexFingerprintSeed = "33333333-3333-4333-8333-333333333333"

func TestCRSSyncOpenAIOAuthCreatesSystemFingerprintSeedForEnabledModes(t *testing.T) {
	for _, mode := range []string{"device", "session", "full"} {
		t.Run(mode, func(t *testing.T) {
			repo := newCRSLongContextAccountRepo()
			result := runCRSOpenAILongContextSync(t, repo, crsOpenAILongContextSource{
				collection:  "openaiOAuthAccounts",
				credentials: map[string]any{"access_token": "oauth-token"},
				extra:       map[string]any{codexFingerprintModeExtraKey: mode},
			})

			require.Equal(t, 1, result.Created)
			stored := repo.accounts["crs-openai-1"]
			require.Equal(t, mode, stored.Extra[codexFingerprintModeExtraKey])
			requireValidCodexFingerprintSeed(t, stored.Extra)
		})
	}
}

func TestCRSSyncOpenAIOAuthStripsExternalFingerprintSeed(t *testing.T) {
	repo := newCRSLongContextAccountRepo()
	result := runCRSOpenAILongContextSync(t, repo, crsOpenAILongContextSource{
		collection:  "openaiOAuthAccounts",
		credentials: map[string]any{"access_token": "oauth-token"},
		extra: map[string]any{
			codexFingerprintModeExtraKey: "device",
			codexFingerprintSeedExtraKey: crsExternalCodexFingerprintSeed,
		},
	})

	require.Equal(t, 1, result.Created)
	storedSeed := requireValidCodexFingerprintSeed(t, repo.accounts["crs-openai-1"].Extra)
	require.NotEqual(t, crsExternalCodexFingerprintSeed, storedSeed)
}

func TestCRSSyncOpenAIOAuthOffDropsExternalFingerprintSeed(t *testing.T) {
	repo := newCRSLongContextAccountRepo()
	result := runCRSOpenAILongContextSync(t, repo, crsOpenAILongContextSource{
		collection:  "openaiOAuthAccounts",
		credentials: map[string]any{"access_token": "oauth-token"},
		extra: map[string]any{
			codexFingerprintModeExtraKey: "off",
			codexFingerprintSeedExtraKey: crsExternalCodexFingerprintSeed,
		},
	})

	require.Equal(t, 1, result.Created)
	stored := repo.accounts["crs-openai-1"]
	require.Equal(t, "off", stored.Extra[codexFingerprintModeExtraKey])
	require.NotContains(t, stored.Extra, codexFingerprintSeedExtraKey)
}

func TestCRSSyncOpenAIOAuthUpdatePreservesLocalFingerprintSeed(t *testing.T) {
	existing := &Account{
		ID:       41,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Extra: map[string]any{
			"crs_account_id":             "crs-openai-1",
			codexFingerprintModeExtraKey: "device",
			codexFingerprintSeedExtraKey: testCodexFingerprintSeed,
		},
	}
	repo := newCRSLongContextAccountRepo(existing)
	result := runCRSOpenAILongContextSync(t, repo, crsOpenAILongContextSource{
		collection:  "openaiOAuthAccounts",
		credentials: map[string]any{"access_token": "oauth-token"},
		extra: map[string]any{
			codexFingerprintModeExtraKey: "full",
			codexFingerprintSeedExtraKey: crsExternalCodexFingerprintSeed,
		},
	})

	require.Equal(t, 1, result.Updated)
	stored := repo.accounts["crs-openai-1"]
	require.Equal(t, "full", stored.Extra[codexFingerprintModeExtraKey])
	require.Equal(t, testCodexFingerprintSeed, requireValidCodexFingerprintSeed(t, stored.Extra))
}

func TestCRSSyncOpenAIOAuthUpdateCreatesFingerprintSeedWhenMissing(t *testing.T) {
	existing := &Account{
		ID:       41,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Extra: map[string]any{
			"crs_account_id":             "crs-openai-1",
			codexFingerprintModeExtraKey: "off",
		},
	}
	repo := newCRSLongContextAccountRepo(existing)
	result := runCRSOpenAILongContextSync(t, repo, crsOpenAILongContextSource{
		collection:  "openaiOAuthAccounts",
		credentials: map[string]any{"access_token": "oauth-token"},
		extra:       map[string]any{codexFingerprintModeExtraKey: "session"},
	})

	require.Equal(t, 1, result.Updated)
	stored := repo.accounts["crs-openai-1"]
	require.Equal(t, "session", stored.Extra[codexFingerprintModeExtraKey])
	requireValidCodexFingerprintSeed(t, stored.Extra)
}

func TestCRSSyncOpenAIOAuthDisableReenablePreservesLocalFingerprintSeed(t *testing.T) {
	existing := &Account{
		ID:       41,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Extra: map[string]any{
			"crs_account_id":             "crs-openai-1",
			codexFingerprintModeExtraKey: "session",
			codexFingerprintSeedExtraKey: testCodexFingerprintSeed,
		},
	}
	repo := newCRSLongContextAccountRepo(existing)

	disabled := runCRSOpenAILongContextSync(t, repo, crsOpenAILongContextSource{
		collection:  "openaiOAuthAccounts",
		credentials: map[string]any{"access_token": "oauth-token"},
		extra: map[string]any{
			codexFingerprintModeExtraKey: "off",
			codexFingerprintSeedExtraKey: crsExternalCodexFingerprintSeed,
		},
	})
	require.Equal(t, 1, disabled.Updated)
	require.Equal(t, "off", repo.accounts["crs-openai-1"].Extra[codexFingerprintModeExtraKey])
	require.Equal(t, testCodexFingerprintSeed, requireValidCodexFingerprintSeed(t, repo.accounts["crs-openai-1"].Extra))

	reenabled := runCRSOpenAILongContextSync(t, repo, crsOpenAILongContextSource{
		collection:  "openaiOAuthAccounts",
		credentials: map[string]any{"access_token": "oauth-token"},
		extra:       map[string]any{codexFingerprintModeExtraKey: "session"},
	})
	require.Equal(t, 1, reenabled.Updated)
	require.Equal(t, "session", repo.accounts["crs-openai-1"].Extra[codexFingerprintModeExtraKey])
	require.Equal(t, testCodexFingerprintSeed, requireValidCodexFingerprintSeed(t, repo.accounts["crs-openai-1"].Extra))
}

func TestCRSSyncNonOpenAIOAuthFingerprintExtraIsUnchanged(t *testing.T) {
	repo := newCRSLongContextAccountRepo()
	result := runCRSOpenAILongContextSync(t, repo, crsOpenAILongContextSource{
		collection:  "openaiResponsesAccounts",
		credentials: map[string]any{"api_key": "sk-test"},
		extra: map[string]any{
			codexFingerprintModeExtraKey: "session",
			codexFingerprintSeedExtraKey: crsExternalCodexFingerprintSeed,
		},
	})

	require.Equal(t, 1, result.Created)
	stored := repo.accounts["crs-openai-1"]
	require.Equal(t, AccountTypeAPIKey, stored.Type)
	require.Equal(t, "session", stored.Extra[codexFingerprintModeExtraKey])
	require.Equal(t, crsExternalCodexFingerprintSeed, stored.Extra[codexFingerprintSeedExtraKey])
}

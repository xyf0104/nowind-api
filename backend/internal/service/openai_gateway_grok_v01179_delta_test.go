package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestPatchGrokResponsesBodyRemovesRedundantViewImageForInlineImage(t *testing.T) {
	t.Parallel()
	body := []byte(`{
		"input":[{"role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,AA=="}]}],
		"tools":[
			{"type":"function","name":"view_image","parameters":{"type":"object"}},
			{"type":"function","name":"shell","parameters":{"type":"object"}}
		],
		"parallel_tool_calls":true
	}`)

	patched, err := patchGrokResponsesBody(body, "grok-4.5")
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(patched, `tools.#(name=="view_image")`).Exists())
	require.True(t, gjson.GetBytes(patched, `tools.#(name=="shell")`).Exists())
}

func TestPatchGrokResponsesBodyPreservesExplicitViewImageChoice(t *testing.T) {
	t.Parallel()
	body := []byte(`{
		"input":[{"role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,AA=="}]}],
		"tools":[{"type":"function","name":"view_image","parameters":{"type":"object"}}],
		"tool_choice":{"type":"function","name":"view_image"}
	}`)

	patched, err := patchGrokResponsesBody(body, "grok-4.5")
	require.NoError(t, err)
	require.True(t, gjson.GetBytes(patched, `tools.#(name=="view_image")`).Exists())
}

func TestStripRedundantGrokChatViewImageToolForInlineImage(t *testing.T) {
	t.Parallel()
	body := []byte(`{
		"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,AA=="}}]}],
		"tools":[{"type":"function","function":{"name":"view_image","parameters":{"type":"object"}}}],
		"tool_choice":"auto",
		"parallel_tool_calls":true
	}`)

	patched, err := stripRedundantGrokChatViewImageTool(body)
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(patched, "tools").Exists())
	require.False(t, gjson.GetBytes(patched, "tool_choice").Exists())
	require.False(t, gjson.GetBytes(patched, "parallel_tool_calls").Exists())
}

func TestPatchGrokResponsesBodyPreservesXHighReasoningForGrok46(t *testing.T) {
	t.Parallel()
	body := []byte(`{"input":"hello","reasoning":{"effort":"xhigh"}}`)

	patched, err := patchGrokResponsesBody(body, "grok-4.6")
	require.NoError(t, err)
	require.Equal(t, "xhigh", gjson.GetBytes(patched, "reasoning.effort").String())
}

func TestPersistGrokTransientModelCooldownOnlyBlocksHeavyModel(t *testing.T) {
	account := &Account{ID: 919179}
	heavyModel := "grok-4.20-multi-agent-0309"
	key := grokModelQuotaBlockKey(account.ID, heavyModel)
	t.Cleanup(func() {
		globalGrokModelQuotaBlocks.mu.Lock()
		delete(globalGrokModelQuotaBlocks.items, key)
		globalGrokModelQuotaBlocks.mu.Unlock()
	})

	require.False(t, persistGrokTransientModelCooldown(account, GrokUpstreamFailureDecision{
		Model: "grok-4.5", Cooldown: time.Minute,
	}))
	require.True(t, persistGrokTransientModelCooldown(account, GrokUpstreamFailureDecision{
		Model: heavyModel, Cooldown: time.Minute,
	}))
	require.True(t, isGrokModelQuotaBlocked(account.ID, heavyModel, time.Now()))
}

package handler

import (
	"fmt"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestDeriveOpenAIForwardAttemptBody_CompactionStopsCrossModeReplay(t *testing.T) {
	for _, kind := range []string{"compaction", "compaction_summary"} {
		for _, suffix := range []string{"", `,{"type":"message","role":"user","content":"continue"}`} {
			t.Run(fmt.Sprintf("%s/current_user=%v", kind, suffix != ""), func(t *testing.T) {
				canonical := []byte(fmt.Sprintf(`{"model":"gpt-6-astra","previous_response_id":"resp_pinned","input":[{"type":%q,"encrypted_content":"opaque-history"}%s]}`, kind, suffix))
				original := append([]byte(nil), canonical...)
				h := &OpenAIGatewayHandler{}
				state := &openAIPassthroughFailoverState{}
				outgoing := 0
				for _, account := range []*service.Account{newOpenAIPassthroughAccount(771, true), newOpenAIPassthroughAccount(772, false)} {
					body, err := h.deriveOpenAIForwardAttemptBody(nil, canonical, account, state)
					if account.ID == 771 {
						require.NoError(t, err)
						require.Equal(t, canonical, body)
						outgoing++
					} else {
						require.ErrorIs(t, err, service.ErrOpenAIContextUnavailable)
						require.Nil(t, body, "handler must not receive a shortened second attempt")
					}
				}
				require.Equal(t, 1, outgoing)
				require.Equal(t, original, canonical)
				require.Equal(t, "resp_pinned", gjson.GetBytes(canonical, "previous_response_id").String())
			})
		}
	}
}

// kiroReasoningCanonicalBody mirrors a native OpenAI Responses request whose input
// carries a provider-specific encrypted reasoning item (Kiro shape: encrypted_content
// coupled with an id and summary), plus surrounding message items.
const kiroReasoningCanonicalBody = `{"model":"gpt-5.1","stream":false,"input":[` +
	`{"type":"message","role":"user","content":"hello"},` +
	`{"type":"reasoning","id":"rs_kiro_abc123","encrypted_content":"ENC_BLOB","summary":[{"type":"summary_text","text":"thinking"}]},` +
	`{"type":"message","role":"assistant","content":"hi"}` +
	`]}`

func newOpenAIPassthroughAccount(id int64, passthrough bool) *service.Account {
	extra := map[string]any{"openai_passthrough": passthrough}
	return &service.Account{
		ID:       id,
		Platform: service.PlatformOpenAI,
		Type:     service.AccountTypeAPIKey,
		Extra:    extra,
	}
}

func reasoningItemCount(t *testing.T, body []byte) int {
	t.Helper()
	count := 0
	gjson.GetBytes(body, "input").ForEach(func(_, item gjson.Result) bool {
		if item.Get("type").String() == "reasoning" {
			count++
		}
		return true
	})
	return count
}

// TestDeriveOpenAIForwardAttemptBody_CrossModeStripsKiroReasoning drives the real
// account-switch/request-body seam used inside OpenAIGatewayHandler.Responses: the
// same deriveOpenAIForwardAttemptBody call the failover loop makes, fed real
// *service.Account values in sequence. It proves the first passthrough attempt gets
// the untouched reasoning item, and after a failover to a non-passthrough account
// the second attempt body has the entire reasoning item removed — while the
// immutable canonical body is never mutated.
func TestDeriveOpenAIForwardAttemptBody_CrossModeStripsKiroReasoning(t *testing.T) {
	h := &OpenAIGatewayHandler{}
	canonical := []byte(kiroReasoningCanonicalBody)

	kiro := newOpenAIPassthroughAccount(1, true)     // Kiro: openai_passthrough=true
	bedrock := newOpenAIPassthroughAccount(2, false) // Bedrock Mantle: passthrough disabled

	state := &openAIPassthroughFailoverState{}

	// Attempt 1 — passthrough (Kiro): original reasoning item must survive intact.
	firstBody, err := h.deriveOpenAIForwardAttemptBody(nil, canonical, kiro, state)
	require.NoError(t, err)
	require.Equal(t, 1, reasoningItemCount(t, firstBody), "first passthrough attempt must keep the reasoning item")
	require.Equal(t, "ENC_BLOB", gjson.GetBytes(firstBody, "input.1.encrypted_content").String())
	require.Equal(t, "rs_kiro_abc123", gjson.GetBytes(firstBody, "input.1.id").String())
	require.JSONEq(t, kiroReasoningCanonicalBody, string(firstBody), "first attempt body must equal the canonical body")

	// Attempt 2 — failover switches to non-passthrough (Bedrock): the whole
	// provider-specific encrypted reasoning item (id/summary/encrypted_content) is dropped.
	secondBody, err := h.deriveOpenAIForwardAttemptBody(nil, canonical, bedrock, state)
	require.NoError(t, err)
	require.Equal(t, 0, reasoningItemCount(t, secondBody), "cross-mode attempt must drop the reasoning item entirely")
	require.False(t, gjson.GetBytes(secondBody, "input.#(encrypted_content)").Exists(), "no encrypted_content may remain")
	require.NotContains(t, string(secondBody), "rs_kiro_abc123", "coupled reasoning id must be gone")
	require.NotContains(t, string(secondBody), "ENC_BLOB")

	// Surviving message items are preserved.
	require.Equal(t, 2, int(gjson.GetBytes(secondBody, "input.#").Int()))
	require.Equal(t, "hello", gjson.GetBytes(secondBody, "input.0.content").String())
	require.Equal(t, "hi", gjson.GetBytes(secondBody, "input.1.content").String())

	// The canonical body is immutable across attempts.
	require.JSONEq(t, kiroReasoningCanonicalBody, string(canonical), "canonical forwardBody must never be mutated")
}

// TestDeriveOpenAIForwardAttemptBody_SameModePreservesReasoning proves that
// non-passthrough attempts before any passthrough attempt, passthrough-family
// failovers, and switching into passthrough forward the canonical reasoning item
// unchanged.
func TestDeriveOpenAIForwardAttemptBody_SameModePreservesReasoning(t *testing.T) {
	h := &OpenAIGatewayHandler{}
	canonical := []byte(kiroReasoningCanonicalBody)

	t.Run("non_passthrough_to_non_passthrough", func(t *testing.T) {
		state := &openAIPassthroughFailoverState{}
		first, err := h.deriveOpenAIForwardAttemptBody(nil, canonical, newOpenAIPassthroughAccount(10, false), state)
		require.NoError(t, err)
		second, err := h.deriveOpenAIForwardAttemptBody(nil, canonical, newOpenAIPassthroughAccount(11, false), state)
		require.NoError(t, err)
		require.Equal(t, 1, reasoningItemCount(t, first))
		require.Equal(t, 1, reasoningItemCount(t, second), "Bedrock-family failover must preserve reasoning")
		require.JSONEq(t, kiroReasoningCanonicalBody, string(second))
	})

	t.Run("passthrough_to_passthrough", func(t *testing.T) {
		state := &openAIPassthroughFailoverState{}
		first, err := h.deriveOpenAIForwardAttemptBody(nil, canonical, newOpenAIPassthroughAccount(20, true), state)
		require.NoError(t, err)
		second, err := h.deriveOpenAIForwardAttemptBody(nil, canonical, newOpenAIPassthroughAccount(21, true), state)
		require.NoError(t, err)
		require.Equal(t, 1, reasoningItemCount(t, first))
		require.Equal(t, 1, reasoningItemCount(t, second), "Kiro-family failover must preserve reasoning")
	})

	t.Run("non_passthrough_to_passthrough", func(t *testing.T) {
		state := &openAIPassthroughFailoverState{}
		_, err := h.deriveOpenAIForwardAttemptBody(nil, canonical, newOpenAIPassthroughAccount(30, false), state)
		require.NoError(t, err)
		second, err := h.deriveOpenAIForwardAttemptBody(nil, canonical, newOpenAIPassthroughAccount(31, true), state)
		require.NoError(t, err)
		require.Equal(t, 1, reasoningItemCount(t, second), "switching into passthrough must preserve reasoning")
	})

	t.Run("same_account_pool_retry", func(t *testing.T) {
		state := &openAIPassthroughFailoverState{}
		kiro := newOpenAIPassthroughAccount(40, true)
		_, err := h.deriveOpenAIForwardAttemptBody(nil, canonical, kiro, state)
		require.NoError(t, err)
		retry, err := h.deriveOpenAIForwardAttemptBody(nil, canonical, kiro, state)
		require.NoError(t, err)
		require.Equal(t, 1, reasoningItemCount(t, retry), "same-account retry must preserve reasoning")
	})
}

// TestDeriveOpenAIForwardAttemptBody_SanitizationSticksAcrossBedrockRetries proves
// that once a passthrough account has been attempted, all later non-passthrough
// retries and failovers remain sanitized instead of restoring the canonical Kiro
// reasoning item after the first Bedrock failure.
func TestDeriveOpenAIForwardAttemptBody_SanitizationSticksAcrossBedrockRetries(t *testing.T) {
	h := &OpenAIGatewayHandler{}
	canonical := []byte(kiroReasoningCanonicalBody)
	state := &openAIPassthroughFailoverState{}

	kiro := newOpenAIPassthroughAccount(50, true)
	bedrockA := newOpenAIPassthroughAccount(51, false)
	bedrockB := newOpenAIPassthroughAccount(52, false)

	first, err := h.deriveOpenAIForwardAttemptBody(nil, canonical, kiro, state)
	require.NoError(t, err)
	second, err := h.deriveOpenAIForwardAttemptBody(nil, canonical, bedrockA, state)
	require.NoError(t, err)
	retry, err := h.deriveOpenAIForwardAttemptBody(nil, canonical, bedrockA, state)
	require.NoError(t, err)
	nextAccount, err := h.deriveOpenAIForwardAttemptBody(nil, canonical, bedrockB, state)
	require.NoError(t, err)

	require.Equal(t, 1, reasoningItemCount(t, first))
	require.Equal(t, 0, reasoningItemCount(t, second))
	require.Equal(t, 0, reasoningItemCount(t, retry), "same Bedrock account retry must remain sanitized")
	require.Equal(t, 0, reasoningItemCount(t, nextAccount), "later Bedrock account must remain sanitized")
	require.JSONEq(t, kiroReasoningCanonicalBody, string(canonical), "canonical forwardBody must never be mutated")
}

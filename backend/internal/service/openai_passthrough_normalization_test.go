package service

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestNormalizeOpenAIPassthroughOAuthBody_RemovesUnsupportedUser(t *testing.T) {
	body := []byte(`{"model":"gpt-5.4","input":"hello","user":"user_123","metadata":{"user_id":"user_123"},"prompt_cache_retention":"24h","safety_identifier":"sid","stream_options":{"include_usage":true}}`)

	normalized, changed, err := normalizeOpenAIPassthroughOAuthBody(body, false)
	require.NoError(t, err)
	require.True(t, changed)
	for _, field := range openAIChatGPTInternalUnsupportedFields {
		require.False(t, gjson.GetBytes(normalized, field).Exists(), "%s should be stripped", field)
	}
	require.True(t, gjson.GetBytes(normalized, "stream").Bool())
	require.False(t, gjson.GetBytes(normalized, "store").Bool())
}

func TestNormalizeOpenAIPassthroughOAuthBody_CompactRemovesUnsupportedUser(t *testing.T) {
	body := []byte(`{"model":"gpt-5.4","input":"hello","user":"user_123","metadata":{"user_id":"user_123"},"stream":true,"store":true}`)

	normalized, changed, err := normalizeOpenAIPassthroughOAuthBody(body, true)
	require.NoError(t, err)
	require.True(t, changed)
	require.False(t, gjson.GetBytes(normalized, "user").Exists())
	require.False(t, gjson.GetBytes(normalized, "metadata").Exists())
	require.False(t, gjson.GetBytes(normalized, "stream").Exists())
	require.False(t, gjson.GetBytes(normalized, "store").Exists())
}

func TestNormalizeOpenAIPassthroughOAuthBody_StringInputWrappedAsArray(t *testing.T) {
	body := []byte(`{"model":"gpt-5.4","input":"hello world"}`)

	normalized, changed, err := normalizeOpenAIPassthroughOAuthBody(body, false)
	require.NoError(t, err)
	require.True(t, changed)

	input := gjson.GetBytes(normalized, "input")
	require.True(t, input.IsArray(), "string input should be converted to array")
	items := input.Array()
	require.Len(t, items, 1)
	require.Equal(t, "message", items[0].Get("type").String())
	require.Equal(t, "user", items[0].Get("role").String())
	require.Equal(t, "hello world", items[0].Get("content").String())
}

func TestNormalizeOpenAIPassthroughOAuthBody_EmptyStringInputWrappedAsEmptyArray(t *testing.T) {
	body := []byte(`{"model":"gpt-5.4","input":"  "}`)

	normalized, changed, err := normalizeOpenAIPassthroughOAuthBody(body, false)
	require.NoError(t, err)
	require.True(t, changed)

	input := gjson.GetBytes(normalized, "input")
	require.True(t, input.IsArray())
	require.Len(t, input.Array(), 0, "whitespace-only input should become empty array")
}

func TestNormalizeOpenAIPassthroughOAuthBody_ObjectInputWrappedAsArray(t *testing.T) {
	body := []byte(`{"model":"gpt-5.4","input":{"type":"message","role":"user","content":"hi"}}`)

	normalized, changed, err := normalizeOpenAIPassthroughOAuthBody(body, false)
	require.NoError(t, err)
	require.True(t, changed)

	input := gjson.GetBytes(normalized, "input")
	require.True(t, input.IsArray(), "object input should be wrapped in array")
	items := input.Array()
	require.Len(t, items, 1)
	require.Equal(t, "message", items[0].Get("type").String())
}

func TestNormalizeOpenAIPassthroughOAuthBody_ArrayInputUnchanged(t *testing.T) {
	body := []byte(`{"model":"gpt-5.4","input":[{"type":"message","role":"user","content":"hi"}]}`)

	normalized, _, err := normalizeOpenAIPassthroughOAuthBody(body, false)
	require.NoError(t, err)

	input := gjson.GetBytes(normalized, "input")
	require.True(t, input.IsArray())
	require.Len(t, input.Array(), 1)
	require.Equal(t, "message", input.Array()[0].Get("type").String())
}

func TestNormalizeOpenAIPassthroughOAuthBody_RemovesPreviousResponseID(t *testing.T) {
	body := []byte(`{"model":"gpt-5.6-sol","previous_response_id":"resp_previous","input":[{"type":"message","role":"user","content":"follow up"}]}`)

	normalized, changed, err := normalizeOpenAIPassthroughOAuthBody(body, false)
	require.NoError(t, err)
	require.True(t, changed)
	require.False(t, gjson.GetBytes(normalized, "previous_response_id").Exists())
	require.True(t, gjson.GetBytes(normalized, "input").IsArray())
}

func TestNormalizeOpenAIPassthroughOAuthBody_RemovesEncryptedReasoningWithPreviousResponseID(t *testing.T) {
	body := []byte(`{"model":"gpt-5.6-sol","previous_response_id":"resp_previous","input":[{"type":"reasoning","id":"rs_previous","encrypted_content":"ciphertext","summary":[{"type":"summary_text","text":"discarded with its invalid anchor"}]},{"type":"message","role":"user","content":"keep this follow up"}]}`)

	normalized, changed, err := normalizeOpenAIPassthroughOAuthBody(body, false)
	require.NoError(t, err)
	require.True(t, changed)
	require.False(t, gjson.GetBytes(normalized, "previous_response_id").Exists())
	require.Len(t, gjson.GetBytes(normalized, "input").Array(), 1)
	require.Equal(t, "message", gjson.GetBytes(normalized, "input.0.type").String())
	require.Equal(t, "keep this follow up", gjson.GetBytes(normalized, "input.0.content").String())
}

func TestNormalizeOpenAIPassthroughOAuthBody_RemovesEncryptedCompactionWithPreviousResponseID(t *testing.T) {
	body := []byte(`{"model":"gpt-5.6-sol","previous_response_id":"resp_previous","input":[{"type":"compaction","id":"cmp_previous","encrypted_content":"ciphertext"},{"type":"compaction_summary","id":"cmp_summary_previous","encrypted_content":"ciphertext"},{"type":"message","role":"user","content":"keep this follow up"}]}`)

	normalized, changed, err := normalizeOpenAIPassthroughOAuthBody(body, false)
	require.NoError(t, err)
	require.True(t, changed)
	require.False(t, gjson.GetBytes(normalized, "previous_response_id").Exists())
	require.Len(t, gjson.GetBytes(normalized, "input").Array(), 1)
	require.Equal(t, "message", gjson.GetBytes(normalized, "input.0.type").String())
	require.Equal(t, "keep this follow up", gjson.GetBytes(normalized, "input.0.content").String())
	require.NotContains(t, string(normalized), "compaction")
}

func TestNormalizeOpenAIPassthroughOAuthBody_StripsLegacyResponseMessageIDsWithPreviousResponseID(t *testing.T) {
	body := []byte(`{"model":"gpt-5.6-sol","previous_response_id":"resp_previous","input":[{"type":"message","id":"resp_legacy_msg","role":"assistant","content":"retain assistant history"},{"type":"message","id":"msg_valid","role":"user","content":"retain user history"},{"type":"function_call","id":"call_valid","name":"lookup","arguments":"{}"}]}`)

	normalized, changed, err := normalizeOpenAIPassthroughOAuthBody(body, false)
	require.NoError(t, err)
	require.True(t, changed)
	require.False(t, gjson.GetBytes(normalized, "previous_response_id").Exists())
	require.False(t, gjson.GetBytes(normalized, "input.0.id").Exists(), "legacy response output ID must not be sent as an input message ID")
	require.Equal(t, "retain assistant history", gjson.GetBytes(normalized, "input.0.content").String())
	require.Equal(t, "msg_valid", gjson.GetBytes(normalized, "input.1.id").String(), "valid message IDs must be preserved")
	require.Equal(t, "call_valid", gjson.GetBytes(normalized, "input.2.id").String(), "tool-call IDs must be preserved")
}

func TestNormalizeOpenAIPassthroughOAuthBody_StripsLegacyMessageIDsWithoutPreviousResponseID(t *testing.T) {
	body := []byte(`{"model":"gpt-5.6-sol","input":[{"type":"message","id":"resp_legacy_msg","role":"assistant","content":"do not rewrite independent requests"}]}`)

	normalized, changed, err := normalizeOpenAIPassthroughOAuthBody(body, false)
	require.NoError(t, err)
	require.True(t, changed)
	require.False(t, gjson.GetBytes(normalized, "input.0.id").Exists())
	require.Equal(t, "do not rewrite independent requests", gjson.GetBytes(normalized, "input.0.content").String())
}

func TestDetectOpenAIPassthroughInstructionsRejectReason(t *testing.T) {
	for _, tt := range []struct {
		name string
		body string
		want string
	}{
		{name: "missing is optional", body: `{"model":"gpt-5.1-codex"}`, want: ""},
		{name: "non string remains rejected", body: `{"instructions":{"text":"invalid"}}`, want: "instructions_not_string"},
		{name: "empty remains rejected", body: `{"instructions":"  "}`, want: "instructions_empty"},
		{name: "non empty remains accepted", body: `{"instructions":"client guidance"}`, want: ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, detectOpenAIPassthroughInstructionsRejectReason("gpt-5.1-codex", []byte(tt.body)))
		})
	}
}

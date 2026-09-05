package service

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCollectOpenAIEncryptedContentDigestsRaw(t *testing.T) {
	t.Parallel()

	digests := collectOpenAIEncryptedContentDigestsRaw([]byte(`{"input":[
		{"type":"reasoning","encrypted_content":"cipher-a"},
		{"type":"compaction","encrypted_content":"cipher-b"},
		{"type":"message","encrypted_content":"not-sanitizable"},
		{"type":"input_text","text":"hello"}
	]}`))
	require.Equal(t, []string{
		openAIEncryptedContentDigest("cipher-a"),
		openAIEncryptedContentDigest("cipher-b"),
	}, digests)
	require.Nil(t, collectOpenAIEncryptedContentDigestsRaw([]byte(`{"model":"gpt-5"}`)))
}

func TestStripOpenAIInvalidEncryptedContentRawPreservesFreshItems(t *testing.T) {
	t.Parallel()

	invalid := map[string]struct{}{openAIEncryptedContentDigest("stale-cipher"): {}}
	payload := []byte(`{"model":"gpt-5","input":[
		{"type":"reasoning","id":"rs_1","encrypted_content":"stale-cipher","summary":[]},
		{"type":"reasoning","id":"rs_2","encrypted_content":"fresh-cipher"},
		{"type":"compaction","encrypted_content":"stale-cipher"},
		{"type":"input_text","text":"hello"}
	]}`)

	stripped, count, err := stripOpenAIInvalidEncryptedContentRaw(payload, invalid)
	require.NoError(t, err)
	require.Equal(t, 2, count)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(stripped, &decoded))
	input := decoded["input"].([]any)
	require.Len(t, input, 3, "matching compaction should be removed entirely")
	first := input[0].(map[string]any)
	require.Equal(t, "rs_1", first["id"])
	require.NotContains(t, first, "encrypted_content")
	require.Equal(t, "fresh-cipher", input[1].(map[string]any)["encrypted_content"])

	cleanPayload := []byte(`{"input":[{"type":"reasoning","encrypted_content":"fresh-cipher"}]}`)
	unchanged, count, err := stripOpenAIInvalidEncryptedContentRaw(cleanPayload, invalid)
	require.NoError(t, err)
	require.Zero(t, count)
	require.Same(t, &cleanPayload[0], &unchanged[0], "cache miss must retain the immutable payload body")
}

func TestStripOpenAIInvalidEncryptedContentReplayKeepsImmutableBodies(t *testing.T) {
	t.Parallel()

	invalid := map[string]struct{}{openAIEncryptedContentDigest("stale-cipher"): {}}
	stale := json.RawMessage(`{"type":"reasoning","id":"rs_1","encrypted_content":"stale-cipher"}`)
	clean := json.RawMessage(`{"type":"input_text","text":"hello"}`)
	items := []json.RawMessage{stale, clean}

	next, stripped := stripOpenAIInvalidEncryptedContentFromReplayItems(items, invalid)
	require.Equal(t, 1, stripped)
	require.Len(t, next, 2)
	require.Contains(t, string(stale), "stale-cipher", "the original replay body must never be mutated")
	require.NotContains(t, string(next[0]), "stale-cipher")
	require.Same(t, &clean[0], &next[1][0], "unchanged immutable bodies should remain shared")
	next[1] = json.RawMessage(`{"type":"input_text","text":"replacement"}`)
	require.Equal(t, `{"type":"input_text","text":"hello"}`, string(items[1]), "slice headers must remain independent")
}

func TestOpenAIWSStateStoreInvalidEncryptedContentLineageIsolation(t *testing.T) {
	t.Parallel()

	store := NewOpenAIWSStateStore(nil)
	require.False(t, store.HasAnySessionInvalidEncryptedContent())
	store.MarkSessionInvalidEncryptedContent(7, 101, "session-a", []string{"d1", "d2"}, time.Minute)
	store.MarkSessionInvalidEncryptedContent(7, 101, "session-a", []string{"d2", "d3"}, time.Minute)

	digests := store.GetSessionInvalidEncryptedContentDigests(7, 101, "session-a")
	require.Len(t, digests, 3)
	require.Nil(t, store.GetSessionInvalidEncryptedContentDigests(8, 101, "session-a"), "groups must be isolated")
	require.Nil(t, store.GetSessionInvalidEncryptedContentDigests(7, 202, "session-a"), "accounts must be isolated")
	require.Nil(t, store.GetSessionInvalidEncryptedContentDigests(7, 101, "session-b"), "sessions must be isolated")

	digests["caller-mutation"] = struct{}{}
	require.Len(t, store.GetSessionInvalidEncryptedContentDigests(7, 101, "session-a"), 3, "callers receive a copy")

	restartedStore := NewOpenAIWSStateStore(nil)
	require.Nil(t, restartedStore.GetSessionInvalidEncryptedContentDigests(7, 101, "session-a"), "node restart must degrade to a cache miss")
}

func TestOpenAIWSStateStoreInvalidEncryptedContentLineageExpiryAndCapacity(t *testing.T) {
	t.Parallel()

	raw := NewOpenAIWSStateStore(nil)
	store := raw.(*defaultOpenAIWSStateStore)
	store.MarkSessionInvalidEncryptedContent(1, 44, "session-expiry", []string{"d1"}, time.Minute)
	key := openAIWSInvalidEncryptedContentKey(1, 44, "session-expiry")
	store.sessionInvalidEncryptedMu.Lock()
	binding := store.sessionInvalidEncrypted[key]
	binding.expiresAt = time.Now().Add(-time.Second)
	store.sessionInvalidEncrypted[key] = binding
	store.sessionInvalidEncryptedMu.Unlock()
	require.Nil(t, store.GetSessionInvalidEncryptedContentDigests(1, 44, "session-expiry"))

	oversized := make([]string, 0, openAIWSInvalidEncryptedDigestsPerSession+10)
	for i := 0; i < openAIWSInvalidEncryptedDigestsPerSession+10; i++ {
		oversized = append(oversized, openAIEncryptedContentDigest(fmt.Sprintf("cipher-%d", i)))
	}
	store.MarkSessionInvalidEncryptedContent(1, 44, "session-capacity", oversized, time.Minute)
	require.Len(t, store.GetSessionInvalidEncryptedContentDigests(1, 44, "session-capacity"), openAIWSInvalidEncryptedDigestsPerSession)
}

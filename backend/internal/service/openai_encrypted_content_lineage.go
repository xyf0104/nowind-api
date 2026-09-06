package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const openAIWSFallbackReasonInvalidEncryptedContent = "invalid_encrypted_content"

// The ingress session hash is captured before retries rewrite the payload.
const openAIWSIngressSessionHashContextKey = "openai_ws_ingress_session_hash"

func openAIEncryptedContentDigest(encrypted string) string {
	sum := sha256.Sum256([]byte(encrypted))
	return hex.EncodeToString(sum[:])
}

func openAIEncryptedLineageItemType(itemType string) bool {
	switch strings.TrimSpace(itemType) {
	case "reasoning", "compaction", "compaction_summary":
		return true
	default:
		return false
	}
}

// collectOpenAIEncryptedContentDigestsRaw reads immutable replay payloads without
// copying their bodies. Only item kinds that the recovery path can sanitize are
// remembered.
func collectOpenAIEncryptedContentDigestsRaw(payload []byte) []string {
	if len(payload) == 0 {
		return nil
	}
	input := gjson.Get(openAIWSPayloadStringView(payload), "input")
	if !input.Exists() {
		return nil
	}
	var digests []string
	appendItem := func(item gjson.Result) {
		if !openAIEncryptedLineageItemType(item.Get("type").String()) {
			return
		}
		encrypted := item.Get("encrypted_content")
		if encrypted.Type == gjson.String && encrypted.String() != "" {
			digests = append(digests, openAIEncryptedContentDigest(encrypted.String()))
		}
	}
	if input.IsArray() {
		input.ForEach(func(_, item gjson.Result) bool {
			appendItem(item)
			return true
		})
		return digests
	}
	if input.IsObject() {
		appendItem(input)
	}
	return digests
}

func stripOpenAIInvalidEncryptedContentItems(reqBody map[string]any, invalid map[string]struct{}) int {
	if len(reqBody) == 0 || len(invalid) == 0 {
		return 0
	}
	inputValue, has := reqBody["input"]
	if !has {
		return 0
	}
	stripped := 0
	stripItem := func(item any) (next any, changed bool, keep bool) {
		inputItem, ok := item.(map[string]any)
		if !ok {
			return item, false, true
		}
		encrypted, ok := inputItem["encrypted_content"].(string)
		if !ok || encrypted == "" {
			return item, false, true
		}
		if _, hit := invalid[openAIEncryptedContentDigest(encrypted)]; !hit {
			return item, false, true
		}
		return sanitizeEncryptedReasoningInputItem(item)
	}
	switch input := inputValue.(type) {
	case []any:
		filtered := input[:0]
		for _, item := range input {
			nextItem, changed, keep := stripItem(item)
			if changed {
				stripped++
			}
			if keep {
				filtered = append(filtered, nextItem)
			}
		}
		if stripped == 0 {
			return 0
		}
		if len(filtered) == 0 {
			delete(reqBody, "input")
		} else {
			reqBody["input"] = filtered
		}
		return stripped
	case map[string]any:
		nextItem, changed, keep := stripItem(input)
		if !changed {
			return 0
		}
		if !keep {
			delete(reqBody, "input")
			return 1
		}
		nextMap, ok := nextItem.(map[string]any)
		if !ok {
			return 0
		}
		reqBody["input"] = nextMap
		return 1
	default:
		return 0
	}
}

func openAIItemHasInvalidEncryptedCompaction(item gjson.Result, invalid map[string]struct{}) bool {
	if !isOpenAIEncryptedCompactionRaw(item) {
		return false
	}
	encrypted := item.Get("encrypted_content")
	if encrypted.Type != gjson.String || encrypted.String() == "" {
		return false
	}
	_, hit := invalid[openAIEncryptedContentDigest(encrypted.String())]
	return hit
}

func openAIRawPayloadHasInvalidEncryptedCompaction(payload []byte, invalid map[string]struct{}) bool {
	input := gjson.GetBytes(payload, "input")
	if input.IsObject() {
		return openAIItemHasInvalidEncryptedCompaction(input, invalid)
	}
	found := false
	if input.IsArray() {
		input.ForEach(func(_, item gjson.Result) bool {
			found = openAIItemHasInvalidEncryptedCompaction(item, invalid)
			return !found
		})
	}
	return found
}

func openAIReplayHasInvalidEncryptedCompaction(items []json.RawMessage, invalid map[string]struct{}) bool {
	for _, item := range items {
		if openAIItemHasInvalidEncryptedCompaction(gjson.ParseBytes(item), invalid) {
			return true
		}
	}
	return false
}

func openAIRawPayloadHasInvalidEncryptedContent(payload []byte, invalid map[string]struct{}) bool {
	if len(payload) == 0 || len(invalid) == 0 {
		return false
	}
	input := gjson.Get(openAIWSPayloadStringView(payload), "input")
	if !input.Exists() {
		return false
	}
	checkItem := func(item gjson.Result) bool {
		encrypted := item.Get("encrypted_content")
		if encrypted.Type != gjson.String || encrypted.String() == "" {
			return false
		}
		_, matched := invalid[openAIEncryptedContentDigest(encrypted.String())]
		return matched
	}
	if input.IsArray() {
		hit := false
		input.ForEach(func(_, item gjson.Result) bool {
			if checkItem(item) {
				hit = true
				return false
			}
			return true
		})
		return hit
	}
	return input.IsObject() && checkItem(input)
}

// A changed replay item gets a new immutable body. Unchanged item bodies remain
// shared with the existing replay history.
func stripOpenAIInvalidEncryptedContentFromReplayItems(items []json.RawMessage, invalid map[string]struct{}) ([]json.RawMessage, int) {
	if len(items) == 0 || len(invalid) == 0 || openAIReplayHasInvalidEncryptedCompaction(items, invalid) {
		return items, 0
	}
	hit := false
	for _, item := range items {
		encrypted := gjson.Get(openAIWSPayloadStringView(item), "encrypted_content")
		if encrypted.Type == gjson.String && encrypted.String() != "" {
			if _, matched := invalid[openAIEncryptedContentDigest(encrypted.String())]; matched {
				hit = true
				break
			}
		}
	}
	if !hit {
		return items, 0
	}

	stripped := 0
	next := make([]json.RawMessage, 0, len(items))
	for _, item := range items {
		var decoded map[string]any
		if err := decodeOpenAIJSONUseNumber(item, &decoded); err != nil {
			next = append(next, item)
			continue
		}
		encrypted, ok := decoded["encrypted_content"].(string)
		if !ok || encrypted == "" {
			next = append(next, item)
			continue
		}
		if _, matched := invalid[openAIEncryptedContentDigest(encrypted)]; !matched {
			next = append(next, item)
			continue
		}
		nextItem, changed, keep := sanitizeEncryptedReasoningInputItem(decoded)
		if !changed {
			next = append(next, item)
			continue
		}
		stripped++
		if !keep {
			continue
		}
		rebuilt, err := marshalOpenAIUpstreamJSON(nextItem)
		if err != nil {
			next = append(next, item)
			stripped--
			continue
		}
		next = append(next, json.RawMessage(rebuilt))
	}
	if stripped == 0 {
		return items, 0
	}
	return next, stripped
}

func stripOpenAIInvalidEncryptedContentRaw(payload []byte, invalid map[string]struct{}) ([]byte, int, error) {
	if openAIRawPayloadHasInvalidEncryptedCompaction(payload, invalid) {
		return payload, 0, ErrOpenAIContextUnavailable
	}
	if !openAIRawPayloadHasInvalidEncryptedContent(payload, invalid) {
		return payload, 0, nil
	}
	var decoded map[string]any
	if err := decodeOpenAIJSONUseNumber(payload, &decoded); err != nil {
		return payload, 0, err
	}
	stripped := stripOpenAIInvalidEncryptedContentItems(decoded, invalid)
	if stripped == 0 {
		return payload, 0, nil
	}
	rebuilt, err := marshalOpenAIUpstreamJSON(decoded)
	if err != nil {
		return payload, 0, err
	}
	return rebuilt, stripped, nil
}

func (s *OpenAIGatewayService) markOpenAIWSInvalidEncryptedContentLineage(groupID, accountID int64, sessionHash string, digests []string) {
	if s == nil || accountID <= 0 || len(digests) == 0 || strings.TrimSpace(sessionHash) == "" {
		return
	}
	if stateStore := s.getOpenAIWSStateStore(); stateStore != nil {
		stateStore.MarkSessionInvalidEncryptedContent(groupID, accountID, sessionHash, digests, s.openAIWSSessionStickyTTL())
	}
}

func (s *OpenAIGatewayService) sessionInvalidEncryptedContentDigests(groupID, accountID int64, sessionHash string) map[string]struct{} {
	if s == nil || accountID <= 0 || strings.TrimSpace(sessionHash) == "" {
		return nil
	}
	stateStore := s.getOpenAIWSStateStore()
	if stateStore == nil || !stateStore.HasAnySessionInvalidEncryptedContent() {
		return nil
	}
	return stateStore.GetSessionInvalidEncryptedContentDigests(groupID, accountID, sessionHash)
}

func (s *OpenAIGatewayService) openAIWSLineageSessionHashFromContext(c *gin.Context, body []byte) string {
	if c != nil {
		if fromCtx := strings.TrimSpace(c.GetString(openAIWSIngressSessionHashContextKey)); fromCtx != "" {
			return fromCtx
		}
	}
	return s.GenerateSessionHash(c, body)
}

func (s *OpenAIGatewayService) markOpenAIWSInvalidEncryptedContentLineageFromPayload(c *gin.Context, payload []byte, logKey string, accountID int64, turn int) {
	digests := collectOpenAIEncryptedContentDigestsRaw(payload)
	if len(digests) == 0 {
		return
	}
	s.markOpenAIWSInvalidEncryptedContentLineage(
		getOpenAIGroupIDFromContext(c),
		accountID,
		s.openAIWSLineageSessionHashFromContext(c, payload),
		digests,
	)
	logOpenAIWSModeInfo("%s account_id=%d turn=%d digests=%d", logKey, accountID, turn, len(digests))
}

func (s *OpenAIGatewayService) stripSessionInvalidEncryptedContentLogged(payload []byte, invalid map[string]struct{}, logKey string, accountID int64, turn int) ([]byte, int) {
	strippedPayload, strippedCount, err := stripOpenAIInvalidEncryptedContentRaw(payload, invalid)
	if err != nil {
		logOpenAIWSModeInfo(
			"%s_skip account_id=%d turn=%d reason=strip_error cause=%s",
			logKey,
			accountID,
			turn,
			truncateOpenAIWSLogValue(err.Error(), openAIWSLogValueMaxLen),
		)
		return payload, 0
	}
	if strippedCount > 0 {
		logOpenAIWSModeInfo("%s account_id=%d turn=%d stripped_items=%d", logKey, accountID, turn, strippedCount)
	}
	return strippedPayload, strippedCount
}

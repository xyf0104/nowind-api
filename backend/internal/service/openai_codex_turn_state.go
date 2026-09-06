package service

import (
	"maps"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// Process-local, expiring snapshots indexed by the exact opaque state's digest.
// This is not a distributed ownership ledger. Unknown/expired values keep the
// existing pass-through policy; a later response cannot relabel a known value.
type openAICodexTurnStateOrigin struct {
	owners map[openAIWSOwnership]time.Time
}

// relayOpenAICodexTurnState publishes the state from the selected upstream
// attempt and records its account only after that attempt is committed.
func (s *OpenAIGatewayService) relayOpenAICodexTurnState(c *gin.Context, account *Account, upstream http.Header) {
	if c == nil || c.Writer == nil {
		return
	}
	canonical := http.CanonicalHeaderKey(openAIWSTurnStateHeader)
	state := extractOpenAICodexTurnState(upstream)
	if state == "" {
		c.Writer.Header().Del(canonical)
		return
	}
	c.Writer.Header().Set(canonical, state)
	s.noteOpenAICodexTurnStateProvenance(c, account, state)
}

func stageOpenAICodexTurnState(dst *http.Header, upstream http.Header) {
	if dst == nil {
		return
	}
	canonical := http.CanonicalHeaderKey(openAIWSTurnStateHeader)
	state := extractOpenAICodexTurnState(upstream)
	if state == "" {
		if *dst != nil {
			dst.Del(canonical)
		}
		return
	}
	if *dst == nil {
		*dst = http.Header{}
	}
	dst.Set(canonical, state)
}

func (s *OpenAIGatewayService) noteStagedOpenAICodexTurnStateCommitted(c *gin.Context, account *Account, staged http.Header) {
	if staged == nil || strings.TrimSpace(staged.Get(openAIWSTurnStateHeader)) == "" {
		return
	}
	s.noteOpenAICodexTurnStateProvenance(c, account, extractOpenAICodexTurnState(staged))
}

func extractOpenAICodexTurnState(upstream http.Header) string {
	if upstream == nil {
		return ""
	}
	return openAIHeaderValueEqualFold(upstream, openAIWSTurnStateHeader)
}

func openAIHeaderValueEqualFold(headers http.Header, name string) string {
	for key, values := range headers {
		if !strings.EqualFold(strings.TrimSpace(key), strings.TrimSpace(name)) {
			continue
		}
		for _, value := range values {
			if value = strings.TrimSpace(value); value != "" {
				return value
			}
		}
	}
	return ""
}

func (s *OpenAIGatewayService) noteOpenAICodexTurnStateProvenance(c *gin.Context, account *Account, state string) {
	if s == nil || account == nil || account.ID <= 0 {
		return
	}
	state = strings.TrimSpace(state)
	if state == "" || c == nil {
		return
	}
	key := openAIOwnershipDigest("turn-state-v1", state)
	owner := openAIWSOwnershipForRequest(c, account)
	now := time.Now()
	for {
		fresh := &openAICodexTurnStateOrigin{owners: map[openAIWSOwnership]time.Time{owner: now.Add(s.openAIWSSessionStickyTTL())}}
		raw, loaded := s.openaiCodexTurnStateOrigins.LoadOrStore(key, fresh)
		if !loaded {
			break
		}
		previous, ok := raw.(*openAICodexTurnStateOrigin)
		if !ok {
			s.openaiCodexTurnStateOrigins.Delete(key)
			continue
		}
		next := &openAICodexTurnStateOrigin{owners: maps.Clone(previous.owners)}
		for scope, expiresAt := range next.owners {
			if !now.Before(expiresAt) {
				delete(next.owners, scope)
			}
		}
		// An upstream may legitimately return the same opaque value to multiple
		// owners. Record explicit grants without allowing unbounded per-value maps.
		if _, exists := next.owners[owner]; exists || len(next.owners) < 256 {
			next.owners[owner] = fresh.owners[owner]
		}
		if s.openaiCodexTurnStateOrigins.CompareAndSwap(key, previous, next) {
			break
		}
	}
	s.sweepOpenAICodexTurnStateOrigins()
}

// guardOpenAICodexTurnStateEcho strips a client-returned turn state only when
// it is known but has no grant for this exact client/conversation/upstream.
// Unknown values remain untouched for externally established sessions.
func (s *OpenAIGatewayService) guardOpenAICodexTurnStateEcho(c *gin.Context, account *Account, h http.Header) {
	if s == nil || h == nil || account == nil || openAIHeaderValueEqualFold(h, openAIWSTurnStateHeader) == "" {
		return
	}
	key := openAIOwnershipDigest("turn-state-v1", openAIHeaderValueEqualFold(h, openAIWSTurnStateHeader))
	raw, ok := s.openaiCodexTurnStateOrigins.Load(key)
	if !ok {
		return
	}
	origin, ok := raw.(*openAICodexTurnStateOrigin)
	if !ok {
		s.openaiCodexTurnStateOrigins.Delete(key)
		return
	}
	now := time.Now()
	if expiresAt := origin.owners[openAIWSOwnershipForRequest(c, account)]; now.Before(expiresAt) {
		return
	}
	for _, expiresAt := range origin.owners {
		if now.Before(expiresAt) {
			deleteOpenAIHeaderEqualFold(h, openAIWSTurnStateHeader)
			return
		}
	}
	s.openaiCodexTurnStateOrigins.CompareAndDelete(key, origin)
}

func (s *OpenAIGatewayService) sweepOpenAICodexTurnStateOrigins() {
	if s.openaiCodexTurnStateWrites.Add(1)%256 != 0 {
		return
	}
	now := time.Now()
	retained := 0
	s.openaiCodexTurnStateOrigins.Range(func(key, value any) bool {
		origin, ok := value.(*openAICodexTurnStateOrigin)
		if ok {
			for _, expiresAt := range origin.owners {
				if now.Before(expiresAt) && retained < openAIWSStateStoreMaxEntriesPerMap {
					retained++
					return true
				}
			}
			s.openaiCodexTurnStateOrigins.CompareAndDelete(key, origin)
		} else {
			s.openaiCodexTurnStateOrigins.Delete(key)
		}
		return true
	})
}

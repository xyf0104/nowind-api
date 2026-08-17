package service

import (
	"context"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
)

var (
	ErrPublicOpenAIOAuthStateMismatch          = errors.New("public oauth state mismatch")
	ErrPublicOpenAIOAuthBrowserBindingMismatch = errors.New("public oauth browser binding mismatch")
)

// PublicOpenAIOAuthSessionStore persists one-shot public PKCE transactions.
// Access and refresh tokens are never stored here.
type PublicOpenAIOAuthSessionStore interface {
	Store(ctx context.Context, sessionID string, session *openai.OAuthSession) error
	Consume(ctx context.Context, sessionID, state, browserBindingHash string) (*openai.OAuthSession, bool, error)
}

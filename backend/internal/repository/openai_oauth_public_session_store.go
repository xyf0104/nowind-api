package repository

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

const publicOpenAIOAuthSessionKeyPrefix = "oauth:public:openai:"

type redisPublicOpenAIOAuthSessionStore struct {
	redis *redis.Client
}

func NewRedisPublicOpenAIOAuthSessionStore(redisClient *redis.Client) service.PublicOpenAIOAuthSessionStore {
	return &redisPublicOpenAIOAuthSessionStore{redis: redisClient}
}

func (s *redisPublicOpenAIOAuthSessionStore) Store(ctx context.Context, sessionID string, session *openai.OAuthSession) error {
	if s == nil || s.redis == nil {
		return errors.New("public oauth session store is unavailable")
	}
	if strings.TrimSpace(sessionID) == "" || session == nil {
		return errors.New("public oauth session is invalid")
	}
	payload, err := json.Marshal(session)
	if err != nil {
		return fmt.Errorf("encode public oauth session: %w", err)
	}
	if err := s.redis.Set(ctx, publicOpenAIOAuthSessionKeyPrefix+sessionID, payload, openai.SessionTTL).Err(); err != nil {
		return fmt.Errorf("store public oauth session: %w", err)
	}
	return nil
}

func (s *redisPublicOpenAIOAuthSessionStore) Consume(ctx context.Context, sessionID, state, browserBindingHash string) (*openai.OAuthSession, bool, error) {
	if s == nil || s.redis == nil {
		return nil, false, errors.New("public oauth session store is unavailable")
	}
	key := publicOpenAIOAuthSessionKeyPrefix + sessionID
	var consumed *openai.OAuthSession
	var found bool
	err := s.redis.Watch(ctx, func(tx *redis.Tx) error {
		payload, err := tx.Get(ctx, key).Bytes()
		if errors.Is(err, redis.Nil) {
			return nil
		}
		if err != nil {
			return err
		}

		var session openai.OAuthSession
		if err := json.Unmarshal(payload, &session); err != nil {
			return fmt.Errorf("decode public oauth session: %w", err)
		}
		if time.Since(session.CreatedAt) > openai.SessionTTL {
			_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
				pipe.Del(ctx, key)
				return nil
			})
			return err
		}
		if subtle.ConstantTimeCompare([]byte(state), []byte(session.State)) != 1 {
			return service.ErrPublicOpenAIOAuthStateMismatch
		}
		if session.BrowserBindingHash != "" && subtle.ConstantTimeCompare(
			[]byte(strings.TrimSpace(browserBindingHash)),
			[]byte(session.BrowserBindingHash),
		) != 1 {
			return service.ErrPublicOpenAIOAuthBrowserBindingMismatch
		}

		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.Del(ctx, key)
			return nil
		})
		if err == nil {
			consumed = &session
			found = true
		}
		return err
	}, key)
	if err != nil {
		return nil, false, fmt.Errorf("consume public oauth session: %w", err)
	}
	return consumed, found, nil
}

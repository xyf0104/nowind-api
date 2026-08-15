package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

const (
	totpSetupKeyPrefix    = "totp:setup:"
	totpLoginKeyPrefix    = "totp:login:"
	totpAttemptsKeyPrefix = "totp:attempts:"
	totpStepUpKeyPrefix   = "totp:stepup:"
)

// TotpCache implements service.TotpCache using Redis
type TotpCache struct {
	rdb *redis.Client
}

var consumeTotpLoginSessionScript = redis.NewScript(`
local value = redis.call("GET", KEYS[1])
if not value then
    return nil
end
redis.call("DEL", KEYS[1])
return value
`)

var reserveTotpVerifyAttemptScript = redis.NewScript(`
local current = tonumber(redis.call("GET", KEYS[1]) or "0")
if current >= tonumber(ARGV[1]) then
    return 0
end
local count = redis.call("INCR", KEYS[1])
redis.call("PEXPIRE", KEYS[1], ARGV[2])
return count
`)

// NewTotpCache creates a new TOTP cache
func NewTotpCache(rdb *redis.Client) service.TotpCache {
	return &TotpCache{rdb: rdb}
}

// GetSetupSession retrieves a TOTP setup session
func (c *TotpCache) GetSetupSession(ctx context.Context, userID int64) (*service.TotpSetupSession, error) {
	key := fmt.Sprintf("%s%d", totpSetupKeyPrefix, userID)
	data, err := c.rdb.Get(ctx, key).Bytes()
	if err != nil {
		if err == redis.Nil {
			return nil, nil
		}
		return nil, fmt.Errorf("get setup session: %w", err)
	}

	var session service.TotpSetupSession
	if err := json.Unmarshal(data, &session); err != nil {
		return nil, fmt.Errorf("unmarshal setup session: %w", err)
	}

	return &session, nil
}

// SetSetupSession stores a TOTP setup session
func (c *TotpCache) SetSetupSession(ctx context.Context, userID int64, session *service.TotpSetupSession, ttl time.Duration) error {
	key := fmt.Sprintf("%s%d", totpSetupKeyPrefix, userID)
	data, err := json.Marshal(session)
	if err != nil {
		return fmt.Errorf("marshal setup session: %w", err)
	}

	if err := c.rdb.Set(ctx, key, data, ttl).Err(); err != nil {
		return fmt.Errorf("set setup session: %w", err)
	}

	return nil
}

// DeleteSetupSession deletes a TOTP setup session
func (c *TotpCache) DeleteSetupSession(ctx context.Context, userID int64) error {
	key := fmt.Sprintf("%s%d", totpSetupKeyPrefix, userID)
	return c.rdb.Del(ctx, key).Err()
}

// GetLoginSession retrieves a TOTP login session
func (c *TotpCache) GetLoginSession(ctx context.Context, tempToken string) (*service.TotpLoginSession, error) {
	key := totpLoginKeyPrefix + tempToken
	data, err := c.rdb.Get(ctx, key).Bytes()
	if err != nil {
		if err == redis.Nil {
			return nil, nil
		}
		return nil, fmt.Errorf("get login session: %w", err)
	}

	var session service.TotpLoginSession
	if err := json.Unmarshal(data, &session); err != nil {
		return nil, fmt.Errorf("unmarshal login session: %w", err)
	}

	return &session, nil
}

// ConsumeLoginSession atomically retrieves and deletes a pending 2FA login session.
func (c *TotpCache) ConsumeLoginSession(ctx context.Context, tempToken string) (*service.TotpLoginSession, error) {
	key := totpLoginKeyPrefix + tempToken
	result, err := consumeTotpLoginSessionScript.Run(ctx, c.rdb, []string{key}).Result()
	if err != nil {
		if err == redis.Nil {
			return nil, nil
		}
		return nil, fmt.Errorf("consume login session: %w", err)
	}
	data, ok := result.(string)
	if !ok {
		return nil, fmt.Errorf("consume login session returned %T", result)
	}

	var session service.TotpLoginSession
	if err := json.Unmarshal([]byte(data), &session); err != nil {
		return nil, fmt.Errorf("unmarshal consumed login session: %w", err)
	}
	return &session, nil
}

// SetLoginSession stores a TOTP login session
func (c *TotpCache) SetLoginSession(ctx context.Context, tempToken string, session *service.TotpLoginSession, ttl time.Duration) error {
	key := totpLoginKeyPrefix + tempToken
	data, err := json.Marshal(session)
	if err != nil {
		return fmt.Errorf("marshal login session: %w", err)
	}

	if err := c.rdb.Set(ctx, key, data, ttl).Err(); err != nil {
		return fmt.Errorf("set login session: %w", err)
	}

	return nil
}

// DeleteLoginSession deletes a TOTP login session
func (c *TotpCache) DeleteLoginSession(ctx context.Context, tempToken string) error {
	key := totpLoginKeyPrefix + tempToken
	return c.rdb.Del(ctx, key).Err()
}

// ReserveVerifyAttempt atomically reserves one verification attempt below maxAttempts.
func (c *TotpCache) ReserveVerifyAttempt(ctx context.Context, userID int64, maxAttempts int, ttl time.Duration) (bool, error) {
	key := fmt.Sprintf("%s%d", totpAttemptsKeyPrefix, userID)
	if maxAttempts <= 0 || ttl <= 0 {
		return false, nil
	}
	count, err := reserveTotpVerifyAttemptScript.Run(
		ctx,
		c.rdb,
		[]string{key},
		maxAttempts,
		ttl.Milliseconds(),
	).Int64()
	if err != nil {
		return false, fmt.Errorf("reserve verify attempt: %w", err)
	}
	return count > 0, nil
}

// ClearVerifyAttempts clears the verify attempt counter
func (c *TotpCache) ClearVerifyAttempts(ctx context.Context, userID int64) error {
	key := fmt.Sprintf("%s%d", totpAttemptsKeyPrefix, userID)
	return c.rdb.Del(ctx, key).Err()
}

func totpStepUpKey(userID int64, sessionKey string) string {
	return fmt.Sprintf("%s%d:%s", totpStepUpKeyPrefix, userID, sessionKey)
}

// SetStepUpGrant 记录一次 step-up 验证通过（sudo 窗口），绑定用户+会话。
func (c *TotpCache) SetStepUpGrant(ctx context.Context, userID int64, sessionKey string, ttl time.Duration) error {
	return c.rdb.Set(ctx, totpStepUpKey(userID, sessionKey), "1", ttl).Err()
}

// HasStepUpGrant 检查 step-up 授权是否仍在有效期内。
func (c *TotpCache) HasStepUpGrant(ctx context.Context, userID int64, sessionKey string) (bool, error) {
	_, err := c.rdb.Get(ctx, totpStepUpKey(userID, sessionKey)).Result()
	if err != nil {
		if err == redis.Nil {
			return false, nil
		}
		return false, fmt.Errorf("get step-up grant: %w", err)
	}
	return true, nil
}

package service

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"sync"
	"time"
)

type grokTeamModelRateLimit struct {
	Until time.Time
}

type grokTeamModelRateLimitStore struct {
	mu    sync.Mutex
	items map[string]grokTeamModelRateLimit
}

var globalGrokTeamModelRateLimits = &grokTeamModelRateLimitStore{items: make(map[string]grokTeamModelRateLimit)}

const (
	grokTeamRateLimitDefaultTTL = 10 * time.Minute
	grokTeamRateLimitMaxTTL     = time.Hour
	grokTeamRateLimitMinTTL     = 30 * time.Second
)

func grokTeamFingerprint(teamID string) string {
	teamID = strings.TrimSpace(teamID)
	if teamID == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(strings.ToLower(teamID)))
	return hex.EncodeToString(sum[:8])
}

func grokTeamModelRateLimitKey(teamFingerprint, model string) string {
	return teamFingerprint + "|" + strings.ToLower(strings.TrimSpace(model))
}

func accountGrokTeamID(account *Account) string {
	if account == nil {
		return ""
	}
	return strings.TrimSpace(account.GetCredential("team_id"))
}

func markGrokTeamModelRateLimit(account *Account, model string, until time.Time) {
	if account == nil || !account.IsGrokOAuth() {
		return
	}
	fingerprint := grokTeamFingerprint(accountGrokTeamID(account))
	model = strings.TrimSpace(model)
	if fingerprint == "" || model == "" || until.IsZero() {
		return
	}
	now := time.Now()
	if !until.After(now) {
		until = now.Add(grokTeamRateLimitDefaultTTL)
	}
	if max := now.Add(grokTeamRateLimitMaxTTL); until.After(max) {
		until = max
	}
	key := grokTeamModelRateLimitKey(fingerprint, model)
	globalGrokTeamModelRateLimits.mu.Lock()
	defer globalGrokTeamModelRateLimits.mu.Unlock()
	if current, ok := globalGrokTeamModelRateLimits.items[key]; ok && current.Until.After(until) {
		return
	}
	globalGrokTeamModelRateLimits.items[key] = grokTeamModelRateLimit{Until: until}
	for itemKey, item := range globalGrokTeamModelRateLimits.items {
		if !item.Until.After(now) {
			delete(globalGrokTeamModelRateLimits.items, itemKey)
		}
	}
}

func isGrokTeamModelRateLimited(account *Account, model string, now time.Time) bool {
	if account == nil || !account.IsGrokOAuth() {
		return false
	}
	fingerprint := grokTeamFingerprint(accountGrokTeamID(account))
	model = strings.TrimSpace(model)
	if fingerprint == "" || model == "" {
		return false
	}
	key := grokTeamModelRateLimitKey(fingerprint, model)
	globalGrokTeamModelRateLimits.mu.Lock()
	defer globalGrokTeamModelRateLimits.mu.Unlock()
	current, ok := globalGrokTeamModelRateLimits.items[key]
	if !ok {
		return false
	}
	if !current.Until.After(now) {
		delete(globalGrokTeamModelRateLimits.items, key)
		return false
	}
	return true
}

func filterGrokTeamModelRateLimitedAccounts(accounts []Account, model string, now time.Time) []Account {
	if len(accounts) == 0 || strings.TrimSpace(model) == "" {
		return accounts
	}
	out := make([]Account, 0, len(accounts))
	for i := range accounts {
		upstreamModel := canonicalOpenAIAccountSchedulingModel(&accounts[i], model)
		if !isGrokTeamModelRateLimited(&accounts[i], upstreamModel, now) {
			out = append(out, accounts[i])
		}
	}
	return out
}

func resolveGrokTeamRateLimitUntil(resetAt, now time.Time) time.Time {
	if resetAt.After(now.Add(grokTeamRateLimitMinTTL)) {
		if max := now.Add(grokTeamRateLimitMaxTTL); resetAt.After(max) {
			return max
		}
		return resetAt
	}
	return now.Add(grokTeamRateLimitDefaultTTL)
}

package service

import (
	"strconv"
	"strings"
	"sync"
	"time"
)

type grokModelQuotaBlock struct {
	Until time.Time
}

type grokModelQuotaBlockStore struct {
	mu    sync.Mutex
	items map[string]grokModelQuotaBlock
}

var globalGrokModelQuotaBlocks = &grokModelQuotaBlockStore{items: make(map[string]grokModelQuotaBlock)}

const (
	grokModelQuotaBlockDefaultTTL = 2 * time.Hour
	grokModelQuotaBlockMaxTTL     = 6 * time.Hour
	grokModelQuotaBlockMinTTL     = 20 * time.Minute
	grokModelTransientBlockMinTTL = 500 * time.Millisecond
	grokModelTransientBlockMaxTTL = 5 * time.Minute
)

func grokModelQuotaBlockKey(accountID int64, model string) string {
	return strings.ToLower(strings.TrimSpace(model)) + "|" + strconv.FormatInt(accountID, 10)
}

func markGrokModelQuotaBlock(accountID int64, model string, until time.Time) {
	model = strings.TrimSpace(model)
	if accountID <= 0 || model == "" || until.IsZero() {
		return
	}
	now := time.Now()
	if !until.After(now.Add(grokModelQuotaBlockMinTTL)) {
		until = now.Add(grokModelQuotaBlockDefaultTTL)
	}
	if max := now.Add(grokModelQuotaBlockMaxTTL); until.After(max) {
		until = max
	}
	storeGrokModelQuotaBlock(accountID, model, until, now)
}

func markGrokModelTransientBlock(accountID int64, model string, until time.Time) {
	model = strings.TrimSpace(model)
	if accountID <= 0 || model == "" || until.IsZero() {
		return
	}
	now := time.Now()
	if !until.After(now.Add(grokModelTransientBlockMinTTL)) {
		until = now.Add(grokModelTransientBlockMinTTL)
	}
	if max := now.Add(grokModelTransientBlockMaxTTL); until.After(max) {
		until = max
	}
	storeGrokModelQuotaBlock(accountID, model, until, now)
}

func storeGrokModelQuotaBlock(accountID int64, model string, until, now time.Time) {
	key := grokModelQuotaBlockKey(accountID, model)
	globalGrokModelQuotaBlocks.mu.Lock()
	defer globalGrokModelQuotaBlocks.mu.Unlock()
	if current, ok := globalGrokModelQuotaBlocks.items[key]; ok && current.Until.After(until) {
		return
	}
	globalGrokModelQuotaBlocks.items[key] = grokModelQuotaBlock{Until: until}
	for itemKey, item := range globalGrokModelQuotaBlocks.items {
		if !item.Until.After(now) {
			delete(globalGrokModelQuotaBlocks.items, itemKey)
		}
	}
}

func isGrokModelQuotaBlocked(accountID int64, model string, now time.Time) bool {
	model = strings.TrimSpace(model)
	if accountID <= 0 || model == "" {
		return false
	}
	key := grokModelQuotaBlockKey(accountID, model)
	globalGrokModelQuotaBlocks.mu.Lock()
	defer globalGrokModelQuotaBlocks.mu.Unlock()
	current, ok := globalGrokModelQuotaBlocks.items[key]
	if !ok {
		return false
	}
	if !current.Until.After(now) {
		delete(globalGrokModelQuotaBlocks.items, key)
		return false
	}
	return true
}

func filterGrokModelQuotaBlockedAccounts(accounts []Account, model string, now time.Time) []Account {
	if len(accounts) == 0 || strings.TrimSpace(model) == "" {
		return accounts
	}
	out := make([]Account, 0, len(accounts))
	for i := range accounts {
		upstreamModel := canonicalOpenAIAccountSchedulingModel(&accounts[i], model)
		if !isGrokModelQuotaBlocked(accounts[i].ID, upstreamModel, now) {
			out = append(out, accounts[i])
		}
	}
	return out
}

func isGrokModelSpecificFreeUsage(low, model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	if model == "" || low == "" {
		return false
	}
	return strings.Contains(low, "for model") || strings.Contains(low, "模型") ||
		(strings.Contains(low, "free usage") && strings.Contains(low, model))
}

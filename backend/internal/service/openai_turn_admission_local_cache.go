package service

import (
	"maps"
	"slices"
	"sync"
	"time"
)

type openAITurnAdmissionLocalCacheEntry struct {
	account *Account
	parent  *Account
	stored  time.Time
}

type openAITurnAdmissionLocalCache struct {
	mu      sync.RWMutex
	entries map[int64]openAITurnAdmissionLocalCacheEntry
}

func (c *openAITurnAdmissionLocalCache) load(accountID int64, ttl time.Duration) (*Account, *Account, bool) {
	if accountID <= 0 || ttl <= 0 {
		return nil, nil, false
	}
	c.mu.RLock()
	entry, ok := c.entries[accountID]
	c.mu.RUnlock()
	if !ok || time.Since(entry.stored) >= ttl {
		return nil, nil, false
	}
	return cloneOpenAITurnAdmissionAccount(entry.account), cloneOpenAITurnAdmissionAccount(entry.parent), true
}

func (c *openAITurnAdmissionLocalCache) store(account, parent *Account) {
	if account == nil || account.ID <= 0 {
		return
	}
	c.mu.Lock()
	if c.entries == nil {
		c.entries = make(map[int64]openAITurnAdmissionLocalCacheEntry)
	}
	c.entries[account.ID] = openAITurnAdmissionLocalCacheEntry{
		account: cloneOpenAITurnAdmissionAccount(account),
		parent:  cloneOpenAITurnAdmissionAccount(parent),
		stored:  time.Now(),
	}
	c.mu.Unlock()
}

func (s *OpenAIGatewayService) openAITurnAdmissionCacheTTL() time.Duration {
	if s == nil || s.cfg == nil {
		return 0
	}
	return time.Duration(s.cfg.Gateway.Scheduling.OpenAITurnAdmissionCacheTTLSeconds) * time.Second
}

func cloneOpenAITurnAdmissionAccount(account *Account) *Account {
	if account == nil {
		return nil
	}
	clone := *account
	clone.Credentials = maps.Clone(account.Credentials)
	clone.Extra = maps.Clone(account.Extra)
	clone.GroupIDs = slices.Clone(account.GroupIDs)
	clone.Groups = nil
	clone.AccountGroups = make([]AccountGroup, len(account.AccountGroups))
	for i := range account.AccountGroups {
		clone.AccountGroups[i] = account.AccountGroups[i]
		clone.AccountGroups[i].AllowedModels = slices.Clone(account.AccountGroups[i].AllowedModels)
		clone.AccountGroups[i].Group = nil
	}
	if account.Proxy != nil {
		proxy := *account.Proxy
		clone.Proxy = &proxy
	}
	clone.modelMappingCache = nil
	clone.modelMappingCacheReady = false
	clone.headerOverrideCache = nil
	clone.headerOverrideCacheReady = false
	return &clone
}

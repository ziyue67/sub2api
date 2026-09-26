package service

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"sync"
	"time"
)

const codex780RouteCacheLimit = 256

// Routes outlive individual tickets and model declarations. Keys retain no tokens.
type codex780RouteEntry struct {
	cookies []string
	expires time.Time
}

type codex780RouteCache struct {
	mu      sync.Mutex
	entries map[[32]byte]codex780RouteEntry
}

func codex780RouteKey(account *Account, token, accountHeader, target, transport string) [32]byte {
	raw, _ := json.Marshal([]any{account.ID, ticketIdentity(account), token, accountHeader, target, transport})
	return sha256.Sum256(raw)
}

func (c *codex780RouteCache) get(key [32]byte, now time.Time) ([]string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if ok && !now.Before(e.expires) {
		delete(c.entries, key)
		return nil, false
	}
	return slices.Clone(e.cookies), ok
}

func (c *codex780RouteCache) update(key [32]byte, target string, seed []string, out codexHarvestProbeResult, now time.Time) {
	if !out.Sent {
		return
	}
	var mintErr *codexMintError
	invalid := errors.As(out.Err, &mintErr) && (mintErr.kind == "invalid_route" || mintErr.kind == "account_error")
	invalid = invalid || out.Status == http.StatusUnauthorized || out.Status == http.StatusForbidden
	if !invalid && out.Status != http.StatusOK && out.Status != http.StatusSwitchingProtocols {
		return
	}
	cookies, expiry, err := codex780Route(out.Cookies, target, now)
	if invalid {
		// Keep a tombstone so a persisted ticket cannot resurrect a rejected seed.
		_, expiry, err = codex780Route(seed, target, now)
		cookies = nil
	}
	if err != nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[[32]byte]codex780RouteEntry)
	}
	if invalid {
		if current, ok := c.entries[key]; ok && !slices.Equal(current.cookies, seed) {
			return // A late failure must not evict a newer route from another lane.
		}
	}
	if current, ok := c.entries[key]; ok && len(seed) > 0 && !slices.Equal(current.cookies, seed) && slices.Equal(cookies, seed) {
		return // A late unchanged response cannot restore a superseded or rejected seed.
	}
	for k, entry := range c.entries {
		if !now.Before(entry.expires) {
			delete(c.entries, k)
		}
	}
	if _, exists := c.entries[key]; !exists && len(c.entries) >= codex780RouteCacheLimit {
		var oldest [32]byte
		var earliest time.Time
		for k, entry := range c.entries {
			if earliest.IsZero() || entry.expires.Before(earliest) {
				oldest, earliest = k, entry.expires
			}
		}
		delete(c.entries, oldest)
	}
	c.entries[key] = codex780RouteEntry{cookies: slices.Clone(cookies), expires: expiry}
}

package repository

import (
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/mihomo"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// Cache decisions survive settings revisions, not process restarts. They never
// contain Cookies or credentials, and are not evidence of an upstream cache TTL.
type astraRotationCache struct {
	mu      sync.Mutex
	blocked map[astraRotationCacheKey]time.Time
	hosts   map[string]astraNodeHost
}
type astraRotationCacheKey struct {
	target      int64
	kind, value string
}
type astraNodeHost struct {
	host    string
	expires time.Time
}

func (c *astraRotationCache) prune(now time.Time) {
	for k, v := range c.blocked {
		if !now.Before(v) {
			delete(c.blocked, k)
		}
	}
	for k, v := range c.hosts {
		if !now.Before(v.expires) {
			delete(c.hosts, k)
		}
	}
}
func (c *astraRotationCache) observe(node, host string, now time.Time) {
	if node == "" || host == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.prune(now)
	if c.hosts == nil {
		c.hosts = map[string]astraNodeHost{}
	}
	if len(c.hosts) >= 4096 {
		for k := range c.hosts {
			delete(c.hosts, k)
			break
		}
	}
	c.hosts[node] = astraNodeHost{host: host, expires: now.Add(24 * time.Hour)}
}
func (c *astraRotationCache) reject(target int64, node, host string, until, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.prune(now)
	if c.blocked == nil {
		c.blocked = map[astraRotationCacheKey]time.Time{}
	}
	for kind, value := range map[string]string{"node": node, "host": host} {
		if value == "" {
			continue
		}
		k := astraRotationCacheKey{target: target, kind: kind, value: value}
		// A late duplicate observation must not slide an existing quiet window.
		if _, exists := c.blocked[k]; exists {
			continue
		}
		if len(c.blocked) >= 16384 {
			for old := range c.blocked {
				delete(c.blocked, old)
				break
			}
		}
		c.blocked[k] = until
	}
}
func (c *astraRotationCache) blockedUntil(target int64, node, host string, now time.Time) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.prune(now)
	if host == "" {
		host = c.hosts[node].host
	}
	a := c.blocked[astraRotationCacheKey{target: target, kind: "node", value: node}]
	b := c.blocked[astraRotationCacheKey{target: target, kind: "host", value: host}]
	if b.After(a) {
		return b
	}
	return a
}
func (p *codexGatewayPinUpstream) nodeCooling(node mihomo.AstraNode, host string, target int64, now time.Time) bool {
	if p.rotationCache == nil {
		return false
	}
	ids := p.config.TargetAccountIDs
	if target != 0 {
		ids = []int64{target}
	}
	if len(ids) == 0 {
		return false
	}
	for _, id := range ids {
		if !now.Before(p.rotationCache.blockedUntil(id, node.Identity, host, now)) {
			return false
		}
	}
	return true
}
func (p *codexGatewayPinUpstream) rememberTargetFailure(id int64, check astraTargetValidation) {
	if !p.config.RotateNodes || p.rotationCache == nil || check.node.Identity == "" {
		return
	}
	seconds := p.config.NodeCooldownSeconds
	if seconds == 0 {
		seconds = 3600
	}
	now := time.Now()
	p.rotationCache.reject(id, check.node.Identity, check.gateway, now.Add(time.Duration(seconds)*time.Second), now)
}
func (c *astraRotationCache) snapshot(ids []int64, now time.Time) []service.AstraRotationCooldown {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.prune(now)
	rows := []service.AstraRotationCooldown{}
	for k, until := range c.blocked {
		selected := false
		for _, id := range ids {
			if id == k.target {
				selected = true
				break
			}
		}
		if selected && k.kind == "host" {
			rows = append(rows, service.AstraRotationCooldown{AccountID: k.target, Gateway: k.value, RetryAt: until, RemainingSeconds: int64(until.Sub(now).Seconds())})
		}
	}
	return rows
}

// Source transport/qualification failures are scoped to the donor and node,
// independently from the longer target-host quiet window.
const astraSourceFailureCooldown = time.Minute

func (c *astraRotationCache) sourceCooling(source int64, node string, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.prune(now)
	return now.Before(c.blocked[astraRotationCacheKey{target: source, kind: "source", value: node}])
}
func (c *astraRotationCache) rejectSource(source int64, node string, now time.Time) {
	if node == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.prune(now)
	if c.blocked == nil {
		c.blocked = map[astraRotationCacheKey]time.Time{}
	}
	key := astraRotationCacheKey{target: source, kind: "source", value: node}
	if _, ok := c.blocked[key]; ok {
		return
	}
	if len(c.blocked) >= 16384 {
		for old := range c.blocked {
			delete(c.blocked, old)
			break
		}
	}
	c.blocked[key] = now.Add(astraSourceFailureCooldown)
}
func (p *codexGatewayPinUpstream) sourcesCooling(node string, now time.Time) bool {
	if p.rotationCache == nil {
		return false
	}
	for _, source := range p.config.SourceAccountIDs {
		if !p.rotationCache.sourceCooling(source, node, now) {
			return false
		}
	}
	return len(p.config.SourceAccountIDs) > 0
}

// A new exit which rediscovers a cooling host must not trigger another full
// source sweep on every request. Keep the pause across settings revisions.
func (c *astraRotationCache) pauseSourceSearch(target int64, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.prune(now)
	if c.blocked == nil {
		c.blocked = map[astraRotationCacheKey]time.Time{}
	}
	key := astraRotationCacheKey{target: target, kind: "source_search"}
	if _, exists := c.blocked[key]; !exists {
		c.blocked[key] = now.Add(5 * time.Minute)
	}
}
func (c *astraRotationCache) sourceSearchPaused(target int64, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.prune(now)
	return now.Before(c.blocked[astraRotationCacheKey{target: target, kind: "source_search"}])
}

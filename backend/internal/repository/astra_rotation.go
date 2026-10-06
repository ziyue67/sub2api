package repository

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/mihomo"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type astraSourceEgress struct {
	node  mihomo.AstraNode
	proxy string
}
type astraSourceEgressKey struct{}
type astraRotationBudgetKey struct{}
type astraRotationTargetKey struct{}
type astraRotationBudget struct {
	remaining int
	seen      map[string]bool
}

func astraNodeLimit(pool *codexGatewayPinUpstream) int {
	if pool.config.MaxNodeAttempts > 0 {
		return pool.config.MaxNodeAttempts
	}
	return 3
}
func (s *astraRoutingUpstream) availableNodes() ([]mihomo.AstraNode, error) {
	if s.listNodes != nil {
		return s.listNodes()
	}
	return mihomo.AstraNodes()
}
func (s *astraRoutingUpstream) acquireNode(ctx context.Context, node mihomo.AstraNode) (string, func(), error) {
	if s.pinNode != nil {
		return s.pinNode(ctx, node)
	}
	return mihomo.PinAstraNode(ctx, node)
}

// prepareMu serializes cursor/budget advancement. A candidate always records the
// node identity which actually produced its Cookie; account proxy rows are untouched.
func (s *astraRoutingUpstream) prepareRotatedSource(ctx context.Context, pool *codexGatewayPinUpstream) error {
	if cookie, _ := pool.currentCookie("/backend-api/codex/responses", time.Now()); cookie != nil {
		return nil
	}
	if s.preparer == nil {
		return errors.New("source_preparer_unavailable")
	}
	pool.mu.Lock()
	retryAfter := pool.rotationRetryAfter
	pool.mu.Unlock()
	if time.Now().Before(retryAfter) {
		return errors.New("source_probe_cooldown")
	}
	target, _ := ctx.Value(astraRotationTargetKey{}).(int64)
	if pool.rotationCache != nil && pool.rotationCache.sourceSearchPaused(target, time.Now()) {
		return errors.New("astra_rotation_cooling")
	}
	nodes, err := s.availableNodes()
	if err != nil {
		return err
	}
	if len(nodes) == 0 {
		return errors.New("astra_rotation_no_nodes")
	}
	budget, _ := ctx.Value(astraRotationBudgetKey{}).(*astraRotationBudget)
	if budget == nil {
		budget = &astraRotationBudget{remaining: astraNodeLimit(pool), seen: map[string]bool{}}
	}
	eligible := false
	s.preparing.Store(true)
	defer s.preparing.Store(false)
	for scanned := 0; scanned < len(nodes) && budget.remaining > 0; scanned++ {
		node := nodes[pool.nodeCursor%len(nodes)]
		pool.nodeCursor++
		if budget.seen[node.Identity] || pool.nodeCooling(node, "", target, time.Now()) || pool.sourcesCooling(node.Identity, time.Now()) {
			continue
		}
		eligible = true
		budget.seen[node.Identity] = true
		budget.remaining--
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if s.current(ctx) != pool {
			return errors.New("configuration_changed")
		}
		proxy, release, pinErr := s.acquireNode(ctx, node)
		if pinErr != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if s.current(ctx) == pool && pool.rotationCache != nil {
				for _, id := range pool.config.SourceAccountIDs {
					pool.rotationCache.rejectSource(id, node.Identity, time.Now())
				}
			}
			continue
		}
		var repeatedCoolingHost atomic.Bool
		success := func() bool {
			defer release()
			probe := func(id int64, probeCtx context.Context) bool {
				if probeCtx.Err() != nil {
					return false
				}
				if pool.rotationCache != nil && pool.rotationCache.sourceCooling(id, node.Identity, time.Now()) {
					return false
				}
				started := time.Now()
				err := s.preparer(probeCtx, id)
				if probeCtx.Err() != nil {
					return false
				}
				if err != nil {
					pool.recordSourceReason(id, started, nil, false, "source_test_failed")
				}
				if cookie, source := pool.currentCookie("/backend-api/codex/responses", time.Now()); cookie != nil {
					pool.mu.Lock()
					route := pool.routes[source]
					pool.mu.Unlock()
					host := astraRoutingHost(cookie.Value)
					if pool.rotationCache != nil {
						pool.rotationCache.observe(route.node.Identity, host, time.Now())
					}
					if pool.nodeCooling(route.node, host, target, time.Now()) {
						repeatedCoolingHost.Store(true)
						pool.mu.Lock()
						delete(pool.routes, source)
						pool.activeSource = 0
						status := pool.statuses[source]
						status.State = "waiting"
						status.Reason = "astra_rotation_cooling"
						pool.statuses[source] = status
						pool.mu.Unlock()
						return false
					}
					return true
				}
				if pool.rotationCache != nil {
					pool.rotationCache.rejectSource(id, node.Identity, time.Now())
				}
				return false
			}
			// With three or more configured donors, probe them concurrently so one
			// slow or degraded donor does not block the backup candidates. Existing
			// two-account configurations retain their deterministic serial order.
			if len(pool.config.SourceAccountIDs) <= 2 {
				for _, id := range pool.config.SourceAccountIDs {
					if ctx.Err() != nil || s.current(ctx) != pool {
						return false
					}
					probeCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
					probeCtx = context.WithValue(probeCtx, astraSourceEgressKey{}, astraSourceEgress{node: node, proxy: proxy})
					ok := probe(id, probeCtx)
					cancel()
					if ok {
						return true
					}
				}
				return false
			}
			probeCtx, cancelAll := context.WithCancel(ctx)
			defer cancelAll()
			results := make(chan bool, len(pool.config.SourceAccountIDs))
			var wg sync.WaitGroup
			for _, id := range pool.config.SourceAccountIDs {
				if pool.rotationCache != nil && pool.rotationCache.sourceCooling(id, node.Identity, time.Now()) {
					continue
				}
				wg.Add(1)
				go func(id int64) {
					defer wg.Done()
					child, cancel := context.WithTimeout(probeCtx, 45*time.Second)
					child = context.WithValue(child, astraSourceEgressKey{}, astraSourceEgress{node: node, proxy: proxy})
					results <- probe(id, child)
					cancel()
				}(id)
			}
			go func() { wg.Wait(); close(results) }()
			ok := false
			for result := range results {
				if result {
					ok = true
					cancelAll()
				}
			}
			return ok
		}()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if s.current(ctx) != pool {
			return errors.New("configuration_changed")
		}
		if success {
			pool.mu.Lock()
			pool.rotationRetryAfter = time.Time{}
			pool.mu.Unlock()
			return nil
		}
		if repeatedCoolingHost.Load() {
			if pool.rotationCache != nil {
				pool.rotationCache.pauseSourceSearch(target, time.Now())
			}
			return errors.New("astra_rotation_cooling")
		}
	}
	if !eligible {
		return errors.New("astra_rotation_cooling")
	}
	pool.mu.Lock()
	pool.rotationRetryAfter = time.Now().Add(15 * time.Second)
	pool.mu.Unlock()
	return errors.New("astra_rotation_exhausted")
}

func (s *astraRoutingUpstream) targetRoute(req *http.Request, proxy string, id int64, n int, profile *tlsfingerprint.Profile) (*http.Cookie, [32]byte, string, func(), error) {
	pool := s.current(req.Context())
	if pool == nil || !pool.config.RotateNodes {
		return s.targetRouteOnce(req, proxy, id, n, profile)
	}
	ctx, cancel := context.WithTimeout(req.Context(), 180*time.Second)
	defer cancel()
	budget := &astraRotationBudget{remaining: astraNodeLimit(pool), seen: map[string]bool{}}
	if _, source := pool.currentCookie(req.URL.Path, time.Now()); source != 0 {
		pool.mu.Lock()
		node := pool.routes[source].node
		pool.mu.Unlock()
		budget.seen[node.Identity] = true
		budget.remaining--
	}
	req = req.Clone(context.WithValue(context.WithValue(ctx, astraRotationTargetKey{}, id), astraRotationBudgetKey{}, budget))
	for {
		priorCookie, priorSource := pool.currentCookie(req.URL.Path, time.Now())
		cookie, key, egress, release, err := s.targetRouteOnce(req, proxy, id, n, profile)
		if err == nil {
			return cookie, key, egress, release, nil
		}
		if ctx.Err() != nil {
			return nil, key, proxy, func() {}, ctx.Err()
		}
		if s.current(ctx) != pool {
			return nil, key, proxy, func() {}, errors.New("configuration_changed")
		}
		if err.Error() == "astra_rotation_cooling" && priorCookie == nil {
			return nil, key, proxy, func() {}, err
		}
		switch err.Error() {
		case "astra_rotation_node_unavailable":
			pool.mu.Lock()
			if route, ok := pool.routes[priorSource]; ok && priorCookie != nil && route.cookie.Value == priorCookie.Value {
				delete(pool.routes, priorSource)
				pool.activeSource = 0
			}
			pool.mu.Unlock()
		case "astra_rotation_cooling", "target_probe_degraded", "target_route_changed", "target_probe_failed":
			pool.targetMu.Lock()
			check := pool.targetChecks[id]
			pool.mu.Lock()
			if route, ok := pool.routes[check.sourceID]; ok && sha256.Sum256([]byte(route.cookie.Value)) == check.cookieFingerprint && route.node.Identity == check.node.Identity {
				delete(pool.routes, check.sourceID)
				status := pool.statuses[check.sourceID]
				status.State = "candidate"
				status.Reason = "source_passed_target_failed"
				pool.statuses[check.sourceID] = status
				pool.activeSource = 0
			}
			pool.mu.Unlock()
			pool.targetMu.Unlock()
		default:
			return nil, key, proxy, func() {}, err
		}
		if budget.remaining <= 0 {
			pool.mu.Lock()
			pool.rotationRetryAfter = time.Now().Add(15 * time.Second)
			pool.mu.Unlock()
			return nil, key, proxy, func() {}, errors.New("astra_rotation_exhausted")
		}
	}
}

// A reused listener must open a new CONNECT, never reuse a previous node's idle connection.
func astraFreshRequest(r *http.Request) *http.Request {
	return r.Clone(service.WithHTTPUpstreamProfile(r.Context(), service.HTTPUpstreamProfileOpenAIHarvest))
}

type astraLeaseBody struct {
	io.ReadCloser
	release func()
	once    sync.Once
}

func (b *astraLeaseBody) Close() error { err := b.ReadCloser.Close(); b.once.Do(b.release); return err }
func attachAstraLease(resp *http.Response, err error, release func()) (*http.Response, error) {
	if err != nil || resp == nil || resp.Body == nil {
		release()
		return resp, err
	}
	resp.Body = &astraLeaseBody{ReadCloser: resp.Body, release: release}
	return resp, err
}

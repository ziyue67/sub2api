package repository

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/mihomo"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// astraContextMutex bounds admission waits without spawning a goroutine per
// waiter. Its zero value is ready to use; cancellation never acquires a lease.
type astraContextMutex struct {
	once  sync.Once
	token chan struct{}
}

func (m *astraContextMutex) Lock(ctx context.Context) error {
	m.once.Do(func() { m.token = make(chan struct{}, 1) })
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case m.token <- struct{}{}:
		if err := ctx.Err(); err != nil {
			m.Unlock()
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (m *astraContextMutex) Unlock() { <-m.token }

type astraTargetValidation struct {
	node                         mihomo.AstraNode
	key                          [32]byte
	cookieFingerprint            [32]byte
	proxyFingerprint             [32]byte
	passed                       bool
	checked, expires, retryAfter time.Time
	sourceID                     int64
	reason                       string
	answer                       string
	gateway                      string
}

// Resolve a borrowed route for ordinary traffic without probing its quality.
// Only an explicit diagnostic request runs the two-shot target probe.
func (s *astraRoutingUpstream) targetRouteOnce(req *http.Request, proxy string, id int64, n int, profile *tlsfingerprint.Profile) (resultCookie *http.Cookie, resultKey [32]byte, resultProxy string, release func(), resultErr error) {
	release = func() {}
	defer func() {
		if resultErr != nil {
			release()
			release = func() {}
		}
	}()
	var zero [32]byte
	force, _ := req.Context().Value(astraForceProbeKey{}).(bool)
	if err := req.Context().Err(); err != nil {
		return nil, zero, proxy, release, err
	}
	pool := s.current(req.Context())
	if pool == nil {
		return nil, zero, proxy, release, nil
	}
	cookie, sourceID := pool.currentCookie(req.URL.Path, time.Now())
	if cookie == nil && !force {
		return nil, zero, proxy, release, nil
	}
	if cookie == nil {
		if err := s.PrepareAstraGateway(req.Context()); err != nil {
			return nil, zero, proxy, release, err
		}
		pool = s.current(req.Context())
		if pool == nil {
			return nil, zero, proxy, release, nil
		}
		cookie, sourceID = pool.currentCookie(req.URL.Path, time.Now())
	}
	if cookie == nil {
		return nil, zero, proxy, release, errCodexGatewayPinUnavailable
	}
	pool.mu.Lock()
	route := pool.routes[sourceID]
	expires := route.expires
	nodeStore := pool.nodeStore
	pool.mu.Unlock()
	if route.cookie.Value != cookie.Value || !time.Now().Before(expires) {
		return nil, zero, proxy, release, errors.New("configuration_changed")
	}
	if force && pool.config.RotateNodes && pool.nodeCooling(route.node, astraRoutingHost(cookie.Value), id, time.Now()) {
		pool.targetMu.Lock()
		if pool.targetChecks == nil {
			pool.targetChecks = map[int64]astraTargetValidation{}
		}
		pool.targetChecks[id] = astraTargetValidation{node: route.node, sourceID: sourceID, cookieFingerprint: sha256.Sum256([]byte(cookie.Value)), gateway: astraRoutingHost(cookie.Value), reason: "astra_rotation_cooling", checked: time.Now()}
		pool.targetMu.Unlock()
		return nil, zero, proxy, release, errors.New("astra_rotation_cooling")
	}
	if pool.config.IPAffinity {
		proxy = route.proxy
	}
	identityProxy := proxy
	if pool.config.RotateNodes {
		if route.node.ID == "" {
			return nil, zero, proxy, release, errors.New("astra_rotation_node_unavailable")
		}
		var err error
		proxy, release, err = s.acquireNode(req.Context(), route.node)
		if err != nil {
			return nil, zero, proxy, release, err
		}
		identityProxy = route.node.Identity
		req = astraFreshRequest(req)
	}
	// Business traffic and parallel IQ tests neither wait for diagnostics nor
	// consume their pass/fail cache. The response belongs to the caller.
	if !force {
		return cookie, zero, proxy, release, nil
	}
	identity := []string{cookie.Value, identityProxy, req.Header.Get("Authorization"), req.Header.Get("ChatGPT-Account-ID"), req.Header.Get("User-Agent"), req.Header.Get("Originator"), req.Header.Get("Version"), req.Header.Get("X-Codex-Turn-State")}
	fingerprint, _ := json.Marshal(profile)
	identity = append(identity, string(fingerprint))
	key := sha256.Sum256([]byte(strings.Join(identity, "\x00")))
	ctx, cancel := context.WithTimeout(req.Context(), 90*time.Second)
	defer cancel()
	if err := pool.targetProbeMu.Lock(ctx); err != nil {
		return nil, key, proxy, release, err
	}
	defer pool.targetProbeMu.Unlock()
	// The route may have expired, rotated or been replaced while waiting. Never
	// reuse old evidence or probe the old exit after admission.
	if s.current(ctx) != pool {
		return nil, key, proxy, release, errors.New("configuration_changed")
	}
	current, currentID := pool.currentCookie(req.URL.Path, time.Now())
	pool.mu.Lock()
	currentRoute := pool.routes[sourceID]
	pool.mu.Unlock()
	if current == nil || current.Value != cookie.Value || currentID != sourceID ||
		(pool.config.IPAffinity && !pool.config.RotateNodes && currentRoute.proxy != route.proxy) ||
		currentRoute.node.Identity != route.node.Identity {
		return nil, key, proxy, release, errors.New("configuration_changed")
	}
	now := time.Now()
	pool.targetMu.Lock()
	old, exists := pool.targetChecks[id]
	pool.targetMu.Unlock()
	if exists && old.key == key && now.Before(old.expires) && !force {
		if old.passed {
			return cookie, key, proxy, release, nil
		}
		if now.Before(old.retryAfter) {
			return nil, key, proxy, release, errors.New(old.reason)
		}
	}

	probe := req.Clone(service.WithHTTPUpstreamRedirectsDisabled(ctx))
	replaceCodexGatewayCookie(probe, cookie)
	result := service.ProbeOpenAICodexStateRoute(ctx, s.delegate, probe, proxy, id, n, profile)
	passed := result.Verdict == service.OpenAICodexStateHealthy
	reason := "target_probe_failed"
	if result.Verdict == service.OpenAICodexStateDegraded {
		reason = "target_probe_degraded"
	}
	if result.Failure == "route_changed" {
		reason = "target_route_changed"
	}

	if s.current(req.Context()) != pool {
		return nil, key, proxy, release, errors.New("configuration_changed")
	}
	current, currentID = pool.currentCookie(req.URL.Path, time.Now())
	if current == nil || current.Value != cookie.Value || currentID != sourceID {
		return nil, key, proxy, release, errors.New("configuration_changed")
	}
	pool.mu.Lock()
	currentRoute = pool.routes[sourceID]
	pool.mu.Unlock()
	if (pool.config.IPAffinity && !pool.config.RotateNodes && currentRoute.proxy != route.proxy) || currentRoute.node.Identity != route.node.Identity {
		return nil, key, proxy, release, errors.New("configuration_changed")
	}
	pool.targetMu.Lock()
	defer func() {
		pool.targetMu.Unlock()
		if pool.historyRecorder != nil {
			pool.historyRecorder(service.AstraGatewayHistoryRecord{Gateway: astraRoutingHost(cookie.Value), SourceAccountID: sourceID, TargetAccountID: id, LastSeen: time.Now(), LastReason: reason}, passed)
		}
	}()
	if pool.targetChecks == nil {
		pool.targetChecks = make(map[int64]astraTargetValidation)
	}
	if passed {
		reason = "target_probe_passed"
	}
	host := astraRoutingHost(cookie.Value)
	pool.targetChecks[id] = astraTargetValidation{node: route.node, key: key, cookieFingerprint: sha256.Sum256([]byte(cookie.Value)), proxyFingerprint: sha256.Sum256([]byte(proxy)), passed: passed, checked: time.Now(), expires: expires, retryAfter: time.Now().Add(15 * time.Second), sourceID: sourceID, reason: reason, gateway: host}
	pool.mu.Lock()
	pool.observeGatewayTarget(host, passed)
	pool.mu.Unlock()
	if nodeStore != nil {
		nodeStore.MarkTarget(cookie.Value, id, passed, reason, time.Now(), expires, time.Now().Add(15*time.Second))
	}
	if !passed {
		return nil, key, proxy, release, errors.New(reason)
	}
	return cookie, key, proxy, release, nil
}

// Manual verification always refreshes the evidence instead of accepting a cached pass.
type astraForceProbeKey struct{}

func (s *astraRoutingUpstream) VerifyAstraGatewayTarget(ctx context.Context, req *http.Request, proxy string, id int64, n int) error {
	if s.unifiedTickets(ctx) {
		return s.tickets.VerifyStoredAstraTarget(ctx, id)
	}
	req = req.Clone(context.WithValue(ctx, astraForceProbeKey{}, true))
	_, _, _, release, err := s.targetRouteOnce(req, proxy, id, n, nil)
	release()
	return err
}
func (s *astraRoutingUpstream) CodexGatewayPinWSRequest(ctx context.Context, headers http.Header, proxy string, id int64, n int) (string, string, func(), error) {
	if s.unifiedTickets(ctx) {
		if !containsTarget(s.cfg.AstraRouting(ctx).CookiePool.TargetAccountIDs, id) {
			return "", proxy, func() {}, nil
		}
		borrowed := headers.Clone()
		effective, release, err := s.tickets.ApplyStoredAstraTarget(ctx, borrowed, id, "websocket")
		if err != nil {
			if release != nil {
				release()
			}
			return "", proxy, func() {}, ctx.Err()
		}
		for key := range headers {
			delete(headers, key)
		}
		for key, values := range borrowed {
			headers[key] = values
		}
		return "", effective, release, nil
	}
	pool := s.current(ctx)
	if pool == nil || !containsTarget(pool.config.TargetAccountIDs, id) {
		return "", proxy, func() {}, nil
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://chatgpt.com/backend-api/codex/responses", nil)
	req.Header = headers.Clone()
	cookie, _, proxy, release, err := s.targetRoute(req, proxy, id, n, nil)
	if err != nil || cookie == nil {
		return "", proxy, release, err
	}
	return cookie.Value, proxy, release, nil
}
func containsTarget(ids []int64, id int64) bool {
	for _, value := range ids {
		if value == id {
			return true
		}
	}
	return false
}

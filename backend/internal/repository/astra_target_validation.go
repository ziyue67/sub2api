package repository

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/mihomo"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

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

// Source success is only a candidate. Validate the borrowed route with the
// existing two-shot state probe and the target credentials on the actual exit.
func (s *astraRoutingUpstream) targetRouteOnce(req *http.Request, proxy string, id int64, n int, profile *tlsfingerprint.Profile) (resultCookie *http.Cookie, resultKey [32]byte, resultProxy string, release func(), resultErr error) {
	release = func() {}
	defer func() {
		if resultErr != nil {
			release()
			release = func() {}
		}
	}()
	var zero [32]byte
	pool := s.current(req.Context())
	if pool == nil {
		return nil, zero, proxy, release, nil
	}
	cookie, sourceID := pool.currentCookie(req.URL.Path, time.Now())
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
	if pool.config.RotateNodes && pool.nodeCooling(route.node, astraRoutingHost(cookie.Value), id, time.Now()) {
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
	identity := []string{cookie.Value, identityProxy, req.Header.Get("Authorization"), req.Header.Get("ChatGPT-Account-ID"), req.Header.Get("User-Agent"), req.Header.Get("Originator"), req.Header.Get("Version"), req.Header.Get("X-Codex-Turn-State")}
	fingerprint, _ := json.Marshal(profile)
	identity = append(identity, string(fingerprint))
	key := sha256.Sum256([]byte(strings.Join(identity, "\x00")))
	if !pool.targetProbeMu.TryLock() {
		return nil, key, proxy, release, errors.New("target_validation_in_progress")
	}
	defer pool.targetProbeMu.Unlock()
	now := time.Now()
	pool.targetMu.Lock()
	old, exists := pool.targetChecks[id]
	pool.targetMu.Unlock()
	force, _ := req.Context().Value(astraForceProbeKey{}).(bool)
	if exists && old.key == key && now.Before(old.expires) && !force {
		if old.passed {
			return cookie, key, proxy, release, nil
		}
		if now.Before(old.retryAfter) {
			return nil, key, proxy, release, errors.New(old.reason)
		}
	}

	ctx, cancel := context.WithTimeout(req.Context(), 90*time.Second)
	defer cancel()
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
	current, currentID := pool.currentCookie(req.URL.Path, time.Now())
	if current == nil || current.Value != cookie.Value || currentID != sourceID {
		return nil, key, proxy, release, errors.New("configuration_changed")
	}
	pool.mu.Lock()
	currentRoute := pool.routes[sourceID]
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
		pool.rememberTargetFailure(id, pool.targetChecks[id])
		return nil, key, proxy, release, errors.New(reason)
	}
	return cookie, key, proxy, release, nil
}

// Manual verification always refreshes the evidence instead of accepting a cached pass.
type astraForceProbeKey struct{}

func (s *astraRoutingUpstream) VerifyAstraGatewayTarget(ctx context.Context, req *http.Request, proxy string, id int64, n int) error {
	req = req.Clone(context.WithValue(ctx, astraForceProbeKey{}, true))
	_, _, _, release, err := s.targetRoute(req, proxy, id, n, nil)
	release()
	return err
}
func (s *astraRoutingUpstream) CodexGatewayPinWSRequest(ctx context.Context, headers http.Header, proxy string, id int64, n int) (string, string, func(), error) {
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

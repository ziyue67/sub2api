package repository

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/mihomo"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/tidwall/gjson"
)

var errCodexGatewayPinUnavailable = errors.New("codex gateway pin: no qualified source route; run an Astra HTTP test on a source account first")

// This private experiment runs after ticket/identity header assembly and also
// covers account tests. Only __oailb is shared; credentials and turn state stay
// owned by the selected account. All routes are process-local and bounded.
type codexGatewayPinUpstream struct {
	rotationCache         *astraRotationCache
	nodeCursor            int
	rotationRetryAfter    time.Time
	targetMu              sync.Mutex
	targetProbeMu         sync.Mutex
	targetChecks          map[int64]astraTargetValidation
	delegate              service.HTTPUpstream
	config                config.CodexGatewayPinConfig
	mu                    sync.Mutex
	routes                map[int64]codexGatewayRoute
	observations          map[int64]time.Time
	activeSource          int64
	statuses              map[int64]service.AstraRouteStatus
	nodeStore             *AstraNodeStore
	gateways              map[string]*service.AstraGatewayObservation
	unknownGatewaySamples int64
	historyRecorder       func(service.AstraGatewayHistoryRecord, bool)
}

type codexGatewayRoute struct {
	node mihomo.AstraNode
	// Process-local only: this may contain proxy credentials. Never expose it.
	proxy   string
	cookie  http.Cookie
	expires time.Time
}

func (s *codexGatewayPinUpstream) Do(req *http.Request, proxy string, accountID int64, concurrency int) (*http.Response, error) {
	return s.DoWithTLS(req, proxy, accountID, concurrency, nil)
}

func (s *codexGatewayPinUpstream) DoWithTLS(req *http.Request, proxy string, accountID int64, concurrency int, profile *tlsfingerprint.Profile) (*http.Response, error) {
	source := slices.Contains(s.config.SourceAccountIDs, accountID)
	target := slices.Contains(s.config.TargetAccountIDs, accountID)
	if !s.config.Enabled || (!source && !target) || !codexGatewayPinRequest(req) || !codexGatewayPinAstra(req) {
		return s.delegate.DoWithTLS(req, proxy, accountID, concurrency, profile)
	}
	if err := s.config.Validate(); err != nil {
		return nil, err
	}
	req = req.Clone(service.WithHTTPUpstreamRedirectsDisabled(req.Context()))
	if target {
		cookie, sourceID := s.currentCookie(req.URL.Path, time.Now())
		if cookie == nil {
			slog.Info("codex_gateway_pin_unavailable", "target_account_id", accountID)
			return nil, errCodexGatewayPinUnavailable
		}
		if s.config.IPAffinity {
			s.mu.Lock()
			route, exists := s.routes[sourceID]
			s.mu.Unlock()
			if !exists || route.cookie.Value != cookie.Value || !time.Now().Before(route.expires) {
				return nil, errCodexGatewayPinUnavailable
			}
			proxy = route.proxy
		}
		replaceCodexGatewayCookie(req, cookie)
		slog.Info("codex_gateway_pin_applied", "source_account_id", sourceID, "target_account_id", accountID)
	}
	egress, _ := req.Context().Value(astraSourceEgressKey{}).(astraSourceEgress)
	if source && egress.node.ID != "" {
		proxy = egress.proxy
		req = astraFreshRequest(req)
	}
	started := time.Now()
	resp, err := s.delegate.DoWithTLS(req, proxy, accountID, concurrency, profile)
	if source && service.IsAstraSourceAcquisition(req.Context()) {
		if err != nil || resp == nil || resp.StatusCode < 200 || resp.StatusCode >= 300 || resp.Body == nil {
			s.recordSourceReason(accountID, started, nil, false, "upstream_http_error")
		} else {
			received := time.Now()
			route := codexGatewayRouteFromResponse(resp, req.URL.Path, received, s.config.TTLSeconds)
			if route != nil {
				route.proxy = proxy
				route.node = egress.node
			}
			ttl := s.config.TTLSeconds
			if ttl == 0 {
				ttl = 230
			}
			if route != nil && !route.expires.IsZero() && route.expires.After(received.Add(time.Duration(ttl)*time.Second)) {
				route.expires = received.Add(time.Duration(ttl) * time.Second)
			}
			// Qualification happens only after the complete SSE response has been read.
			// A header or response.created event does not prove request success.
			if contentType := strings.ToLower(resp.Header.Get("Content-Type")); contentType == "" || strings.HasPrefix(contentType, "text/event-stream") {
				resp.Body = &codexGatewayProbeBody{ReadCloser: resp.Body, onResult: func(ok bool) { s.recordSource(accountID, started, route, ok) }}
			} else {
				s.recordSource(accountID, started, nil, false)
			}
		}
	}
	return resp, err
}

func codexGatewayPinRequest(req *http.Request) bool {
	if req == nil || req.URL == nil || req.Method != http.MethodPost || req.URL.Scheme != "https" || req.URL.Host != "chatgpt.com" || (req.Host != "" && req.Host != "chatgpt.com") {
		return false
	}
	return req.URL.Path == "/backend-api/codex/responses" || req.URL.Path == "/backend-api/codex/responses/lite"
}

func codexGatewayPinAstra(req *http.Request) bool {
	// Requests built by the application have GetBody. Never consume a caller's
	// non-replayable request body just to participate in this experiment.
	if req.GetBody == nil {
		return false
	}
	r, err := req.GetBody()
	if err != nil {
		return false
	}
	defer func() { _ = r.Close() }()
	const limit = 4 << 20
	body, err := io.ReadAll(io.LimitReader(r, limit+1))
	return err == nil && len(body) <= limit && gjson.ValidBytes(body) && gjson.GetBytes(body, "model").String() == "gpt-6-astra"
}

func codexGatewayCookiePathMatches(path, scope string) bool {
	return path == scope || (strings.HasPrefix(path, scope) && (strings.HasSuffix(scope, "/") || strings.HasPrefix(strings.TrimPrefix(path, scope), "/")))
}

func codexGatewayRouteFromResponse(resp *http.Response, path string, now time.Time, configuredTTL ...int) *codexGatewayRoute {
	ttl := 230
	if len(configuredTTL) > 0 && configuredTTL[0] >= 30 && configuredTTL[0] <= 240 {
		ttl = configuredTTL[0]
	}
	var route *codexGatewayRoute
	for _, cookie := range resp.Cookies() {
		if cookie.Name != "__oailb" || !cookie.Secure || (cookie.Domain != "" && strings.TrimPrefix(cookie.Domain, ".") != "chatgpt.com") {
			continue
		}
		if cookie.Path == "" {
			cookie.Path = path[:strings.LastIndex(path, "/")]
		}
		if !strings.HasPrefix(cookie.Path, "/") || !codexGatewayCookiePathMatches(path, cookie.Path) {
			continue
		}
		// A deletion sentinel is applied even if the successful response has no new route.
		if cookie.MaxAge < 0 || cookie.Value == "" || (cookie.MaxAge == 0 && !cookie.Expires.IsZero() && !now.Before(cookie.Expires)) {
			return &codexGatewayRoute{}
		}
		expires := now.Add(time.Duration(ttl) * time.Second)
		if cookie.MaxAge > 0 && cookie.MaxAge < ttl {
			expires = now.Add(time.Duration(cookie.MaxAge) * time.Second)
		} else if cookie.MaxAge == 0 {
			if cookie.Expires.IsZero() {
				continue
			}
			if cookie.Expires.Before(expires) {
				expires = cookie.Expires
			}
		}
		route = &codexGatewayRoute{cookie: *cookie, expires: expires}
	}
	return route
}

func (s *codexGatewayPinUpstream) recordSource(id int64, started time.Time, route *codexGatewayRoute, qualified bool) {
	reason := "response_not_qualified"
	if qualified {
		reason = "qualified"
		if route == nil {
			reason = "routing_cookie_missing"
		} else if route.cookie.Value == "" {
			reason = "routing_cookie_deleted"
		} else if !time.Now().Before(route.expires) {
			reason = "route_expired"
		}
	}
	s.recordSourceReason(id, started, route, qualified, reason)
}
func (s *codexGatewayPinUpstream) recordSourceReason(id int64, started time.Time, route *codexGatewayRoute, qualified bool, reason string) {
	s.mu.Lock()
	var history *service.AstraGatewayHistoryRecord
	defer func() {
		s.mu.Unlock()
		if history != nil && s.historyRecorder != nil {
			s.historyRecorder(*history, qualified)
		}
	}()
	if s.observations == nil {
		s.observations = make(map[int64]time.Time)
	}
	if started.Before(s.observations[id]) {
		return
	}
	s.observations[id] = started
	if s.statuses == nil {
		s.statuses = make(map[int64]service.AstraRouteStatus)
	}
	checked := time.Now()
	host := ""
	if route != nil && route.cookie.Value != "" {
		host = astraRoutingHost(route.cookie.Value)
		s.observeGatewaySource(host, id, qualified, checked)
		if host != "" {
			history = &service.AstraGatewayHistoryRecord{Gateway: host, SourceAccountID: id, LastSeen: checked, LastReason: reason}
		}
	} else if prior, ok := s.statuses[id]; ok {
		host = prior.Gateway
	}
	s.statuses[id] = service.AstraRouteStatus{AccountID: id, State: "rejected", Reason: reason, CheckedAt: &checked, Gateway: host}
	if !qualified || (route != nil && route.cookie.Value == "") {
		delete(s.routes, id)
		slog.Info("codex_gateway_pin_source_rejected", "source_account_id", id)
		return
	}
	if route == nil || !time.Now().Before(route.expires) {
		return
	}
	if s.routes == nil {
		s.routes = make(map[int64]codexGatewayRoute)
	}
	// A newly qualified route replaces the prior sample; an identical cookie
	// must never gain lifetime from being replayed.
	if old, ok := s.routes[id]; ok && time.Now().Before(old.expires) && old.cookie.Value == route.cookie.Value {
		if (!s.config.IPAffinity || old.proxy == route.proxy) && old.node.Identity == route.node.Identity {
			return
		}
		// A changed egress needs revalidation without extending Cookie life.
		if old.expires.Before(route.expires) {
			route.expires = old.expires
		}
	}
	s.routes[id] = *route
	if s.nodeStore == nil {
		s.nodeStore = NewAstraNodeStore()
	}
	s.nodeStore.Upsert(route.cookie.Value, id, astraRoutingHost(route.cookie.Value), route.expires, time.Now())
	slog.Info("codex_gateway_pin_source_ready", "source_account_id", id, "expires_at", route.expires)
}

func (s *codexGatewayPinUpstream) currentCookie(path string, now time.Time) (*http.Cookie, int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pick := func(id int64) *http.Cookie {
		route, ok := s.routes[id]
		if !ok {
			return nil
		}
		if !now.Before(route.expires) {
			status := s.statuses[id]
			status.AccountID = id
			status.State = "expired"
			status.Reason = "route_expired"
			expiry := route.expires
			status.ExpiresAt = &expiry
			if s.statuses == nil {
				s.statuses = make(map[int64]service.AstraRouteStatus)
			}
			s.statuses[id] = status
			delete(s.routes, id)
			return nil
		}
		if !codexGatewayCookiePathMatches(path, route.cookie.Path) {
			return nil
		}
		cookie := route.cookie
		return &cookie
	}
	if cookie := pick(s.activeSource); cookie != nil {
		return cookie, s.activeSource
	}
	for _, id := range s.config.SourceAccountIDs {
		if cookie := pick(id); cookie != nil {
			s.activeSource = id
			return cookie, id
		}
	}
	s.activeSource = 0
	return nil, 0
}

func replaceCodexGatewayCookie(req *http.Request, cookie *http.Cookie) {
	var kept []string
	for _, header := range req.Header.Values("Cookie") {
		for _, part := range strings.Split(header, ";") {
			part = strings.TrimSpace(part)
			name, _, ok := strings.Cut(part, "=")
			if part != "" && (!ok || strings.TrimSpace(name) != "__oailb") {
				kept = append(kept, part)
			}
		}
	}
	kept = append(kept, (&http.Cookie{Name: "__oailb", Value: cookie.Value}).String())
	req.Header.Set("Cookie", strings.Join(kept, "; "))
}

// The observer forwards bytes unchanged and buffers at most one bounded SSE
// event. Early close, malformed/oversized events, model mismatch and stream errors
// cannot qualify a route. Model labels are not a capability benchmark.
type codexGatewayProbeBody struct {
	mu sync.Mutex
	io.ReadCloser
	pending  string
	data     string
	done     bool
	mismatch bool
	text     bool
	onResult func(bool)
}

func (b *codexGatewayProbeBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.done {
		b.pending += string(p[:n])
		for !b.done {
			line, rest, ok := strings.Cut(b.pending, "\n")
			if !ok {
				break
			}
			b.pending = rest
			line = strings.TrimSuffix(line, "\r")
			if line == "" {
				b.event()
			} else if strings.HasPrefix(line, "data:") {
				b.data += strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " ") + "\n"
				// Codex account-test readers stop on the terminal data line.
				// Process a complete JSON event immediately; fragmented/multiline
				// JSON still waits until it becomes complete or reaches a delimiter.
				if gjson.Valid(strings.TrimSpace(b.data)) {
					b.event()
				}
			}
			if len(b.data) > 1<<20 {
				b.finish(false)
			}
		}
		if len(b.pending) > 1<<20 {
			b.finish(false)
		}
		if err != nil && !b.done {
			b.finish(false)
		}
	}
	return n, err
}

func (b *codexGatewayProbeBody) finish(ok bool) {
	if b.done {
		return
	}
	b.done = true
	b.pending = ""
	b.data = ""
	b.onResult(ok)
}

func (b *codexGatewayProbeBody) event() {
	data := strings.TrimSpace(b.data)
	b.data = ""
	if data == "" {
		return
	}
	if data == "[DONE]" {
		b.finish(false)
		return
	}
	if !gjson.Valid(data) {
		b.finish(false)
		return
	}
	e := gjson.Parse(data)
	model := e.Get("response.model").String()
	if model != "" && model != "gpt-6-astra" {
		b.mismatch = true
	}
	switch e.Get("type").String() {
	case "response.output_text.delta":
		delta := e.Get("delta").String()
		b.text = b.text || strings.TrimSpace(delta) != ""
	case "error", "response.failed", "response.incomplete", "response.cancelled", "response.canceled":
		b.finish(false)
	case "response.completed", "response.done":
		for _, item := range e.Get("response.output").Array() {
			for _, c := range item.Get("content").Array() {
				if c.Get("type").String() == "output_text" && strings.TrimSpace(c.Get("text").String()) != "" {
					b.text = true
				}
			}
		}
		b.finish(!b.mismatch && model == "gpt-6-astra" && e.Get("response.status").String() == "completed" && b.text && !e.Get("response.error").IsObject())
	}
}

func (b *codexGatewayProbeBody) Close() error {
	err := b.ReadCloser.Close()
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.done {
		b.finish(false)
	}
	return err
}

// CodexGatewayPinWSHeader lets the private WS experiment use the same qualified
// routing pool at handshake time. It does not capture or share WS response IDs.
func (s *codexGatewayPinUpstream) CodexGatewayPinWSHeader(accountID int64) (string, error) {
	if !s.config.Enabled || !slices.Contains(s.config.TargetAccountIDs, accountID) {
		return "", nil
	}
	cookie, _ := s.currentCookie("/backend-api/codex/responses", time.Now())
	if cookie == nil {
		return "", errCodexGatewayPinUnavailable
	}
	return cookie.Value, nil
}

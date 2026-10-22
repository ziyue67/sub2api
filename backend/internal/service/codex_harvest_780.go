package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"
)

func codexTicketLifetime(length int) time.Duration {
	if length == 780 {
		return openAICodexCredentialTTL
	}
	return time.Hour - 30*time.Second
}

var codex780GatewayRE = regexp.MustCompile(`(?:^|[.])(?:chat\.)?gateway\.(unified-[0-9]{1,5})(?:[.]|$)`)

var codex780TargetRE = regexp.MustCompile(`^(?:chat\.gateway\.)?(unified-[0-9]{1,5})(?:\.api\.openai\.com)?$`)

var codex780GatewayAliasRE = regexp.MustCompile("^(?:unified[-_.]?)?([0-9]{1,5})$")

func normalizeCodex780Gateway(target string) string {
	target = strings.ToLower(strings.TrimSpace(target))
	if target == "*" || target == "any" {
		return "any"
	}
	if match := codex780GatewayAliasRE.FindStringSubmatch(target); len(match) == 2 {
		return "unified-" + match[1]
	}
	if match := codex780TargetRE.FindStringSubmatch(target); len(match) == 2 {
		return match[1]
	}
	return target
}

func codex780GatewayAllowed(actual, target string) bool {
	return actual != "" && (target == "any" || actual == target)
}

func codex780CookieGateway(cookies []string) string {
	for _, cookie := range cookies {
		name, value, _ := strings.Cut(cookie, "=")
		if name != "__oailb" {
			continue
		}
		parts := strings.Split(value, ".")
		if len(parts) != 3 {
			return ""
		}
		raw, err := base64.RawURLEncoding.DecodeString(parts[1])
		if err != nil {
			return ""
		}
		match := codex780GatewayRE.FindSubmatch(raw)
		if len(match) == 2 {
			return string(match[1])
		}
	}
	return ""
}

// Only the two LB cookies are retained. JWT claims are routing hints, not verified identity.
func codex780Route(cookies []string, target string, now time.Time) ([]string, time.Time, error) {
	target = normalizeCodex780Gateway(target)
	bad := mintRouteError("route_cookie_invalid")
	pair := map[string]string{}
	for _, cookie := range cookies {
		name, value, ok := strings.Cut(cookie, "=")
		if name != "__cflb" && name != "__oailb" {
			continue
		}
		if !ok || value == "" || len(value) > 4096 || strings.ContainsAny(value, ";,\r\n\t ") || pair[name] != "" {
			return nil, time.Time{}, bad
		}
		for _, c := range value {
			if c < 33 || c > 126 {
				return nil, time.Time{}, bad
			}
		}
		pair[name] = value
	}
	if target == "" {
		return nil, time.Time{}, mintRouteError("route_target_missing")
	}
	if pair["__cflb"] == "" && pair["__oailb"] == "" {
		return nil, time.Time{}, mintRouteError("route_pair_missing")
	}
	if pair["__cflb"] == "" {
		return nil, time.Time{}, mintRouteError("route_cflb_missing")
	}
	if pair["__oailb"] == "" {
		return nil, time.Time{}, mintRouteError("route_oailb_missing")
	}
	parts := strings.Split(pair["__oailb"], ".")
	if len(parts) != 3 {
		return nil, time.Time{}, bad
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, time.Time{}, bad
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if json.Unmarshal(raw, &claims) != nil || claims.Exp <= 0 || claims.Exp >= 4102444800 {
		return nil, time.Time{}, mintRouteError("route_expiry_invalid")
	}
	if claims.Exp <= now.Unix() {
		return nil, time.Time{}, mintRouteError("route_pair_expired")
	}
	match := codex780GatewayRE.FindSubmatch(raw)
	if target != "any" && len(match) != 2 {
		return nil, time.Time{}, mintRouteError("route_gateway_unknown")
	}
	if target != "any" && !codex780GatewayAllowed(string(match[1]), target) {
		return nil, time.Time{}, mintRouteError("route_gateway_mismatch: got=" + string(match[1]) + " want=" + target)
	}
	return []string{"__cflb=" + pair["__cflb"], "__oailb=" + pair["__oailb"]}, time.Unix(claims.Exp, 0), nil
}

type codex780SeedContextKey struct{}
type codex780Seed struct {
	state   string
	cookies []string
}

func (s *OpenAIGatewayService) requestCodex780Probe(ctx context.Context, account *Account, token, model, proxy string, reserve func() bool, session string) (out codexHarvestProbeResult) {
	// Self collection has no seed. Fast reuse is an explicit candidate, and
	// only the new state issued to the target may enter its account store.
	controls, _ := s.harvestControls(ctx)
	seed, borrowed := ctx.Value(codex780SeedContextKey{}).(codex780Seed)
	out = s.requestCodex780Shot(ctx, account, token, model, proxy, reserve, session, seed.state, seed.cookies, controls)
	if borrowed && out.Err == nil && out.State == seed.state {
		out.Err = &codexMintError{kind: "probe_inconclusive", detail: "reuse did not issue a target-owned state"}
		return
	}
	out.Shape, out.Kind = classifyCodexHarvestProbe(ctx, account, s.openAICodexTicketConfig(), out)
	if out.Kind != "success" {
		return
	}
	continuation := s.requestCodex780Shot(ctx, account, token, model, proxy, reserve, session, out.State, out.Cookies, controls)
	if !continuation.Sent {
		out.Err = &codexMintError{kind: "probe_inconclusive", detail: "ticket probe not admitted"}
		if ctx.Err() != nil {
			out.Err = mintTransportError(ctx.Err())
		}
		return
	}
	// Classify transport and stream failures before comparing ticket headers.
	if continuation.Err != nil || (continuation.Status != http.StatusOK && continuation.Status != http.StatusSwitchingProtocols) {
		out.Err, out.Status, out.RetryAfter = continuation.Err, continuation.Status, continuation.RetryAfter
		return
	}
	if continuation.State != "" && continuation.State != out.State {
		out.Err = &codexMintError{kind: "probe_reissued", detail: "ticket probe issued a different state"}
		return
	}
	if !slices.Equal(continuation.Cookies, out.Cookies) {
		out.Err = &codexMintError{kind: "probe_route_changed", detail: "ticket probe changed the route pair"}
		return
	}
	out.ProbeVerified = true
	return
}

func (s *OpenAIGatewayService) requestCodex780Shot(ctx context.Context, account *Account, token, model, proxy string, reserve func() bool, session, state string, seed []string, controls CodexHarvestControls) (out codexHarvestProbeResult) {
	ctx = context.WithValue(ctx, openAICodexStateProbeContextKey{}, true)
	if edge, ok := ctx.Value(codexMintEdgeContextKey{}).(string); ok {
		controls.EdgeIP = edge
	}
	if err := validateCodexMintEdgeIP(controls.EdgeIP); err != nil {
		out.Err = err
		return
	}
	// Gateway claims describe routing, not ticket quality.
	target := "any"
	out.Gateway = target
	out.Transport = "sse"
	out.EdgeIP = controls.EdgeIP
	payload := []byte(`{"model":` + jsonString(model) + `,"instructions":"Reply with exactly OK. Do not call tools.","stream":true,"store":false,"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"Reply OK"}]}],"parallel_tool_calls":false}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, chatgptCodexURL, bytes.NewReader(payload))
	if err != nil {
		out.Err = errors.New("cannot construct mint request")
		return
	}
	req = req.WithContext(WithHTTPUpstreamProfile(req.Context(), HTTPUpstreamProfileOpenAIHarvest))
	req.Close = true
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("session-id", session)
	req.Header.Set("session_id", session)
	if state != "" {
		req.Header.Set(openAICodexTurnStateHeader, state)
	}
	// Share the canonical client identity and model version floor with state
	// probes instead of pinning harvest traffic to a separate UA/version.
	applyOpenAICodexTicketHarvestIdentity(req.Header, model)
	if err = resolveAndSetOpenAIChatGPTAccountHeaders(ctx, s.accountRepo, req.Header, account); err != nil {
		out.Err = errors.New("account identity unavailable")
		return
	}
	if len(seed) > 0 {
		req.Header.Set("Cookie", strings.Join(seed, "; "))
	}
	if ctx.Err() != nil || (reserve != nil && !reserve()) {
		out.Err = errors.New("mint request no longer admitted")
		return
	}
	out.Sent = true
	if controls.Transport == "websocket" {
		return requestCodex780WS(req, proxy, controls.EdgeIP, payload, seed, target, model)
	}
	var resp *http.Response
	if controls.EdgeIP != "" {
		var client *http.Client
		client, req, err = codexMintHTTPClient(req, proxy, controls.EdgeIP)
		if err == nil {
			defer client.CloseIdleConnections()
			resp, err = client.Do(req)
		}
	} else {
		resp, err = s.httpUpstream.Do(req, proxy, account.ID, account.Concurrency)
	}
	if err != nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		out.Err = mintTransportError(err)
		return
	}
	if resp == nil {
		out.Err = errors.New("mint response missing")
		return
	}
	out.Status = resp.StatusCode
	out.RetryAfter = codexHarvestRetryAfter(resp.Header.Get("Retry-After"), time.Now())
	if resp.Body == nil {
		out.Err = errors.New("mint response body missing")
		return
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return
	}
	out.State = extractOpenAICodexTurnState(resp.Header)
	modelErr := readCodexHarvestComplete(resp.Body)
	out.Cookies, err = codex780ResponseRoute(resp, seed, target, time.Now())
	var eventErr *codexMintError
	if errors.As(modelErr, &eventErr) && (eventErr.terminal || eventErr.kind == "rate_limited") {
		out.Err = modelErr // Error-only responses need not issue a route pair.
		return
	}
	if err != nil {
		out.Err = err
		return
	}
	out.Gateway = codex780CookieGateway(out.Cookies)
	if out.Gateway == "" {
		out.Gateway = "any"
	}
	out.Err = modelErr
	return
}

// Model names and response.created alone are not acceptance evidence. Require
// a complete, bounded stream, preserving only controlled error categories.
func readCodexHarvestComplete(body io.Reader) error {
	raw, err := io.ReadAll(io.LimitReader(body, openAICodexStateProbeMaxBody+1))
	if err != nil {
		return mintTransportError(err)
	}
	if len(raw) > openAICodexStateProbeMaxBody {
		return &codexMintError{kind: "response_incomplete_or_error", detail: "ticket probe response too large"}
	}
	// SSE permits a BOM and CR, CRLF or LF line endings.
	raw = bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf"))
	raw = bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n"))
	raw = bytes.ReplaceAll(raw, []byte("\r"), []byte("\n"))
	for _, event := range openAICodexStateStreamEvents(raw) {
		if err := codex780EventError(event, ""); err != nil {
			return err
		}
	}
	if err := validateCodexProbeResponse(raw); err != nil {
		return &codexMintError{kind: "response_incomplete_or_error", detail: "ticket probe did not complete"}
	}
	return nil
}

func codex780ResponseRoute(resp *http.Response, seed []string, target string, now time.Time) ([]string, error) {
	var incoming []string
	for _, cookie := range resp.Cookies() {
		if cookie.Name != "__cflb" && cookie.Name != "__oailb" {
			continue
		}
		if cookie.Value == "" || cookie.MaxAge < 0 || (cookie.MaxAge == 0 && !cookie.Expires.IsZero() && !now.Before(cookie.Expires)) {
			return nil, mintRouteError("route_cookie_deleted")
		}
		incoming = append(incoming, cookie.Name+"="+cookie.Value)
	}
	if len(incoming) == 0 {
		incoming = seed
	}
	// A changed pair must be complete; never combine old and new halves.
	clean, _, err := codex780Route(incoming, target, now)
	return clean, err
}

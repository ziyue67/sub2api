package repository

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type unifiedAstraProvider struct {
	service.AstraTicketLifecycle
	enabled                             bool
	calls, released, prepared, verified int
	err                                 error
	mutateOnFailure                     bool
}

func (p *unifiedAstraProvider) UnifiedAstraTicketsEnabled(context.Context) bool { return p.enabled }
func (p *unifiedAstraProvider) PrepareStoredAstraSources(context.Context) error {
	p.prepared++
	return p.err
}
func (p *unifiedAstraProvider) VerifyStoredAstraTarget(context.Context, int64) error {
	p.verified++
	return p.err
}
func (p *unifiedAstraProvider) StoredAstraSnapshot(context.Context) service.AstraGatewayRuntime {
	return service.AstraGatewayRuntime{ReadyRoutes: 1, Targets: []service.AstraRouteStatus{{AccountID: 300, State: "ready", Reason: "target_probe_passed"}}}
}
func (p *unifiedAstraProvider) ApplyStoredAstraTarget(_ context.Context, h http.Header, _ int64, protocol string) (string, func(), error) {
	p.calls++
	if p.err != nil {
		if p.mutateOnFailure {
			h.Set("Cookie", "partial-borrow")
			h.Set("Authorization", "partial-auth")
		}
		return "", func() {}, p.err
	}
	h.Set("Cookie", "__cflb=target-lb; __oailb=target-route")
	h.Set("X-Codex-Turn-State", "verified-target-"+protocol)
	return "http://verified-exit.invalid", func() { p.released++ }, nil
}

func TestUnifiedAstraAdapterForwardsOneCompleteBundleAndReleases(t *testing.T) {
	for _, failure := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete", true: "transport_error"}[failure], func(t *testing.T) {
			p := &unifiedAstraProvider{enabled: true}
			cfg := &config.Config{}
			cfg.Gateway.CodexGatewayPin = pinConfig()
			calls := 0
			s := &astraRoutingUpstream{cfg: cfg}
			s.SetAstraTicketLifecycle(p)
			s.delegate = gatewayPinDelegate{call: func(r *http.Request, proxy string, id int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
				calls++
				require.Equal(t, int64(300), id)
				require.Equal(t, "http://verified-exit.invalid", proxy)
				require.Equal(t, "verified-target-sse", r.Header.Get("X-Codex-Turn-State"))
				require.Equal(t, "__cflb=target-lb; __oailb=target-route", r.Header.Get("Cookie"))
				require.Equal(t, "Bearer target-token", r.Header.Get("Authorization"))
				if failure {
					return nil, errors.New("fixture failure")
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(http.NoBody)}, nil
			}}
			// A stale legacy source must not be prepared or mixed with the bundle.
			s.SetAstraGatewayPreparer(func(context.Context, int64) error { t.Fatal("legacy source collection ran"); return nil })
			r := pinRequest(t)
			r.Header.Set("Authorization", "Bearer target-token")
			r.Header.Set("Cookie", "__cflb=old; __oailb=old")
			response, err := s.Do(r, "old-exit", 300, 1)
			if failure {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Zero(t, p.released)
				require.NoError(t, response.Body.Close())
				require.NoError(t, response.Body.Close())
			}
			require.Equal(t, 1, calls)
			require.Equal(t, 1, p.calls)
			require.Equal(t, 1, p.released)
		})
	}
}

func TestUnifiedAstraAdapterKeepsDiagnosticsSeparateFromForwarding(t *testing.T) {
	p := &unifiedAstraProvider{enabled: true}
	cfg := &config.Config{}
	cfg.Gateway.CodexGatewayPin = pinConfig()
	s := &astraRoutingUpstream{cfg: cfg, tickets: p, delegate: gatewayPinDelegate{call: func(*http.Request, string, int64, int, *tlsfingerprint.Profile) (*http.Response, error) {
		return pinResponse(""), nil
	}}}
	require.NoError(t, s.PrepareAstraGateway(t.Context()))
	require.Equal(t, 1, p.prepared)
	require.NoError(t, s.VerifyAstraGatewayTarget(t.Context(), pinRequest(t), "", 300, 1))
	require.Equal(t, 1, p.verified)
	require.Equal(t, 1, s.AstraGatewaySnapshot(t.Context()).ReadyRoutes)
	p.err = errors.New("target_probe_failed")
	resp, err := s.Do(pinRequest(t), "", 300, 1)
	consumeAffinityResponse(t, resp, err)
	require.Zero(t, p.released)
}

func TestUnifiedAstraAdapterWebSocketReplacesWholeBundle(t *testing.T) {
	p := &unifiedAstraProvider{enabled: true}
	cfg := &config.Config{}
	cfg.Gateway.CodexGatewayPin = pinConfig()
	s := &astraRoutingUpstream{cfg: cfg, tickets: p}
	h := http.Header{}
	h.Set("Cookie", "__cflb=old; __oailb=old")
	route, proxy, release, err := s.CodexGatewayPinWSRequest(t.Context(), h, "old-exit", 300, 1)
	require.NoError(t, err)
	require.Empty(t, route, "caller must not overwrite the full bundle with a separate cookie")
	require.Equal(t, "http://verified-exit.invalid", proxy)
	require.Equal(t, "verified-target-websocket", h.Get("X-Codex-Turn-State"))
	require.Equal(t, "__cflb=target-lb; __oailb=target-route", h.Get("Cookie"))
	release()
	require.Equal(t, 1, p.released)
}

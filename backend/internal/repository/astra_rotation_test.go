package repository

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/mihomo"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func rotationFixture(t *testing.T, limit int, successNode string) (*astraRoutingUpstream, *[]string, *int) {
	t.Helper()
	cfg := &config.Config{}
	cfg.Gateway.CodexGatewayPin = pinConfig()
	cfg.Gateway.CodexGatewayPin.RotateNodes = true
	cfg.Gateway.CodexGatewayPin.IPAffinity = true
	cfg.Gateway.CodexGatewayPin.MaxNodeAttempts = limit
	nodes := []mihomo.AstraNode{{ID: "bad", Identity: "bad", Name: "US fixture", Country: "US"}, {ID: "good", Identity: "good", Name: "SG fixture", Country: "SG"}, {ID: "third", Identity: "third", Name: "JP fixture", Country: "JP"}}
	var calls []string
	leases := 0
	active := map[string]string{}
	serial := 0
	wrapper := &astraRoutingUpstream{cfg: cfg, listNodes: func() ([]mihomo.AstraNode, error) { return nodes, nil }}
	wrapper.pinNode = func(ctx context.Context, node mihomo.AstraNode) (string, func(), error) {
		require.NoError(t, ctx.Err())
		serial++
		p := fmt.Sprintf("http://127.0.0.1:%d", 19000+serial)
		leases++
		active[p] = node.ID
		return p, func() {
			if _, ok := active[p]; ok {
				delete(active, p)
				leases--
			}
		}, nil
	}
	wrapper.delegate = gatewayPinDelegate{call: func(r *http.Request, proxy string, id int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
		node, ok := active[proxy]
		require.True(t, ok, "lease must outlive probe and business response")
		require.Equal(t, service.HTTPUpstreamProfileOpenAIHarvest, service.HTTPUpstreamProfileFromContext(r.Context()))
		calls = append(calls, fmt.Sprintf("%s:%d", node, id))
		if id == 299 {
			return pinResponse("__oailb=" + node + "; Path=/; Secure; Max-Age=230"), nil
		}
		require.Equal(t, int64(300), id)
		require.Equal(t, "Bearer target-only", r.Header.Get("Authorization"))
		cookie, err := r.Cookie("__oailb")
		require.NoError(t, err)
		require.Equal(t, node, cookie.Value)
		resp := pinResponse("")
		if node != successNode && r.Header.Get("X-Codex-Turn-State") != "" {
			resp.Header.Set("X-Codex-Turn-State", "different-ticket")
		}
		return resp, nil
	}}
	wrapper.SetAstraGatewayPreparer(func(ctx context.Context, id int64) error {
		req := pinRequest(t).WithContext(service.WithAstraSourceAcquisition(ctx))
		resp, err := wrapper.Do(req, "account-proxy", id, 1)
		if err != nil {
			return err
		}
		defer func() { _ = resp.Body.Close() }()
		_, err = io.Copy(io.Discard, resp.Body)
		return err
	})
	return wrapper, &calls, &leases
}
func rotationTarget(t *testing.T) *http.Request {
	r := pinRequest(t)
	r.Header.Set("Authorization", "Bearer target-only")
	return r
}

func TestAstraGatewayRotationRejectsBadNodeAndPinsSuccessfulNode(t *testing.T) {
	s, calls, leases := rotationFixture(t, 3, "good")
	resp, err := s.Do(rotationTarget(t), "target-original", 300, 1)
	require.NoError(t, err)
	require.Equal(t, 1, *leases, "actual response still owns node lease")
	consumeAffinityResponse(t, resp, err)
	require.Zero(t, *leases)
	require.Equal(t, []string{"bad:299", "bad:300", "bad:300", "good:299", "good:300", "good:300", "good:300"}, *calls)
	snapshot := s.AstraGatewaySnapshot(t.Context())
	require.Equal(t, 1, snapshot.ReadyRoutes)
	require.Equal(t, "SG", snapshot.Targets[0].ProxyCountry)
	require.Equal(t, "SG fixture", snapshot.Targets[0].ProxyNode)
	// A new private listener for the same node must reuse qualification, not credentials.
	resp, err = s.Do(rotationTarget(t), "target-original", 300, 1)
	consumeAffinityResponse(t, resp, err)
	require.Len(t, *calls, 8)
	cookie, proxy, release, err := s.CodexGatewayPinWSRequest(t.Context(), rotationTarget(t).Header, "target-original", 300, 1)
	require.NoError(t, err)
	require.Equal(t, "good", cookie)
	require.Contains(t, proxy, "127.0.0.1")
	require.Equal(t, 1, *leases)
	release()
	require.Zero(t, *leases)
	require.Len(t, *calls, 8, "WS reuses node qualification while owning its own dial lease")
	// Expiration picks the next eligible exit rather than extending old Cookie lifetime.
	pool := s.current(t.Context())
	route := pool.routes[299]
	route.expires = time.Now().Add(-time.Second)
	pool.routes[299] = route
	resp, err = s.Do(rotationTarget(t), "target-original", 300, 1)
	consumeAffinityResponse(t, resp, err)
}
func TestAstraGatewayRotationBoundedAndFailsClosed(t *testing.T) {
	s, calls, leases := rotationFixture(t, 2, "never")
	_, err := s.Do(rotationTarget(t), "target-original", 300, 1)
	require.ErrorContains(t, err, "astra_rotation_exhausted")
	require.Equal(t, []string{"bad:299", "bad:300", "bad:300", "good:299", "good:300", "good:300"}, *calls)
	require.Zero(t, *leases)
	require.Zero(t, s.AstraGatewaySnapshot(t.Context()).ReadyRoutes)
	_, err = s.Do(rotationTarget(t), "target-original", 300, 1)
	require.ErrorContains(t, err, "source_probe_cooldown")
	require.Len(t, *calls, 6)
}
func TestAstraGatewayRotationCancellationAndUnavailablePool(t *testing.T) {
	s, calls, leases := rotationFixture(t, 3, "good")
	s.listNodes = func() ([]mihomo.AstraNode, error) { return nil, fmt.Errorf("astra_rotation_no_nodes") }
	_, err := s.Do(rotationTarget(t), "target-original", 300, 1)
	require.ErrorContains(t, err, "astra_rotation_no_nodes")
	require.Empty(t, *calls)
	require.Zero(t, *leases)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = s.Do(rotationTarget(t).WithContext(ctx), "target-original", 300, 1)
	require.Error(t, err)
	require.Empty(t, *calls)
}

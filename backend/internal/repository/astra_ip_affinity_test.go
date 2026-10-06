package repository

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

func consumeAffinityResponse(t *testing.T, resp *http.Response, err error) {
	t.Helper()
	require.NoError(t, err)
	require.NotNil(t, resp)
	_, err = io.Copy(io.Discard, resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
}

func TestAstraGatewayIPAffinity(t *testing.T) {
	for _, tc := range []struct {
		name         string
		enabled      bool
		source, want string
	}{
		{"off", false, "http://source.invalid:80", "http://target.invalid:80"},
		{"on", true, "http://source.invalid:80", "http://source.invalid:80"},
		{"direct", true, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{}
			cfg.Gateway.CodexGatewayPin = pinConfig()
			cfg.Gateway.CodexGatewayPin.IPAffinity = tc.enabled
			calls := 0
			wrapper := &astraRoutingUpstream{cfg: cfg}
			wrapper.delegate = gatewayPinDelegate{call: func(r *http.Request, proxy string, id int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
				if id == 299 {
					return pinResponse("__oailb=source-route; Path=/; Secure; Max-Age=230"), nil
				}
				require.Equal(t, int64(300), id)
				require.Equal(t, tc.want, proxy)
				require.Equal(t, "Bearer target-test", r.Header.Get("Authorization"))
				require.Equal(t, "target-account", r.Header.Get("ChatGPT-Account-ID"))
				cookie, err := r.Cookie("__oailb")
				require.NoError(t, err)
				require.Equal(t, "source-route", cookie.Value)
				calls++
				return pinResponse(""), nil
			}}
			resp, err := wrapper.Do(pinRequest(t), tc.source, 299, 1)
			consumeAffinityResponse(t, resp, err)
			target := pinRequest(t)
			target.Header.Set("Authorization", "Bearer target-test")
			target.Header.Set("ChatGPT-Account-ID", "target-account")
			resp, err = wrapper.Do(target, "http://target.invalid:80", 300, 1)
			consumeAffinityResponse(t, resp, err)
			require.Equal(t, 3, calls, "validation and actual request must share egress")
			cookie, proxy, release, err := wrapper.CodexGatewayPinWSRequest(t.Context(), target.Header, "http://target.invalid:80", 300, 1)
			require.NoError(t, err)
			release()
			require.Equal(t, "source-route", cookie)
			require.Equal(t, tc.want, proxy, "WS must dial the validated egress")
			require.Equal(t, 3, calls, "HTTP and WS can reuse the same validated identity")
			raw, err := json.Marshal(wrapper.AstraGatewaySnapshot(t.Context()))
			require.NoError(t, err)
			require.NotContains(t, string(raw), "source.invalid")
		})
	}
}

func TestAstraGatewayIPAffinityRevalidatesChangedEgress(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.CodexGatewayPin = pinConfig()
	cfg.Gateway.CodexGatewayPin.IPAffinity = true
	wrapper := &astraRoutingUpstream{cfg: cfg}
	var proxies []string
	wrapper.delegate = gatewayPinDelegate{call: func(_ *http.Request, p string, id int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
		if id == 299 {
			return pinResponse("__oailb=same-cookie; Path=/; Secure; Max-Age=230"), nil
		}
		proxies = append(proxies, p)
		return pinResponse(""), nil
	}}
	resp, err := wrapper.Do(pinRequest(t), "old-source", 299, 1)
	consumeAffinityResponse(t, resp, err)
	resp, err = wrapper.Do(pinRequest(t), "target", 300, 1)
	consumeAffinityResponse(t, resp, err)
	pool := wrapper.current(t.Context())
	expiry := pool.routes[299].expires
	require.Equal(t, "ready", wrapper.AstraGatewaySnapshot(t.Context()).Targets[0].State)
	resp, err = wrapper.Do(pinRequest(t), "new-source", 299, 1)
	consumeAffinityResponse(t, resp, err)
	require.Equal(t, expiry, pool.routes[299].expires)
	require.Equal(t, "waiting", wrapper.AstraGatewaySnapshot(t.Context()).Targets[0].State)
	resp, err = wrapper.Do(pinRequest(t), "target", 300, 1)
	consumeAffinityResponse(t, resp, err)
	require.Equal(t, []string{"old-source", "old-source", "old-source", "new-source", "new-source", "new-source"}, proxies)
	// A backup source must bring its own egress along with its Cookie.
	backup := codexGatewayRoute{cookie: http.Cookie{Name: "__oailb", Value: "backup", Path: "/"}, proxy: "backup-source", expires: time.Now().Add(time.Minute)}
	pool.recordSource(298, time.Now(), &backup, true)
	pool.recordSource(299, time.Now(), nil, false)
	resp, err = wrapper.Do(pinRequest(t), "target", 300, 1)
	consumeAffinityResponse(t, resp, err)
	require.Equal(t, []string{"backup-source", "backup-source", "backup-source"}, proxies[6:])
	// Turning affinity off publishes a fresh pool without stale source proxies.
	cfg.Gateway.CodexGatewayPin.IPAffinity = false
	require.NotSame(t, pool, wrapper.current(t.Context()))
	resp, err = wrapper.Do(pinRequest(t), "new-source", 299, 1)
	consumeAffinityResponse(t, resp, err)
	resp, err = wrapper.Do(pinRequest(t), "target", 300, 1)
	consumeAffinityResponse(t, resp, err)
	require.Equal(t, []string{"target", "target", "target"}, proxies[9:])
}

func TestAstraGatewayIPAffinityRejectsFailedTarget(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.CodexGatewayPin = pinConfig()
	cfg.Gateway.CodexGatewayPin.IPAffinity = true
	calls := 0
	wrapper := &astraRoutingUpstream{cfg: cfg, delegate: gatewayPinDelegate{call: func(_ *http.Request, proxy string, id int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
		if id == 299 {
			return pinResponse("__oailb=route; Path=/; Secure; Max-Age=230"), nil
		}
		require.Equal(t, "source", proxy)
		calls++
		return &http.Response{StatusCode: 403, Body: http.NoBody}, nil
	}}}
	resp, err := wrapper.Do(pinRequest(t), "source", 299, 1)
	consumeAffinityResponse(t, resp, err)
	_, err = wrapper.Do(pinRequest(t), "target", 300, 1)
	require.Error(t, err)
	require.Equal(t, 1, calls, "do not forward actual target request after failure")
	_, _, _, err = wrapper.CodexGatewayPinWSRequest(t.Context(), http.Header{}, "target", 300, 1)
	require.Error(t, err)
	require.Equal(t, 1, calls, "failed validation stays in cooldown")
}

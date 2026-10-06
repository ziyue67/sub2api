package repository

import (
	"context"
	"crypto/sha256"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAstraReadinessRequiresCurrentTargetPass(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.CodexGatewayPin = pinConfig()
	wrapper := &astraRoutingUpstream{cfg: cfg}
	pool := wrapper.current(t.Context())
	now := time.Now()
	route := codexGatewayRouteFromResponse(pinResponse("__oailb=first; Path=/; Secure; Max-Age=3600"), "/backend-api/codex/responses", now)
	pool.recordSource(299, now, route, true)
	snapshot := wrapper.AstraGatewaySnapshot(t.Context())
	require.Zero(t, snapshot.ReadyRoutes)
	require.Equal(t, "candidate", snapshot.Sources[0].State)
	require.Nil(t, snapshot.Sources[0].ExpiresAt)
	require.Zero(t, snapshot.Sources[0].RemainingSeconds)
	pool.targetChecks = map[int64]astraTargetValidation{300: {cookieFingerprint: sha256.Sum256([]byte("first")), sourceID: 299, passed: false, checked: now, expires: route.expires, reason: "target_probe_degraded"}}
	snapshot = wrapper.AstraGatewaySnapshot(t.Context())
	require.Zero(t, snapshot.ReadyRoutes)
	require.Equal(t, "source_passed_target_failed", snapshot.Sources[0].Reason)
	require.Nil(t, snapshot.Sources[0].ExpiresAt)
	require.Equal(t, "target_probe_degraded", snapshot.Targets[0].Reason)
	require.Nil(t, snapshot.Targets[0].ExpiresAt)
	require.Zero(t, snapshot.Targets[0].RemainingSeconds)
	check := pool.targetChecks[300]
	check.passed = true
	pool.targetChecks[300] = check
	snapshot = wrapper.AstraGatewaySnapshot(t.Context())
	require.Equal(t, 1, snapshot.ReadyRoutes)
	require.NotNil(t, snapshot.Sources[0].ExpiresAt)
	require.NotNil(t, snapshot.Targets[0].ExpiresAt)
	// A newer cookie on the same gateway/source must not inherit an older pass.
	next := *route
	next.cookie.Value = "second"
	pool.recordSource(299, now.Add(time.Second), &next, true)
	snapshot = wrapper.AstraGatewaySnapshot(t.Context())
	require.Zero(t, snapshot.ReadyRoutes)
	require.Nil(t, snapshot.Sources[0].ExpiresAt)
	require.Nil(t, snapshot.Targets[0].ExpiresAt)
}
func TestAstraReadinessSnapshotDoesNotWaitForNetwork(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.CodexGatewayPin = pinConfig()
	wrapper := &astraRoutingUpstream{cfg: cfg}
	pool := wrapper.current(t.Context())
	pool.targetProbeMu.Lock()
	defer pool.targetProbeMu.Unlock()
	done := make(chan struct{})
	go func() { wrapper.AstraGatewaySnapshot(context.Background()); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("status blocked on target network probe")
	}
}

func TestAstraSchedulingToggleRetainsPool(t *testing.T) {
	settings := config.AstraRoutingSettings{Revision: "one", CookiePool: pinConfig()}
	cfg := &config.Config{}
	cfg.SetAstraRoutingLoader(func(context.Context) config.AstraRoutingSettings { return settings })
	wrapper := &astraRoutingUpstream{cfg: cfg}
	pool := wrapper.current(t.Context())
	settings.AccountScheduling = true
	require.Same(t, pool, wrapper.current(t.Context()))
	settings.SchedulingMode = "groups"
	settings.SchedulingGroupIDs = []int64{7}
	require.Same(t, pool, wrapper.current(t.Context()))
	settings.Revision = "two"
	require.NotSame(t, pool, wrapper.current(t.Context()))
}

func TestAstraProbeTransportFailureRevokesExistingPass(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.CodexGatewayPin = pinConfig()
	fail := false
	wrapper := &astraRoutingUpstream{cfg: cfg}
	wrapper.delegate = gatewayPinDelegate{call: func(_ *http.Request, _ string, id int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
		if id == 299 {
			return pinResponse("__oailb=route; Path=/; Secure; Max-Age=230"), nil
		}
		if fail {
			return nil, errors.New("offline")
		}
		return pinResponse(""), nil
	}}
	resp, err := wrapper.Do(pinRequest(t), "", 299, 1)
	consumeAffinityResponse(t, resp, err)
	resp, err = wrapper.Do(pinRequest(t), "", 300, 1)
	consumeAffinityResponse(t, resp, err)
	require.Equal(t, 1, wrapper.AstraGatewaySnapshot(t.Context()).ReadyRoutes)
	fail = true
	req := pinRequest(t)
	err = wrapper.VerifyAstraGatewayTarget(t.Context(), req, "", 300, 1)
	require.Error(t, err)
	require.Zero(t, wrapper.AstraGatewaySnapshot(t.Context()).ReadyRoutes)
}

func TestAstraStateProbeBypassesBorrowingRecursion(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.CodexGatewayPin = pinConfig()
	calls := 0
	wrapper := &astraRoutingUpstream{cfg: cfg, delegate: gatewayPinDelegate{call: func(r *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
		calls++
		require.True(t, service.IsOpenAICodexStateProbeRequest(r.Context()))
		return pinResponse(""), nil
	}}}
	req := pinRequest(t)
	req.Header.Set("Cookie", "__oailb=explicit-route")
	result := service.ProbeOpenAICodexStateRoute(t.Context(), wrapper, req, "exit", 300, 1, nil)
	require.Equal(t, service.OpenAICodexStateHealthy, result.Verdict)
	require.Equal(t, 2, calls)
	require.Nil(t, wrapper.pool, "standalone probes must not acquire or publish borrowing routes")
}

package repository

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/mihomo"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func concurrentAstraWrapper() *astraRoutingUpstream {
	cfg := &config.Config{}
	cfg.Gateway.CodexGatewayPin = pinConfig()
	return &astraRoutingUpstream{cfg: cfg}
}
func seedConcurrentAstraRoute(t *testing.T, s *astraRoutingUpstream, value string) {
	t.Helper()
	now := time.Now()
	route := codexGatewayRouteFromResponse(pinResponse("__oailb="+value+"; Path=/; Secure; Max-Age=3600"), "/backend-api/codex/responses", now)
	s.current(t.Context()).recordSource(299, now, route, true)
}
func receiveAdmission(t *testing.T, results <-chan error) error {
	t.Helper()
	select {
	case err := <-results:
		return err
	case <-time.After(3 * time.Second):
		t.Fatal("admission did not finish")
		return nil
	}
}

func forceAstraProbe(req *http.Request) *http.Request {
	return req.WithContext(context.WithValue(req.Context(), astraForceProbeKey{}, true))
}
func TestAstraConcurrentPreparationSharesResult(t *testing.T) {
	for _, success := range []bool{true, false} {
		t.Run(map[bool]string{true: "success", false: "failure"}[success], func(t *testing.T) {
			s := concurrentAstraWrapper()
			entered, release := make(chan struct{}), make(chan struct{})
			var calls atomic.Int32
			s.SetAstraGatewayPreparer(func(context.Context, int64) error {
				if calls.Add(1) == 1 {
					close(entered)
					<-release
				}
				if success {
					seedConcurrentAstraRoute(t, s, "shared")
					return nil
				}
				return errors.New("offline")
			})
			results := make(chan error, 5)
			go func() { results <- s.PrepareAstraGateway(t.Context()) }()
			<-entered
			for range 4 {
				go func() { results <- s.PrepareAstraGateway(t.Context()) }()
			}
			select {
			case err := <-results:
				close(release)
				t.Fatalf("premature admission: %v", err)
			case <-time.After(40 * time.Millisecond):
			}
			close(release)
			for range 5 {
				err := receiveAdmission(t, results)
				if success {
					require.NoError(t, err)
				} else {
					require.EqualError(t, err, "no_qualified_source_route")
				}
			}
			if success {
				require.EqualValues(t, 1, calls.Load())
			} else {
				require.EqualValues(t, len(pinConfig().SourceAccountIDs), calls.Load())
			}
		})
	}
}
func TestAstraConcurrentPreparationWaiterCancellation(t *testing.T) {
	s := concurrentAstraWrapper()
	entered, release := make(chan struct{}), make(chan struct{})
	s.SetAstraGatewayPreparer(func(context.Context, int64) error {
		close(entered)
		<-release
		seedConcurrentAstraRoute(t, s, "shared")
		return nil
	})
	leader := make(chan error, 1)
	go func() { leader <- s.PrepareAstraGateway(t.Context()) }()
	<-entered
	ctx, cancel := context.WithCancel(t.Context())
	follower := make(chan error, 1)
	go func() { follower <- s.PrepareAstraGateway(ctx) }()
	cancel()
	require.ErrorIs(t, receiveAdmission(t, follower), context.Canceled)
	close(release)
	require.NoError(t, receiveAdmission(t, leader))
}
func TestAstraConcurrentBusinessRequestsSkipTargetProbe(t *testing.T) {
	s := concurrentAstraWrapper()
	var probes, business atomic.Int32
	s.delegate = gatewayPinDelegate{call: func(r *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
		if service.IsOpenAICodexStateProbeRequest(r.Context()) {
			probes.Add(1)
		} else {
			business.Add(1)
		}
		return pinResponse(""), nil
	}}
	seedConcurrentAstraRoute(t, s, "shared")
	results := make(chan error, 5)
	request := func() {
		resp, err := s.Do(pinRequest(t), "", 300, 5)
		if resp != nil {
			_ = resp.Body.Close()
		}
		results <- err
	}
	for range 5 {
		go request()
	}
	for range 5 {
		require.NoError(t, receiveAdmission(t, results))
	}
	require.Zero(t, probes.Load(), "business requests must not run target probes")
	require.EqualValues(t, 5, business.Load(), "each business request is sent exactly once")
}
func TestAstraTargetWaitCancellationAndRouteChange(t *testing.T) {
	for _, change := range []string{"cancel", "route", "config", "expiry"} {
		t.Run(change, func(t *testing.T) {
			s := concurrentAstraWrapper()
			entered, release := make(chan struct{}), make(chan struct{})
			var probes atomic.Int32
			s.delegate = gatewayPinDelegate{call: func(r *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
				if probes.Add(1) == 1 {
					close(entered)
					<-release
				}
				return pinResponse(""), nil
			}}
			seedConcurrentAstraRoute(t, s, "old")
			pool := s.current(t.Context())
			leader := make(chan error, 1)
			go func() {
				_, _, _, done, err := s.targetRouteOnce(forceAstraProbe(pinRequest(t)), "", 300, 1, nil)
				done()
				leader <- err
			}()
			<-entered
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			follower := make(chan error, 1)
			go func() {
				_, _, _, done, err := s.targetRouteOnce(forceAstraProbe(pinRequest(t).WithContext(ctx)), "", 300, 1, nil)
				done()
				follower <- err
			}()
			select {
			case err := <-follower:
				close(release)
				t.Fatalf("premature admission: %v", err)
			case <-time.After(40 * time.Millisecond):
			}
			switch change {
			case "cancel":
				cancel()
				require.ErrorIs(t, receiveAdmission(t, follower), context.Canceled)
			case "route":
				seedConcurrentAstraRoute(t, s, "new")
			case "config":
				s.mu.Lock()
				s.pool = nil
				s.mu.Unlock()
			case "expiry":
				pool.mu.Lock()
				route := pool.routes[299]
				route.expires = time.Now().Add(-time.Second)
				pool.routes[299] = route
				pool.mu.Unlock()
			}
			close(release)
			if change == "cancel" {
				require.NoError(t, receiveAdmission(t, leader))
			} else {
				require.EqualError(t, receiveAdmission(t, leader), "configuration_changed")
				require.EqualError(t, receiveAdmission(t, follower), "configuration_changed")
			}
			require.EqualValues(t, 2, probes.Load(), "waiter must not probe a stale route")
		})
	}
}

func TestAstraPreparationWaitRejectsReplacedPool(t *testing.T) {
	s := concurrentAstraWrapper()
	entered, release := make(chan struct{}), make(chan struct{})
	s.SetAstraGatewayPreparer(func(context.Context, int64) error { close(entered); <-release; return errors.New("offline") })
	leader, follower := make(chan error, 1), make(chan error, 1)
	go func() { leader <- s.PrepareAstraGateway(t.Context()) }()
	<-entered
	go func() { follower <- s.PrepareAstraGateway(t.Context()) }()
	select {
	case err := <-follower:
		close(release)
		t.Fatalf("premature admission: %v", err)
	case <-time.After(40 * time.Millisecond):
	}
	s.mu.Lock()
	s.pool = nil
	s.mu.Unlock()
	close(release)
	require.EqualError(t, receiveAdmission(t, leader), "configuration_changed")
	require.EqualError(t, receiveAdmission(t, follower), "configuration_changed")
}

func TestAstraTargetWaitDeadlineReleasesNodeLease(t *testing.T) {
	s, _, leases := rotationFixture(t, 1, "good")
	// Use a valid rotated source without sending a business request.
	s.listNodes = func() ([]mihomo.AstraNode, error) { return []mihomo.AstraNode{{ID: "good", Identity: "good"}}, nil }
	require.NoError(t, s.PrepareAstraGateway(t.Context()))
	pool := s.current(t.Context())
	require.NoError(t, pool.targetProbeMu.Lock(t.Context()))
	defer pool.targetProbeMu.Unlock()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	_, _, _, release, err := s.targetRouteOnce(forceAstraProbe(rotationTarget(t).WithContext(ctx)), "", 300, 1, nil)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Zero(t, *leases, "expired waiter releases the pinned exit")
	release()
	require.Zero(t, *leases, "release remains safe after an error")
}

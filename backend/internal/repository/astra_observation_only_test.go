package repository

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/mihomo"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

func TestAstraUnavailableRouteForwardsOriginalRequestOnce(t *testing.T) {
	for _, state := range []string{"empty", "cooldown", "expired", "exit_unavailable"} {
		t.Run(state, func(t *testing.T) {
			cfg := &config.Config{}
			cfg.Gateway.CodexGatewayPin = pinConfig()
			cfg.Gateway.CodexGatewayPin.RotateNodes = true
			cfg.Gateway.CodexGatewayPin.IPAffinity = true
			calls := 0
			upstreamFailure := errors.New("actual upstream failure")
			s := &astraRoutingUpstream{cfg: cfg}
			s.SetAstraGatewayPreparer(func(context.Context, int64) error { t.Fatal("business started a probe"); return nil })
			s.pinNode = func(context.Context, mihomo.AstraNode) (string, func(), error) {
				return "", func() {}, errors.New("exit unavailable")
			}
			pool := s.current(t.Context())
			if state == "cooldown" {
				pool.rotationRetryAfter = time.Now().Add(time.Minute)
			}
			if state == "expired" || state == "exit_unavailable" {
				expires := time.Now().Add(time.Minute)
				if state == "expired" {
					expires = time.Now().Add(-time.Second)
				}
				pool.recordSource(299, time.Now(), &codexGatewayRoute{cookie: http.Cookie{Name: "__oailb", Value: "borrowed", Path: "/"}, proxy: "borrowed-exit", expires: expires, node: mihomo.AstraNode{ID: "node", Identity: "node"}}, true)
			}
			for _, fail := range []bool{false, true} {
				r := pinRequest(t)
				r.Header.Set("Authorization", "Bearer original")
				r.Header.Set("Cookie", "__oailb=original")
				r.Header.Set("X-Codex-Turn-State", "original-state")
				r.Body = io.NopCloser(strings.NewReader("original body"))
				headers := r.Header.Clone()
				s.delegate = gatewayPinDelegate{call: func(got *http.Request, proxy string, id int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
					calls++
					require.Equal(t, "original-exit", proxy)
					require.Equal(t, int64(300), id)
					require.Equal(t, headers, got.Header)
					body, err := io.ReadAll(got.Body)
					require.NoError(t, err)
					require.Equal(t, "original body", string(body))
					if fail {
						return nil, upstreamFailure
					}
					return pinResponse(""), nil
				}}
				resp, err := s.Do(r, "original-exit", 300, 1)
				if fail {
					require.ErrorIs(t, err, upstreamFailure)
				} else {
					consumeAffinityResponse(t, resp, err)
				}
			}
			require.Equal(t, 2, calls, "one send per business request; never replay upstream failures")
			headers := http.Header{"Authorization": []string{"Bearer original"}}
			before := headers.Clone()
			cookie, proxy, release, err := s.CodexGatewayPinWSRequest(t.Context(), headers, "original-exit", 300, 1)
			require.NoError(t, err)
			release()
			require.Empty(t, cookie)
			require.Equal(t, "original-exit", proxy)
			require.Equal(t, before, headers)
			cookie, err = s.CodexGatewayPinWSHeader(300)
			require.NoError(t, err)
			if state != "exit_unavailable" {
				require.Empty(t, cookie)
			}
		})
	}
}

func TestUnifiedAstraFailedOptionalBundlePreservesOriginalHeaders(t *testing.T) {
	for _, code := range []string{"source_probe_cooldown", "target_probe_failed", "target_validation_in_progress", "configuration_changed"} {
		t.Run(code, func(t *testing.T) {
			cfg := &config.Config{}
			cfg.Gateway.CodexGatewayPin = pinConfig()
			p := &unifiedAstraProvider{enabled: true, err: errors.New(code), mutateOnFailure: true}
			calls := 0
			s := &astraRoutingUpstream{cfg: cfg, tickets: p}
			r := pinRequest(t)
			r.Header.Set("Cookie", "original")
			before := r.Header.Clone()
			s.delegate = gatewayPinDelegate{call: func(got *http.Request, proxy string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
				calls++
				require.Equal(t, "original", proxy)
				require.Equal(t, before, got.Header)
				return pinResponse(""), nil
			}}
			resp, err := s.Do(r, "original", 300, 1)
			consumeAffinityResponse(t, resp, err)
			require.Equal(t, 1, calls)
			cookie, proxy, release, err := s.CodexGatewayPinWSRequest(t.Context(), r.Header, "original", 300, 1)
			require.NoError(t, err)
			release()
			require.Empty(t, cookie)
			require.Equal(t, "original", proxy)
			require.Equal(t, before, r.Header)
			require.Zero(t, p.prepared)
			require.Zero(t, p.verified)
		})
	}
}

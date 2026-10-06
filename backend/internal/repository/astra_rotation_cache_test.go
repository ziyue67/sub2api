package repository

import (
	"context"
	"encoding/base64"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/mihomo"
	"github.com/stretchr/testify/require"
)

func TestAstraRotationCacheHostIsolationAndDeadline(t *testing.T) {
	c := &astraRotationCache{}
	now := time.Now()
	until := now.Add(time.Hour)
	c.observe("node-a", "host-1", now)
	c.observe("node-b", "host-1", now)
	c.reject(300, "node-a", "host-1", until, now)
	require.Equal(t, until, c.blockedUntil(300, "node-b", "", now), "another exit with same known host must not refresh that target")
	require.True(t, c.blockedUntil(301, "node-b", "", now).IsZero(), "another target is independent")
	c.reject(300, "node-a", "host-1", until.Add(time.Hour), now.Add(time.Minute))
	require.Equal(t, until, c.blockedUntil(300, "node-a", "", now.Add(time.Minute)), "duplicate failure does not slide quiet window")
	require.True(t, c.blockedUntil(300, "node-b", "", until.Add(time.Second)).IsZero())
}
func TestAstraRotationCacheSurvivesSaveAndSuppressesNetwork(t *testing.T) {
	s, calls, leases := rotationFixture(t, 3, "never")
	_, err := s.Do(rotationTarget(t), "target", 300, 1)
	require.ErrorContains(t, err, "astra_rotation_exhausted")
	require.Len(t, *calls, 9)
	require.Zero(t, *leases)
	// Any new revision drops Cookies but must keep retry decisions.
	s.cfg.Gateway.CodexGatewayPin.TTLSeconds = 200
	_, err = s.Do(rotationTarget(t), "target", 300, 1)
	require.ErrorContains(t, err, "astra_rotation_cooling")
	require.Len(t, *calls, 9)
	require.Zero(t, *leases)
	// Expiry allows retry without deleting durable history or resetting account state.
	s.rotationCache.mu.Lock()
	for key := range s.rotationCache.blocked {
		s.rotationCache.blocked[key] = time.Now().Add(-time.Second)
	}
	s.rotationCache.mu.Unlock()
	_, err = s.Do(rotationTarget(t), "target", 300, 1)
	require.ErrorContains(t, err, "astra_rotation_exhausted")
	require.Len(t, *calls, 18)
}
func TestAstraRotationCacheFiltersWithoutSpendingAttemptBudget(t *testing.T) {
	s, calls, leases := rotationFixture(t, 1, "good")
	now := time.Now()
	s.rotationCache.reject(300, "bad", "", now.Add(time.Hour), now)
	resp, err := s.Do(rotationTarget(t), "target", 300, 1)
	consumeAffinityResponse(t, resp, err)
	require.Equal(t, []string{"good:299", "good:300", "good:300", "good:300"}, *calls)
	require.Zero(t, *leases)
}
func TestAstraRotationCacheRejectsMappedHostBeforeTargetProbe(t *testing.T) {
	s, calls, leases := rotationFixture(t, 3, "good")
	now := time.Now()
	s.rotationCache.observe("bad", "blocked-host", now)
	s.rotationCache.reject(300, "other-node", "blocked-host", now.Add(time.Hour), now)
	s.listNodes = func() ([]mihomo.AstraNode, error) { return []mihomo.AstraNode{{ID: "bad", Identity: "bad"}}, nil }
	require.ErrorContains(t, s.PrepareAstraGateway(context.Background()), "astra_rotation_cooling")
	require.Empty(t, *calls)
	require.Zero(t, *leases)
	rows := s.rotationCache.snapshot([]int64{300}, now)
	require.Len(t, rows, 1)
	require.Equal(t, "blocked-host", rows[0].Gateway)
}

func TestAstraSourceFailuresSkipUntilExpiryAcrossSave(t *testing.T) {
	s, _, leases := rotationFixture(t, 1, "never")
	s.listNodes = func() ([]mihomo.AstraNode, error) { return []mihomo.AstraNode{{ID: "bad", Identity: "bad"}}, nil }
	calls := map[int64]int{}
	s.SetAstraGatewayPreparer(func(_ context.Context, id int64) error { calls[id]++; return nil }) // No qualified Cookie is also a failure.
	require.ErrorContains(t, s.PrepareAstraGateway(t.Context()), "exhausted")
	require.Equal(t, map[int64]int{299: 1, 298: 1}, calls)
	require.Zero(t, *leases)
	s.cfg.Gateway.CodexGatewayPin.TTLSeconds = 201
	require.ErrorContains(t, s.PrepareAstraGateway(t.Context()), "cooling")
	require.Equal(t, map[int64]int{299: 1, 298: 1}, calls)
	require.Zero(t, *leases)
	s.rotationCache.mu.Lock()
	for key := range s.rotationCache.blocked {
		if key.kind == "source" {
			s.rotationCache.blocked[key] = time.Now().Add(-time.Second)
		}
	}
	s.rotationCache.mu.Unlock()
	require.ErrorContains(t, s.PrepareAstraGateway(t.Context()), "exhausted")
	require.Equal(t, map[int64]int{299: 2, 298: 2}, calls)
}
func TestAstraSourceCooldownDoesNotBlockOtherDonorsOrTargets(t *testing.T) {
	c := &astraRotationCache{}
	now := time.Now()
	c.rejectSource(299, "node", now)
	require.True(t, c.sourceCooling(299, "node", now))
	require.False(t, c.sourceCooling(298, "node", now))
	require.False(t, c.sourceCooling(299, "different-node", now))
	require.True(t, c.blockedUntil(299, "node", "", now).IsZero())
	c.rejectSource(299, "node", now.Add(30*time.Second))
	require.False(t, c.sourceCooling(299, "node", now.Add(time.Minute)))
}
func TestAstraSourceCancelledPreparationDoesNotPoisonCache(t *testing.T) {
	s, _, leases := rotationFixture(t, 1, "never")
	ctx, cancel := context.WithCancel(t.Context())
	s.SetAstraGatewayPreparer(func(context.Context, int64) error { cancel(); return context.Canceled })
	require.ErrorIs(t, s.PrepareAstraGateway(ctx), context.Canceled)
	require.False(t, s.rotationCache.sourceCooling(299, "bad", time.Now()))
	require.Zero(t, *leases)
}

func TestRepeatedCoolingHostPausesSourceNetworkAcrossSaves(t *testing.T) {
	s, _, leases := rotationFixture(t, 3, "never")
	s.cfg.Gateway.CodexGatewayPin.SourceAccountIDs = []int64{299}
	s.current(t.Context())
	host := "chat.gateway.unified-95.api.openai.com"
	s.rotationCache.reject(300, "old-node", host, time.Now().Add(time.Hour), time.Now())
	calls := 0
	s.SetAstraGatewayPreparer(func(ctx context.Context, id int64) error {
		calls++
		egress, ok := ctx.Value(astraSourceEgressKey{}).(astraSourceEgress)
		require.True(t, ok)
		value := "header." + base64.RawURLEncoding.EncodeToString([]byte(`{"host":"`+host+`"}`)) + ".signature"
		route := codexGatewayRouteFromResponse(pinResponse("__oailb="+value+"; Path=/; Secure; Max-Age=230"), "/backend-api/codex/responses", time.Now())
		route.node = egress.node
		s.current(ctx).recordSource(id, time.Now(), route, true)
		return nil
	})
	ctx := context.WithValue(t.Context(), astraRotationTargetKey{}, int64(300))
	require.ErrorContains(t, s.PrepareAstraGateway(ctx), "astra_rotation_cooling")
	require.Equal(t, 1, calls, "stop scanning other exits after duplicate cooling host")
	require.Zero(t, *leases)
	s.cfg.Gateway.CodexGatewayPin.TTLSeconds = 200
	require.ErrorContains(t, s.PrepareAstraGateway(ctx), "astra_rotation_cooling")
	require.Equal(t, 1, calls, "a settings revision must not restart source traffic")
	require.False(t, s.rotationCache.sourceSearchPaused(343, time.Now()))
	require.False(t, s.rotationCache.sourceSearchPaused(300, time.Now().Add(5*time.Minute)))
}

func TestSourceSearchPauseDoesNotSlide(t *testing.T) {
	c := &astraRotationCache{}
	now := time.Now()
	c.pauseSourceSearch(300, now)
	c.pauseSourceSearch(300, now.Add(4*time.Minute))
	require.True(t, c.sourceSearchPaused(300, now.Add(4*time.Minute)))
	require.False(t, c.sourceSearchPaused(300, now.Add(5*time.Minute)))
	require.False(t, c.sourceSearchPaused(343, now))
}

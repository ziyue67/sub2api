package service

import (
	"context"
	"errors"
	"github.com/gin-gonic/gin"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestAstraRouteFailureDisablesTargetAndAllowsFailover(t *testing.T) {
	settings := config.AstraRoutingSettings{AccountScheduling: true, Revision: "one", CookiePool: config.CodexGatewayPinConfig{Enabled: true, TargetAccountIDs: []int64{300}}}
	cfg := &config.Config{}
	cfg.SetAstraRoutingLoader(func(context.Context) config.AstraRoutingSettings { return settings })
	account := &Account{ID: 300, Schedulable: true}
	repo := &astraSchedulingRepo{accounts: map[int64]*Account{300: account}}
	provider := &astraSetupUpstream{snapshot: AstraGatewayRuntime{Revision: "one"}}
	svc := &OpenAIGatewayService{cfg: cfg, accountRepo: repo, httpUpstream: provider}
	expiry := time.Now().Add(time.Minute)
	provider.snapshot.Targets = []AstraRouteStatus{{AccountID: 300, State: "ready", Reason: "target_probe_passed", ExpiresAt: &expiry}}
	require.NoError(t, svc.checkAstraSchedulingRoute(t.Context(), account))
	require.Empty(t, repo.writes)
	expiry = time.Now().Add(-time.Second)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	_, err := svc.Forward(t.Context(), c, account, []byte(`{"model":"gpt-6-astra","stream":true}`))
	var failover *UpstreamFailoverError
	require.ErrorAs(t, err, &failover)
	require.Equal(t, http.StatusServiceUnavailable, failover.StatusCode)
	require.False(t, account.Schedulable)
	require.False(t, c.Writer.Written())
	require.Empty(t, rec.Body.String())
	account.Schedulable = true
	err = svc.handleOpenAIUpstreamTransportError(t.Context(), c, account, errors.New("astra_rotation_no_nodes"), false)
	require.ErrorAs(t, err, &failover)
	require.False(t, account.Schedulable)
	require.False(t, c.Writer.Written())
	account.Schedulable = true
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	original := errors.New("astra_rotation_no_nodes")
	require.Same(t, original, svc.astraRouteFailover(ctx, account, original))
	require.True(t, account.Schedulable, "client cancellation must not disable account")
	require.Same(t, original, svc.astraRouteFailover(t.Context(), &Account{ID: 299}, original))
	settings.CookiePool.Enabled = false
	err = svc.checkAstraSchedulingRoute(t.Context(), account)
	require.ErrorAs(t, err, &failover)
	require.False(t, account.Schedulable)
}

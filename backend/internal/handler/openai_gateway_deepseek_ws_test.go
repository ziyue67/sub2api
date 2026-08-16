package handler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type deepSeekWSHandlerFailoverUpstream struct {
	service.HTTPUpstream
	mu           sync.Mutex
	calls        []int64
	bodies       [][]byte
	successCount int
	cyberAt      int
}

func (u *deepSeekWSHandlerFailoverUpstream) Do(req *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
	body, _ := io.ReadAll(req.Body)
	u.mu.Lock()
	u.calls = append(u.calls, accountID)
	u.bodies = append(u.bodies, append([]byte(nil), body...))
	if accountID == 8801 {
		u.mu.Unlock()
		return &http.Response{
			StatusCode: http.StatusServiceUnavailable,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"temporary unavailable"}}`)),
		}, nil
	}
	u.successCount++
	successCount := u.successCount
	cyberAt := u.cyberAt
	u.mu.Unlock()

	responseID := fmt.Sprintf("resp_ds_ws_failover_%d", successCount)
	if cyberAt > 0 && successCount == cyberAt {
		responseID = fmt.Sprintf("resp_ds_ws_cyber_%d", successCount)
		response := fmt.Sprintf(
			"event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"id\":%q,\"model\":\"deepseek-v4-pro\",\"status\":\"failed\",\"output\":[],\"error\":{\"code\":\"cyber_policy\",\"message\":\"blocked\"},\"usage\":{\"input_tokens\":9,\"output_tokens\":2,\"total_tokens\":11}}}\n\n",
			responseID,
		)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(response)),
		}, nil
	}
	response := fmt.Sprintf(
		"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":%q,\"model\":\"deepseek-v4-pro\",\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"ok\"}]}],\"usage\":{\"input_tokens\":5,\"output_tokens\":2,\"total_tokens\":7}}}\n\n",
		responseID,
	)
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(response)),
	}, nil
}

func (u *deepSeekWSHandlerFailoverUpstream) snapshot() ([]int64, [][]byte) {
	u.mu.Lock()
	defer u.mu.Unlock()
	calls := append([]int64(nil), u.calls...)
	bodies := make([][]byte, len(u.bodies))
	for i := range u.bodies {
		bodies[i] = append([]byte(nil), u.bodies[i]...)
	}
	return calls, bodies
}

func TestDeepSeekResponsesWebSocketHandlerBridgesNativeHTTPAndRecordsUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstreamBody := deepSeekCompactCompletedResponsesSSE(t, "resp_ds_ws_handler", "done", 7, 3)
	h, usageRepo, apiKey, upstream := newDeepSeekPartialUsageHandler(t, upstreamBody)
	h.cfg.Gateway.OpenAIWS.Enabled = true
	h.cfg.Gateway.OpenAIWS.ModeRouterV2Enabled = true
	h.cfg.Gateway.OpenAIWS.HTTPBridgeEnabled = true
	h.cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 3
	h.cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 3
	h.cfg.DeepSeek.UserIDSecret = "deepseek-ws-handler-user-secret"

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), ctxkey.UserID, apiKey.User.ID))
		c.Set(string(middleware.ContextKeyAPIKey), apiKey)
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: apiKey.User.ID})
		c.Next()
	})
	router.GET("/backend-api/codex/responses", h.ResponsesWebSocket)
	server := httptest.NewServer(router)
	defer server.Close()

	dialCtx, cancelDial := context.WithTimeout(context.Background(), 3*time.Second)
	conn, _, err := coderws.Dial(
		dialCtx,
		"ws"+strings.TrimPrefix(server.URL, "http")+"/backend-api/codex/responses",
		&coderws.DialOptions{CompressionMode: coderws.CompressionContextTakeover},
	)
	cancelDial()
	require.NoError(t, err)
	defer func() { _ = conn.CloseNow() }()

	request := []byte(`{"type":"response.create","generate":true,"model":"deepseek-v4-pro","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}]}`)
	writeCtx, cancelWrite := context.WithTimeout(context.Background(), 3*time.Second)
	err = conn.Write(writeCtx, coderws.MessageText, request)
	cancelWrite()
	require.NoError(t, err)

	var completed []byte
	for completed == nil {
		readCtx, cancelRead := context.WithTimeout(context.Background(), 3*time.Second)
		_, event, readErr := conn.Read(readCtx)
		cancelRead()
		require.NoError(t, readErr)
		if gjson.GetBytes(event, "type").String() == "response.completed" {
			completed = event
		}
	}
	require.Equal(t, "resp_ds_ws_handler", gjson.GetBytes(completed, "response.id").String())

	require.Equal(t, "/responses", upstream.lastPath)
	require.True(t, gjson.GetBytes(upstream.lastBody, "stream").Bool())
	require.False(t, gjson.GetBytes(upstream.lastBody, "type").Exists())
	require.False(t, gjson.GetBytes(upstream.lastBody, "generate").Exists())
	require.False(t, gjson.GetBytes(upstream.lastBody, "previous_response_id").Exists())
	require.True(t, strings.HasPrefix(gjson.GetBytes(upstream.lastBody, "user").String(), "dsu_v1_"))
	require.NotContains(t, string(upstream.lastBody), "sk-deepseek-partial")

	select {
	case usageLog := <-usageRepo.created:
		require.Equal(t, 7, usageLog.InputTokens)
		require.Equal(t, 3, usageLog.OutputTokens)
		require.True(t, usageLog.Stream)
		require.True(t, usageLog.OpenAIWSMode)
	case <-time.After(3 * time.Second):
		t.Fatal("waiting for DeepSeek WebSocket usage record timed out")
	}

	_ = conn.Close(coderws.StatusNormalClosure, http.StatusText(http.StatusOK))
}

func TestDeepSeekResponsesWebSocketCyberPolicyBlocksNextTurnAndRecordsOnce(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstreamBody := "event: response.failed\n" +
		`data: {"type":"response.failed","response":{"id":"resp_ds_ws_cyber","model":"deepseek-v4-pro","status":"failed","output":[],"error":{"code":"cyber_policy","message":"blocked"},"usage":{"input_tokens":9,"output_tokens":2,"total_tokens":11}}}` + "\n\n"
	h, usageRepo, apiKey, upstream := newDeepSeekPartialUsageHandler(t, upstreamBody)
	h.cfg.Gateway.OpenAIWS.Enabled = true
	h.cfg.Gateway.OpenAIWS.ModeRouterV2Enabled = true
	h.cfg.Gateway.OpenAIWS.HTTPBridgeEnabled = true
	h.cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 3
	h.cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 3
	h.cfg.DeepSeek.UserIDSecret = "deepseek-ws-cyber-user-secret"

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), ctxkey.UserID, apiKey.User.ID))
		c.Set(string(middleware.ContextKeyAPIKey), apiKey)
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: apiKey.User.ID})
		c.Next()
	})
	router.GET("/responses", h.ResponsesWebSocket)
	server := httptest.NewServer(router)
	defer server.Close()

	dialCtx, cancelDial := context.WithTimeout(context.Background(), 3*time.Second)
	conn, _, err := coderws.Dial(dialCtx, "ws"+strings.TrimPrefix(server.URL, "http")+"/responses", nil)
	cancelDial()
	require.NoError(t, err)
	defer func() { _ = conn.CloseNow() }()

	writeCtx, cancelWrite := context.WithTimeout(context.Background(), 3*time.Second)
	require.NoError(t, conn.Write(writeCtx, coderws.MessageText, []byte(`{"type":"response.create","model":"deepseek-v4-pro","input":"first"}`)))
	cancelWrite()
	readCtx, cancelRead := context.WithTimeout(context.Background(), 3*time.Second)
	_, event, err := conn.Read(readCtx)
	cancelRead()
	require.NoError(t, err)
	require.Equal(t, "response.failed", gjson.GetBytes(event, "type").String())

	select {
	case usageLog := <-usageRepo.created:
		require.Equal(t, 9, usageLog.InputTokens)
		require.Equal(t, 2, usageLog.OutputTokens)
		require.Equal(t, service.RequestTypeCyberBlocked, usageLog.RequestType)
	case <-time.After(3 * time.Second):
		t.Fatal("waiting for DeepSeek WebSocket cyber usage record timed out")
	}

	writeCtx, cancelWrite = context.WithTimeout(context.Background(), 3*time.Second)
	require.NoError(t, conn.Write(writeCtx, coderws.MessageText, []byte(`{"type":"response.create","model":"deepseek-v4-pro","input":"second"}`)))
	cancelWrite()
	readCtx, cancelRead = context.WithTimeout(context.Background(), 3*time.Second)
	_, _, err = conn.Read(readCtx)
	cancelRead()
	require.Error(t, err, "the connection must close before a cyber-blocked second turn reaches upstream")
	require.Equal(t, 1, upstream.calls)
	select {
	case extra := <-usageRepo.created:
		t.Fatalf("unexpected duplicate cyber usage row: %+v", extra)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestDeepSeekResponsesWebSocketHTTPCyberPolicyDoesNotFailoverAndRecordsOnce(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, usageRepo, apiKey, upstream := newDeepSeekPartialUsageHandler(t, `{"error":{"code":"cyber_policy","message":"blocked"}}`)
	upstream.statusCode = http.StatusBadRequest
	h.cfg.Gateway.OpenAIWS.Enabled = true
	h.cfg.Gateway.OpenAIWS.ModeRouterV2Enabled = true
	h.cfg.Gateway.OpenAIWS.HTTPBridgeEnabled = true
	h.cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 3
	h.cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 3
	h.cfg.DeepSeek.UserIDSecret = "deepseek-ws-http-cyber-user-secret"

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), ctxkey.UserID, apiKey.User.ID))
		c.Set(string(middleware.ContextKeyAPIKey), apiKey)
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: apiKey.User.ID})
		c.Next()
	})
	router.GET("/responses", h.ResponsesWebSocket)
	server := httptest.NewServer(router)
	defer server.Close()

	dialCtx, cancelDial := context.WithTimeout(context.Background(), 3*time.Second)
	conn, _, err := coderws.Dial(dialCtx, "ws"+strings.TrimPrefix(server.URL, "http")+"/responses", nil)
	cancelDial()
	require.NoError(t, err)
	defer func() { _ = conn.CloseNow() }()

	writeCtx, cancelWrite := context.WithTimeout(context.Background(), 3*time.Second)
	require.NoError(t, conn.Write(writeCtx, coderws.MessageText, []byte(`{"type":"response.create","model":"deepseek-v4-pro","input":"blocked","reasoning":{"effort":"max"}}`)))
	cancelWrite()
	readCtx, cancelRead := context.WithTimeout(context.Background(), 3*time.Second)
	_, event, err := conn.Read(readCtx)
	cancelRead()
	require.NoError(t, err)
	require.Equal(t, "error", gjson.GetBytes(event, "type").String())

	select {
	case usageLog := <-usageRepo.created:
		require.Zero(t, usageLog.InputTokens)
		require.Zero(t, usageLog.OutputTokens)
		require.Equal(t, service.RequestTypeCyberBlocked, usageLog.RequestType)
		require.Equal(t, "deepseek-v4-pro", usageLog.Model)
		require.NotNil(t, usageLog.UpstreamModel)
		require.Equal(t, "deepseek-v4-pro", *usageLog.UpstreamModel)
		require.NotNil(t, usageLog.ReasoningEffort)
		require.Equal(t, "max", *usageLog.ReasoningEffort)
		require.True(t, usageLog.Stream)
		require.True(t, usageLog.OpenAIWSMode)
	case <-time.After(3 * time.Second):
		t.Fatal("waiting for DeepSeek WebSocket HTTP cyber usage record timed out")
	}
	require.Equal(t, 1, upstream.calls, "cyber policy must not fail over to another account")
	select {
	case extra := <-usageRepo.created:
		t.Fatalf("unexpected duplicate cyber usage row: %+v", extra)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestDeepSeekResponsesWSScheduleOutcome(t *testing.T) {
	completed := &service.OpenAIForwardResult{UpstreamTerminalEvent: "response.completed"}

	report, succeeded := deepSeekResponsesWSScheduleOutcome(completed, nil, nil)
	require.True(t, report)
	require.True(t, succeeded)

	report, succeeded = deepSeekResponsesWSScheduleOutcome(completed, errors.New("missing billable usage"), nil)
	require.True(t, report)
	require.False(t, succeeded, "protocol and missing-usage failures must not mark the account healthy")

	report, succeeded = deepSeekResponsesWSScheduleOutcome(completed, context.Canceled, context.Canceled)
	require.False(t, report, "client cancellation must not punish account health")
	require.False(t, succeeded)

	clientErr := service.NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, "invalid request", nil)
	report, succeeded = deepSeekResponsesWSScheduleOutcome(nil, clientErr, nil)
	require.False(t, report, "client protocol errors must not punish account health")
	require.False(t, succeeded)
}

func TestDeepSeekResponsesWebSocketHandlerCompositeFailoverAndPerTurnSlots(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const groupID int64 = 8800
	accounts := []service.Account{
		{
			ID: 8801, Name: "deepseek-ws-first", Platform: service.PlatformDeepSeek,
			Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Concurrency: 1, Priority: 1,
			GroupIDs:    []int64{groupID},
			Credentials: map[string]any{"api_key": "sk-first", "base_url": "https://deepseek.first.test"},
			Extra: map[string]any{
				service.DeepSeekUserIsolationModeKey:      service.DeepSeekUserIsolationModeAuthenticatedUser,
				service.DeepSeekResponsesWebSocketModeKey: service.DeepSeekResponsesWebSocketModeHTTPBridge,
			},
		},
		{
			ID: 8802, Name: "deepseek-ws-second", Platform: service.PlatformDeepSeek,
			Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Concurrency: 1, Priority: 2,
			GroupIDs:    []int64{groupID},
			Credentials: map[string]any{"api_key": "sk-second", "base_url": "https://deepseek.second.test"},
			Extra: map[string]any{
				service.DeepSeekUserIsolationModeKey:      service.DeepSeekUserIsolationModeAuthenticatedUser,
				service.DeepSeekResponsesWebSocketModeKey: service.DeepSeekResponsesWebSocketModeHTTPBridge,
			},
		},
	}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Default.RateMultiplier = 1
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Gateway.MaxAccountSwitches = 2
	cfg.Gateway.OpenAIWS.Enabled = true
	cfg.Gateway.OpenAIWS.ModeRouterV2Enabled = true
	cfg.Gateway.OpenAIWS.HTTPBridgeEnabled = true
	cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 3
	cfg.DeepSeek.UserIDSecret = "deepseek-ws-failover-user-secret"

	accountRepo := &openAIWSFailoverHandlerAccountRepoStub{accounts: accounts}
	usageRepo := &openAIWSUsageHandlerUsageLogRepoStub{created: make(chan *service.UsageLog, 3)}
	upstream := &deepSeekWSHandlerFailoverUpstream{cyberAt: 3}
	var userAcquires atomic.Int32
	var accountAcquires atomic.Int32
	cache := &concurrencyCacheMock{
		acquireUserSlotFn: func(context.Context, int64, int, string) (bool, error) {
			userAcquires.Add(1)
			return true, nil
		},
		acquireAccountSlotFn: func(context.Context, int64, int, string) (bool, error) {
			accountAcquires.Add(1)
			return true, nil
		},
	}
	concurrencySvc := service.NewConcurrencyService(cache)
	billingCacheSvc := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingCacheSvc.Stop)
	rateLimitSvc := service.NewRateLimitService(accountRepo, nil, cfg, nil, nil)
	gatewaySvc := service.NewOpenAIGatewayService(
		accountRepo, usageRepo, nil, nil, nil, nil, nil, cfg, nil, concurrencySvc,
		service.NewBillingService(cfg, nil), rateLimitSvc, billingCacheSvc, upstream,
		&service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil,
	)
	h := NewOpenAIGatewayHandler(
		gatewaySvc,
		concurrencySvc,
		billingCacheSvc,
		service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg),
		nil, nil, nil, nil, cfg,
	)

	apiKey := &service.APIKey{
		ID: 8803, GroupID: func() *int64 { value := groupID; return &value }(),
		User: &service.User{ID: 8804, Status: service.StatusActive},
		Group: &service.Group{
			ID: groupID, Platform: service.PlatformComposite, Status: service.StatusActive, RateMultiplier: 1,
		},
	}
	resolver := service.NewCompositeRouteResolver(&responsesWSCompositeRouteRepoStub{routes: []service.CompositeModelRoute{{
		ID: 8805, GroupID: groupID, PublicModel: "company-coding-model", MatchType: service.CompositeRouteMatchExact,
		TargetPlatform: service.PlatformDeepSeek, UpstreamModel: "deepseek-v4-pro", Endpoint: service.CompositeRouteEndpointResponses,
		Enabled: true,
	}}})
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), ctxkey.UserID, apiKey.User.ID))
		c.Set(string(middleware.ContextKeyAPIKey), apiKey)
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: apiKey.User.ID, Concurrency: 1})
		AttachResponsesWebSocketCompositeResolver(c, resolver)
		c.Next()
	})
	router.GET("/responses", h.ResponsesWebSocket)
	server := httptest.NewServer(router)
	defer server.Close()

	dialCtx, cancelDial := context.WithTimeout(context.Background(), 3*time.Second)
	conn, _, err := coderws.Dial(dialCtx, "ws"+strings.TrimPrefix(server.URL, "http")+"/responses", nil)
	cancelDial()
	require.NoError(t, err)
	defer func() { _ = conn.CloseNow() }()

	writeTurn := func(payload string) {
		writeCtx, cancelWrite := context.WithTimeout(context.Background(), 3*time.Second)
		writeErr := conn.Write(writeCtx, coderws.MessageText, []byte(payload))
		cancelWrite()
		require.NoError(t, writeErr)
	}
	readCompleted := func(wantID string) {
		for {
			readCtx, cancelRead := context.WithTimeout(context.Background(), 3*time.Second)
			_, event, readErr := conn.Read(readCtx)
			cancelRead()
			require.NoError(t, readErr)
			if gjson.GetBytes(event, "type").String() == "response.completed" {
				require.Equal(t, wantID, gjson.GetBytes(event, "response.id").String())
				return
			}
		}
	}

	writeTurn(`{"type":"response.create","model":"company-coding-model","input":"first"}`)
	readCompleted("resp_ds_ws_failover_1")
	require.Eventually(t, func() bool {
		return atomic.LoadInt32(&cache.releaseUserCalled) == 1 && atomic.LoadInt32(&cache.releaseAccountCalled) == 2
	}, 3*time.Second, 10*time.Millisecond, "the idle socket must release first-turn user/account slots")

	writeTurn(`{"type":"response.create","input":"second"}`)
	readCompleted("resp_ds_ws_failover_2")
	require.Eventually(t, func() bool {
		return atomic.LoadInt32(&cache.releaseUserCalled) == 2 && atomic.LoadInt32(&cache.releaseAccountCalled) == 4
	}, 3*time.Second, 10*time.Millisecond, "the next turn must independently release its slots")
	require.Equal(t, int32(2), userAcquires.Load())
	require.Equal(t, int32(4), accountAcquires.Load())

	writeTurn(`{"type":"response.create","input":"third","reasoning":{"effort":"max"}}`)
	for {
		readCtx, cancelRead := context.WithTimeout(context.Background(), 3*time.Second)
		_, event, readErr := conn.Read(readCtx)
		cancelRead()
		require.NoError(t, readErr)
		if gjson.GetBytes(event, "type").String() == "response.failed" {
			require.Equal(t, "resp_ds_ws_cyber_3", gjson.GetBytes(event, "response.id").String())
			break
		}
	}
	require.Eventually(t, func() bool {
		return atomic.LoadInt32(&cache.releaseUserCalled) == 3 && atomic.LoadInt32(&cache.releaseAccountCalled) == 6
	}, 3*time.Second, 10*time.Millisecond, "the cyber turn must release its user/account slots")
	require.Equal(t, int32(3), userAcquires.Load())
	require.Equal(t, int32(6), accountAcquires.Load())

	logs := make([]*service.UsageLog, 0, 3)
	for len(logs) < 3 {
		select {
		case log := <-usageRepo.created:
			logs = append(logs, log)
		case <-time.After(3 * time.Second):
			t.Fatal("waiting for DeepSeek WebSocket usage records timed out")
		}
	}
	require.Len(t, logs, 3, "each successful or billable cyber turn must record usage exactly once")
	var cyberLog *service.UsageLog
	for _, log := range logs {
		if log.RequestType == service.RequestTypeCyberBlocked {
			cyberLog = log
			break
		}
	}
	require.NotNil(t, cyberLog)
	require.Equal(t, "deepseek-v4-pro", cyberLog.Model)
	require.Equal(t, "company-coding-model", cyberLog.RequestedModel)
	require.NotNil(t, cyberLog.UpstreamModel)
	require.Equal(t, "deepseek-v4-pro", *cyberLog.UpstreamModel)
	require.NotNil(t, cyberLog.ReasoningEffort)
	require.Equal(t, "max", *cyberLog.ReasoningEffort)
	require.True(t, cyberLog.Stream)
	require.True(t, cyberLog.OpenAIWSMode)
	require.Equal(t, 9, cyberLog.InputTokens)
	require.Equal(t, 2, cyberLog.OutputTokens)

	calls, bodies := upstream.snapshot()
	require.Equal(t, []int64{8801, 8802, 8801, 8802, 8801, 8802}, calls)
	require.Len(t, bodies, 6)
	for _, body := range bodies {
		require.Equal(t, "deepseek-v4-pro", gjson.GetBytes(body, "model").String())
		require.True(t, strings.HasPrefix(gjson.GetBytes(body, "user").String(), "dsu_v1_"))
	}
}

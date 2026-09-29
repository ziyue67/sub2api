package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const wsSSETestRequest = `{"model":"gpt-5.1","stream":true,"instructions":"test","input":[{"role":"user","content":"hello"}]}`

func wsSSETestAccount() *Account {
	return &Account{
		ID: 99101, Name: "ws-sse-test", Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Status: StatusActive, Schedulable: true, Concurrency: 1,
		Credentials: map[string]any{"access_token": "test-oauth-token"},
		Extra: map[string]any{
			"openai_oauth_responses_websockets_v2_enabled": true,
			"openai_oauth_ws_sse_acceleration":             true,
		},
	}
}

func TestOpenAIWSSSEAccelerationRouting(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		name   string
		change func(*Account, *config.Config, *gin.Context)
		body   string
		wantWS bool
	}{
		{name: "opt_in", wantWS: true},
		{name: "default_off", change: func(a *Account, _ *config.Config, _ *gin.Context) {
			delete(a.Extra, "openai_oauth_ws_sse_acceleration")
		}},
		{name: "string_not_boolean", change: func(a *Account, _ *config.Config, _ *gin.Context) {
			a.Extra["openai_oauth_ws_sse_acceleration"] = "true"
		}},
		{name: "api_key", change: func(a *Account, _ *config.Config, _ *gin.Context) { a.Type = AccountTypeAPIKey }},
		{name: "setup_token", change: func(a *Account, _ *config.Config, _ *gin.Context) { a.Type = AccountTypeSetupToken }},
		{name: "other_platform", change: func(a *Account, _ *config.Config, _ *gin.Context) { a.Platform = PlatformAnthropic }},
		{name: "shadow", change: func(a *Account, _ *config.Config, _ *gin.Context) { id := int64(42); a.ParentAccountID = &id }},
		{name: "passthrough", change: func(a *Account, _ *config.Config, _ *gin.Context) { a.Extra["openai_passthrough"] = true }},
		{name: "account_force_http", change: func(a *Account, _ *config.Config, _ *gin.Context) { a.Extra["openai_ws_force_http"] = true }},
		{name: "account_ws_off", change: func(a *Account, _ *config.Config, _ *gin.Context) {
			a.Extra["openai_oauth_responses_websockets_v2_enabled"] = false
		}},
		{name: "global_off", change: func(_ *Account, cfg *config.Config, _ *gin.Context) { cfg.Gateway.OpenAIWS.Enabled = false }},
		{name: "oauth_off", change: func(_ *Account, cfg *config.Config, _ *gin.Context) { cfg.Gateway.OpenAIWS.OAuthEnabled = false }},
		{name: "global_force_http", change: func(_ *Account, cfg *config.Config, _ *gin.Context) { cfg.Gateway.OpenAIWS.ForceHTTP = true }},
		{name: "ws_v1", change: func(_ *Account, cfg *config.Config, _ *gin.Context) {
			cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = false
			cfg.Gateway.OpenAIWS.ResponsesWebsockets = true
		}},
		{name: "compact", change: func(_ *Account, _ *config.Config, c *gin.Context) { c.Request.URL.Path = "/v1/responses/compact" }},
		{name: "chat_completions", change: func(_ *Account, _ *config.Config, c *gin.Context) { c.Request.URL.Path = "/v1/chat/completions" }},
		{name: "nonstream", body: `{"stream":false}`},
		{name: "string_stream", body: `{"stream":"true"}`},
		{name: "previous_response", body: `{"stream":true,"previous_response_id":"resp_previous"}`},
		{name: "messages_bridge", body: `{"stream":true,"prompt_cache_key":"anthropic-cache-demo"}`},
		{name: "agent_identity", change: func(a *Account, _ *config.Config, _ *gin.Context) {
			a.Credentials[openAIAuthModeCredentialKey] = OpenAIAuthModeAgentIdentity
		}},
		{name: "pat", change: func(a *Account, _ *config.Config, _ *gin.Context) {
			a.Credentials[openAIAuthModeCredentialKey] = OpenAIAuthModePersonalAccessToken
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
			a, cfg := wsSSETestAccount(), newOpenAIWSV2TestConfig()
			if tc.change != nil {
				tc.change(a, cfg, c)
			}
			body := tc.body
			if body == "" {
				body = wsSSETestRequest
			}
			s := &OpenAIGatewayService{cfg: cfg}
			d := s.resolveOpenAIHTTPWSSSEDecision(c, a, []byte(body), NewOpenAIWSProtocolResolver(cfg).Resolve(a))
			require.Equal(t, tc.wantWS, d.Transport == OpenAIUpstreamTransportResponsesWebsocketV2)
			if tc.wantWS {
				require.Equal(t, openAIOAuthWSSSEAccelerationReason, d.Reason)
			}
		})
	}
}

type wsSSETestDialer struct {
	conn   openAIWSClientConn
	status int
	err    error
	calls  atomic.Int32
}

func (d *wsSSETestDialer) Dial(context.Context, string, http.Header, string) (openAIWSClientConn, int, http.Header, error) {
	d.calls.Add(1)
	return d.conn, d.status, http.Header{}, d.err
}

func wsSSETestService(t *testing.T, d *wsSSETestDialer, upstream HTTPUpstream) *OpenAIGatewayService {
	t.Helper()
	cfg := newOpenAIWSV2TestConfig()
	cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
	cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 1
	cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 2
	cfg.Gateway.OpenAIWS.EventFlushBatchSize = 100
	cfg.Gateway.OpenAIWS.EventFlushIntervalMS = 60000
	pool := newOpenAIWSConnPool(cfg)
	pool.setClientDialerForTest(d)
	t.Cleanup(pool.Close)
	return &OpenAIGatewayService{cfg: cfg, httpUpstream: upstream, cache: &stubGatewayCache{},
		openaiWSResolver: NewOpenAIWSProtocolResolver(cfg), toolCorrector: NewCodexToolCorrector(), openaiWSPool: pool}
}

type wsSSEFlushWriter struct {
	*httptest.ResponseRecorder
	flushed chan struct{}
	once    sync.Once
}

func (w *wsSSEFlushWriter) Flush() {
	w.ResponseRecorder.Flush()
	if strings.Contains(w.Body.String(), "response.created") && !strings.Contains(w.Body.String(), "output_text.delta") {
		w.once.Do(func() { close(w.flushed) })
	}
}

type wsSSEWaitForFlushConn struct {
	*openAIWSCaptureConn
	flushed <-chan struct{}
	reads   atomic.Int32
}

func (c *wsSSEWaitForFlushConn) ReadMessage(ctx context.Context) ([]byte, error) {
	if c.reads.Add(1) == 2 {
		select {
		case <-c.flushed:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return c.openAIWSCaptureConn.ReadMessage(ctx)
}

func TestOpenAIWSSSEAccelerationFlushesMetadataBeforeContent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := &wsSSEFlushWriter{ResponseRecorder: httptest.NewRecorder(), flushed: make(chan struct{})}
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
	conn := &wsSSEWaitForFlushConn{openAIWSCaptureConn: &openAIWSCaptureConn{events: [][]byte{
		[]byte(`{"type":"response.created","response":{"id":"resp_sse","model":"gpt-5.1"}}`),
		[]byte(`{"type":"response.output_text.delta","delta":"hello"}`),
		[]byte(`{"type":"response.completed","response":{"id":"resp_sse","model":"gpt-5.1","usage":{"input_tokens":7,"output_tokens":2}}}`),
	}}, flushed: w.flushed}
	d := &wsSSETestDialer{conn: conn}
	httpUpstream := &httpUpstreamRecorder{}
	s := wsSSETestService(t, d, httpUpstream)
	result, err := s.Forward(context.Background(), c, wsSSETestAccount(), []byte(wsSSETestRequest))
	require.NoError(t, err)
	require.True(t, result.OpenAIWSMode)
	require.Equal(t, 7, result.Usage.InputTokens)
	require.Equal(t, 2, result.Usage.OutputTokens)
	require.NotNil(t, result.FirstTokenMs)
	require.Equal(t, "text/event-stream", w.Header().Get("Content-Type"))
	require.Contains(t, w.Body.String(), "response.completed")
	require.Nil(t, httpUpstream.lastReq)
	require.EqualValues(t, 1, d.calls.Load())
	conn.mu.Lock()
	defer conn.mu.Unlock()
	require.Len(t, conn.writes, 1)
	require.Equal(t, "response.create", conn.writes[0]["type"])
	require.Equal(t, true, conn.writes[0]["stream"], "preserve the existing OAuth WS payload contract")
}

func TestOpenAIWSSSEAccelerationDoesNotReplayAfterSend(t *testing.T) {
	for _, created := range []bool{false, true} {
		name := "before_first_event"
		if created {
			name = "after_metadata"
		}
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
			conn := &openAIWSCaptureConn{}
			if created {
				conn.events = [][]byte{[]byte(`{"type":"response.created","response":{"id":"resp_broken"}}`)}
			}
			d := &wsSSETestDialer{conn: conn}
			httpUpstream := &httpUpstreamRecorder{}
			s := wsSSETestService(t, d, httpUpstream)
			_, err := s.Forward(context.Background(), c, wsSSETestAccount(), []byte(wsSSETestRequest))
			require.Error(t, err)
			require.Nil(t, httpUpstream.lastReq)
			require.EqualValues(t, 1, d.calls.Load())
			conn.mu.Lock()
			defer conn.mu.Unlock()
			require.Len(t, conn.writes, 1)
			if created {
				require.Contains(t, rec.Body.String(), "response.created")
			}
		})
	}
}

func TestOpenAIWSSSEAccelerationHandshakeFallback(t *testing.T) {
	for _, status := range []int{http.StatusUpgradeRequired, http.StatusForbidden, http.StatusTooManyRequests} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
			d := &wsSSETestDialer{status: status, err: errors.New("handshake refused")}
			event := `data: {"type":"response.completed","response":{"id":"resp_http","usage":{"input_tokens":3,"output_tokens":1}}}` + string([]byte{10, 10})
			httpUpstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK,
				Header: http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:   io.NopCloser(strings.NewReader(event))}}
			s := wsSSETestService(t, d, httpUpstream)
			result, err := s.Forward(context.Background(), c, wsSSETestAccount(), []byte(wsSSETestRequest))
			if status == http.StatusUpgradeRequired {
				require.NoError(t, err)
				require.NotNil(t, httpUpstream.lastReq)
				require.False(t, result.OpenAIWSMode)
				require.Equal(t, 3, result.Usage.InputTokens)
				require.Equal(t, "oauth_ws_sse_handshake_fallback", c.GetString("openai_ws_transport_reason"))
			} else {
				require.Error(t, err)
				require.Nil(t, httpUpstream.lastReq)
			}
			require.EqualValues(t, 1, d.calls.Load())
		})
	}
}

func TestOpenAIWSSSEAccelerationHandshakeFallbackStopsOnCancelOrOutput(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	err := wrapOpenAIWSFallback("upgrade_required", &openAIWSDialError{StatusCode: 426, Err: errors.New("upgrade")})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.False(t, canFallbackOpenAIWSSSEHandshake(ctx, c, err))
	require.False(t, canFallbackOpenAIWSSSEHandshake(context.Background(), c, wrapOpenAIWSFallback("read_event", io.EOF)))
	c.Writer.WriteHeaderNow()
	require.False(t, canFallbackOpenAIWSSSEHandshake(context.Background(), c, err))
}

func TestOpenAIWSSSEAccelerationKeepsPluginPriority(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
	cfg := newOpenAIWSV2TestConfig()
	manager := &PluginManager{}
	manager.route.Store(&pluginRoute{pluginID: 1, rolloutPercent: 100, unavailable: "fixture"})
	s := &OpenAIGatewayService{cfg: cfg, pluginManager: manager}
	a := wsSSETestAccount()
	d := s.resolveOpenAIHTTPWSSSEDecision(c, a, []byte(wsSSETestRequest), NewOpenAIWSProtocolResolver(cfg).Resolve(a))
	require.Equal(t, OpenAIUpstreamTransportHTTPSSE, d.Transport)
}

func TestOpenAIWSSSEAccelerationMetadataDoesNotStartTTFT(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
	conn := &openAIWSCaptureConn{events: [][]byte{
		[]byte(`{"type":"response.created","response":{"id":"resp_metadata"}}`),
		[]byte(`{"type":"response.in_progress","response":{"id":"resp_metadata"}}`),
		[]byte(`{"type":"response.completed","response":{"id":"resp_metadata","usage":{"input_tokens":3,"output_tokens":0}}}`),
	}}
	s := wsSSETestService(t, &wsSSETestDialer{conn: conn}, &httpUpstreamRecorder{})
	result, err := s.Forward(context.Background(), c, wsSSETestAccount(), []byte(wsSSETestRequest))
	require.NoError(t, err)
	require.Nil(t, result.FirstTokenMs)
	require.Contains(t, rec.Body.String(), "response.created")
	require.Contains(t, rec.Body.String(), "response.in_progress")
}

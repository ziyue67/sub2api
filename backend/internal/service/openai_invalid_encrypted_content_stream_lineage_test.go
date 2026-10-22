package service

import (
	"context"
	"encoding/json"
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
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// 流已开始后才出现的 invalid_encrypted_content 无法在本请求内剥离重试；这里
// 覆盖的是下一轮：同一会话带着同一失效密文再次进场时应被预剥离，而不是
// 每轮都被上游以同一密文拒绝。

const streamLineageTestBody = `{"model":"gpt-5.1","stream":true,"input":[{"type":"reasoning","encrypted_content":"gAAA-foreign","summary":[{"type":"summary_text","text":"keep me"}]},{"type":"input_text","text":"hello"}]}`

func newStreamLineageTestContext(sessionID string) (*gin.Context, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
	c.Request.Header.Set("User-Agent", "custom-client/1.0")
	c.Request.Header.Set("session_id", sessionID)
	SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
	return c, rec
}

func newStreamLineageSSEResponse(events ...string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(strings.Join(events, ""))),
	}
}

func newStreamLineageHTTPService(upstream HTTPUpstream) *OpenAIGatewayService {
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	return &OpenAIGatewayService{
		cfg:              cfg,
		httpUpstream:     upstream,
		openaiWSResolver: NewOpenAIWSProtocolResolver(cfg),
	}
}

func newStreamLineageHTTPAccount() *Account {
	return &Account{
		Status:      StatusActive,
		Schedulable: true,
		ID:          471,
		Name:        "openai-apikey",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "sk-test",
			"base_url": "https://api.openai.example",
		},
	}
}

const (
	streamLineageCreatedEvent = "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_stream_lineage\"}}\n\n"
	streamLineageDeltaEvent   = "data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n"
	streamLineageDoneEvent    = "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_stream_lineage_ok\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n" +
		"data: [DONE]\n\n"
)

func TestOpenAIGatewayService_Forward_HTTPStreamInvalidEncryptedContentStripsOnNextTurn(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []struct {
		name        string
		rejection   string
		expectStrip bool
	}{
		{
			name: "bare error event",
			rejection: "data: {\"type\":\"error\",\"error\":{\"code\":\"invalid_encrypted_content\",\"type\":\"invalid_request_error\"," +
				"\"message\":\"The encrypted content gAAA...eign could not be verified. Reason: Encrypted content could not be decrypted or parsed.\"},\"status\":400}\n\n",
			expectStrip: true,
		},
		{
			name: "response.failed nested error",
			rejection: "data: {\"type\":\"response.failed\",\"response\":{\"id\":\"resp_stream_lineage\",\"status\":\"failed\"," +
				"\"error\":{\"code\":\"invalid_encrypted_content\",\"message\":\"The encrypted content gAAA...eign could not be verified. Reason: Encrypted content could not be decrypted or parsed.\"}}}\n\n",
			expectStrip: true,
		},
		{
			name: "unrelated stream failure keeps ciphertext",
			rejection: "data: {\"type\":\"response.failed\",\"response\":{\"id\":\"resp_stream_lineage\",\"status\":\"failed\"," +
				"\"error\":{\"code\":\"server_error\",\"message\":\"Our servers are currently overloaded. Please try again later.\"}}}\n\n",
			expectStrip: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			upstream := &httpUpstreamSequenceRecorder{
				responses: []*http.Response{
					newStreamLineageSSEResponse(streamLineageCreatedEvent, streamLineageDeltaEvent, tc.rejection),
					newStreamLineageSSEResponse(streamLineageCreatedEvent, streamLineageDeltaEvent, streamLineageDoneEvent),
				},
			}
			svc := newStreamLineageHTTPService(upstream)
			account := newStreamLineageHTTPAccount()

			firstCtx, firstRec := newStreamLineageTestContext("sess-stream-lineage")
			_, err := svc.Forward(context.Background(), firstCtx, account, []byte(streamLineageTestBody))
			require.Error(t, err, "流内拒绝应作为本轮失败返回")
			require.Contains(t, firstRec.Body.String(), "partial", "拒绝前已向客户端写出输出，本轮不可重试")
			require.Equal(t, 1, upstream.callCount, "流已开始后不应在本请求内重试")

			secondCtx, _ := newStreamLineageTestContext("sess-stream-lineage")
			result, err := svc.Forward(context.Background(), secondCtx, account, []byte(streamLineageTestBody))
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Len(t, upstream.bodies, 2)

			nextTurn := upstream.bodies[1]
			require.Equal(t, !tc.expectStrip, gjson.GetBytes(nextTurn, "input.0.encrypted_content").Exists(),
				"下一轮是否剥离失效密文")
			require.Equal(t, "keep me", gjson.GetBytes(nextTurn, "input.0.summary.0.text").String(), "剥离只移除密文，保留 reasoning summary")
			require.Equal(t, "hello", gjson.GetBytes(nextTurn, "input.1.text").String())
		})
	}
}

func TestOpenAIGatewayService_Forward_HTTPStreamInvalidEncryptedContentScopedToSession(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rejection := "data: {\"type\":\"error\",\"error\":{\"code\":\"invalid_encrypted_content\",\"type\":\"invalid_request_error\"," +
		"\"message\":\"The encrypted content gAAA...eign could not be verified. Reason: Encrypted content could not be decrypted or parsed.\"},\"status\":400}\n\n"
	upstream := &httpUpstreamSequenceRecorder{
		responses: []*http.Response{
			newStreamLineageSSEResponse(streamLineageCreatedEvent, streamLineageDeltaEvent, rejection),
			newStreamLineageSSEResponse(streamLineageCreatedEvent, streamLineageDeltaEvent, streamLineageDoneEvent),
		},
	}
	svc := newStreamLineageHTTPService(upstream)
	account := newStreamLineageHTTPAccount()

	firstCtx, _ := newStreamLineageTestContext("sess-rejected")
	_, err := svc.Forward(context.Background(), firstCtx, account, []byte(streamLineageTestBody))
	require.Error(t, err)

	otherCtx, _ := newStreamLineageTestContext("sess-unrelated")
	_, err = svc.Forward(context.Background(), otherCtx, account, []byte(streamLineageTestBody))
	require.NoError(t, err)
	require.Len(t, upstream.bodies, 2)
	require.True(t, gjson.GetBytes(upstream.bodies[1], "input.0.encrypted_content").Exists(),
		"lineage 只作用于被拒的会话，其他会话的同名密文不应被剥离")
}

// streamLineageSequenceDialer 按拨号顺序返回预置连接，用于在同一服务实例上模拟
// 连续两轮请求。
type streamLineageSequenceDialer struct {
	mu    sync.Mutex
	conns []*openAIWSCaptureConn
	calls int
}

func (d *streamLineageSequenceDialer) Dial(context.Context, string, http.Header, string) (openAIWSClientConn, int, http.Header, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	idx := d.calls
	d.calls++
	if idx >= len(d.conns) {
		return nil, 0, nil, errors.New("unexpected websocket dial")
	}
	return d.conns[idx], 0, http.Header{}, nil
}

// 生产场景：OAuth 账号开启 HTTP SSE 加速，上游在首个 token 之前（response.created
// 之后）以 WS 错误事件拒绝密文。此时下游尚未写出，但加速路径禁止重放，剥离重试
// 不可达。
func TestOpenAIGatewayService_Forward_WSSSEAccelerationInvalidEncryptedContentStripsOnNextTurn(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rejectedConn := &openAIWSCaptureConn{events: [][]byte{
		[]byte(`{"type":"response.created","response":{"id":"resp_accel_rejected","model":"gpt-5.1"}}`),
		[]byte(`{"type":"error","error":{"code":"invalid_encrypted_content","type":"invalid_request_error","message":"The encrypted content gAAA...eign could not be verified. Reason: Encrypted content could not be decrypted or parsed."}}`),
	}}
	recoveredConn := &openAIWSCaptureConn{events: [][]byte{
		[]byte(`{"type":"response.created","response":{"id":"resp_accel_ok","model":"gpt-5.1"}}`),
		[]byte(`{"type":"response.output_text.delta","delta":"hello"}`),
		[]byte(`{"type":"response.completed","response":{"id":"resp_accel_ok","model":"gpt-5.1","usage":{"input_tokens":3,"output_tokens":1}}}`),
	}}
	dialer := &streamLineageSequenceDialer{conns: []*openAIWSCaptureConn{rejectedConn, recoveredConn}}

	cfg := newOpenAIWSV2TestConfig()
	cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
	cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 1
	cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 2
	pool := newOpenAIWSConnPool(cfg)
	pool.setClientDialerForTest(dialer)
	t.Cleanup(pool.Close)
	httpUpstream := &httpUpstreamRecorder{}
	svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: httpUpstream, cache: &stubGatewayCache{},
		openaiWSResolver: NewOpenAIWSProtocolResolver(cfg), toolCorrector: NewCodexToolCorrector(), openaiWSPool: pool}

	body := []byte(`{"model":"gpt-5.1","stream":true,"instructions":"test","input":[{"type":"reasoning","encrypted_content":"gAAA-foreign","summary":[{"type":"summary_text","text":"keep me"}]},{"role":"user","content":"hello"}]}`)
	newCtx := func() (*gin.Context, *httptest.ResponseRecorder) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		c.Request.Header.Set("session_id", "sess-accel-lineage")
		SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
		return c, rec
	}

	firstCtx, _ := newCtx()
	_, err := svc.Forward(context.Background(), firstCtx, wsSSETestAccount(), body)
	require.Error(t, err)
	require.Equal(t, openAIOAuthWSSSEAccelerationReason, firstCtx.GetString("openai_ws_transport_reason"))
	require.Nil(t, httpUpstream.lastReq, "加速路径的语义拒绝不应回退 HTTP")

	secondCtx, _ := newCtx()
	_, err = svc.Forward(context.Background(), secondCtx, wsSSETestAccount(), body)
	require.NoError(t, err)

	rejectedConn.mu.Lock()
	firstWrites := append([]map[string]any(nil), rejectedConn.writes...)
	rejectedConn.mu.Unlock()
	recoveredConn.mu.Lock()
	secondWrites := append([]map[string]any(nil), recoveredConn.writes...)
	recoveredConn.mu.Unlock()
	require.Len(t, firstWrites, 1, "被拒的一轮不应在本请求内重放")
	require.Len(t, secondWrites, 1)

	firstPayload, _ := json.Marshal(firstWrites[0])
	secondPayload, _ := json.Marshal(secondWrites[0])
	require.True(t, gjson.GetBytes(firstPayload, "input.0.encrypted_content").Exists())
	require.False(t, gjson.GetBytes(secondPayload, "input.0.encrypted_content").Exists(), "下一轮进场应预剥离已被拒绝的密文")
	require.Equal(t, "keep me", gjson.GetBytes(secondPayload, "input.0.summary.0.text").String())
}

func TestOpenAIGatewayService_Forward_WSv2InvalidEncryptedContentAfterOutputStripsOnNextTurn(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var wsAttempts atomic.Int32
	var wsRequestMu sync.Mutex
	var wsRequests [][]byte
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt := wsAttempts.Add(1)
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket failed: %v", err)
			return
		}
		defer func() {
			_ = conn.Close()
		}()

		var req map[string]any
		if err := conn.ReadJSON(&req); err != nil {
			t.Errorf("read ws request failed: %v", err)
			return
		}
		raw, _ := json.Marshal(req)
		wsRequestMu.Lock()
		wsRequests = append(wsRequests, raw)
		wsRequestMu.Unlock()

		_ = conn.WriteJSON(map[string]any{
			"type":     "response.created",
			"response": map[string]any{"id": "resp_ws_stream_lineage", "model": "gpt-5.3-codex"},
		})
		_ = conn.WriteJSON(map[string]any{"type": "response.output_text.delta", "delta": "partial"})
		if attempt == 1 {
			_ = conn.WriteJSON(map[string]any{
				"type": "error",
				"error": map[string]any{
					"code":    "invalid_encrypted_content",
					"type":    "invalid_request_error",
					"message": "The encrypted content gAAA...eign could not be verified. Reason: Encrypted content could not be decrypted or parsed.",
				},
			})
			return
		}
		_ = conn.WriteJSON(map[string]any{
			"type": "response.completed",
			"response": map[string]any{
				"id":    "resp_ws_stream_lineage_ok",
				"model": "gpt-5.3-codex",
				"usage": map[string]any{
					"input_tokens":         1,
					"output_tokens":        1,
					"input_tokens_details": map[string]any{"cached_tokens": 0},
				},
			},
		})
	}))
	defer wsServer.Close()

	cfg := &config.Config{}
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	cfg.Gateway.OpenAIWS.Enabled = true
	cfg.Gateway.OpenAIWS.OAuthEnabled = true
	cfg.Gateway.OpenAIWS.APIKeyEnabled = true
	cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
	cfg.Gateway.OpenAIWS.FallbackCooldownSeconds = 1

	upstream := &httpUpstreamRecorder{}
	svc := &OpenAIGatewayService{
		cfg:              cfg,
		httpUpstream:     upstream,
		openaiWSResolver: NewOpenAIWSProtocolResolver(cfg),
		toolCorrector:    NewCodexToolCorrector(),
	}
	account := &Account{
		Status:      StatusActive,
		Schedulable: true,
		ID:          472,
		Name:        "openai-apikey",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "sk-test",
			"base_url": wsServer.URL,
		},
		Extra: map[string]any{
			"responses_websockets_v2_enabled": true,
		},
	}

	newCtx := func() (*gin.Context, *httptest.ResponseRecorder) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
		c.Request.Header.Set("User-Agent", "custom-client/1.0")
		c.Request.Header.Set("session_id", "sess-ws-stream-lineage")
		return c, rec
	}
	body := []byte(`{"model":"gpt-5.3-codex","stream":true,"input":[{"type":"reasoning","encrypted_content":"gAAA-foreign"},{"type":"input_text","text":"hello"}]}`)

	firstCtx, firstRec := newCtx()
	_, err := svc.Forward(context.Background(), firstCtx, account, body)
	require.Error(t, err, "输出开始后的 WS 拒绝应作为本轮失败返回")
	require.Contains(t, firstRec.Body.String(), "partial", "拒绝前已向客户端写出输出")
	require.Equal(t, int32(1), wsAttempts.Load(), "输出已开始时不应在本请求内重连重试")

	secondCtx, _ := newCtx()
	result, err := svc.Forward(context.Background(), secondCtx, account, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Nil(t, upstream.lastReq, "WS 请求不应回退 HTTP")

	wsRequestMu.Lock()
	requests := append([][]byte(nil), wsRequests...)
	wsRequestMu.Unlock()
	require.Len(t, requests, 2)
	require.True(t, gjson.GetBytes(requests[0], "input.0.encrypted_content").Exists(), "首轮按原样发送密文")
	// 无 summary 的 reasoning 剥离密文后成为空骨架，整项被移除。
	require.Empty(t, gjson.GetBytes(requests[1], "input.#.encrypted_content").Array(), "下一轮进场应预剥离已被拒绝的密文")
	require.Equal(t, "hello", gjson.GetBytes(requests[1], `input.#(type=="input_text").text`).String())
}

// Generic stream failures do not identify which input item was rejected. Keep
// opaque history and ambiguous inputs intact on both the failing and next turn.
func TestHTTPStreamInvalidEncryptedContentPreservesOpaqueAndAmbiguousHistory(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, input := range []string{
		`[{"type":"compaction","encrypted_content":"history"},{"type":"input_text","text":"continue"}]`,
		`[{"type":"compaction_summary","encrypted_content":"history"},{"type":"input_text","text":"continue"}]`,
		`[{"type":"reasoning","encrypted_content":"one"},{"type":"reasoning","encrypted_content":"two"},{"type":"input_text","text":"continue"}]`,
	} {
		t.Run(input, func(t *testing.T) {
			rejection := `data: {"type":"response.failed","response":{"error":{"code":"invalid_encrypted_content","message":"Invalid encrypted content"}}}` + "\n\n"
			upstream := &httpUpstreamSequenceRecorder{responses: []*http.Response{
				newStreamLineageSSEResponse(streamLineageCreatedEvent, streamLineageDeltaEvent, rejection),
				newStreamLineageSSEResponse(streamLineageCreatedEvent, streamLineageDeltaEvent, streamLineageDoneEvent),
			}}
			svc := newStreamLineageHTTPService(upstream)
			account := newStreamLineageHTTPAccount()
			body := []byte(`{"model":"gpt-5.1","stream":true,"input":` + input + `}`)
			first, _ := newStreamLineageTestContext("opaque-history")
			_, err := svc.Forward(context.Background(), first, account, body)
			require.Error(t, err)
			require.Equal(t, 1, upstream.callCount, "no replay after output")
			next, _ := newStreamLineageTestContext("opaque-history")
			_, err = svc.Forward(context.Background(), next, account, body)
			require.NoError(t, err)
			require.Len(t, upstream.bodies, 2)
			require.JSONEq(t, gjson.GetBytes(upstream.bodies[0], "input").Raw, gjson.GetBytes(upstream.bodies[1], "input").Raw)
			require.True(t, gjson.GetBytes(upstream.bodies[1], "input.0.encrypted_content").Exists())
		})
	}
}

func TestIssue494WSAccelerationLocatorNextTurn(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rejectedConn := &openAIWSCaptureConn{events: [][]byte{
		[]byte(`{"type":"response.created","response":{"id":"resp_accel_rejected","model":"gpt-5.1"}}`),
		[]byte(`{"type":"error","error":{"code":"invalid_encrypted_content","type":"invalid_request_error","message":"The encrypted content AAAA...ZZZ1 could not be verified. Reason: Encrypted content could not be decrypted or parsed."}}`),
	}}
	recoveredConn := &openAIWSCaptureConn{events: [][]byte{
		[]byte(`{"type":"response.created","response":{"id":"resp_accel_ok","model":"gpt-5.1"}}`),
		[]byte(`{"type":"response.output_text.delta","delta":"hello"}`),
		[]byte(`{"type":"response.completed","response":{"id":"resp_accel_ok","model":"gpt-5.1","usage":{"input_tokens":3,"output_tokens":1}}}`),
	}}
	dialer := &streamLineageSequenceDialer{conns: []*openAIWSCaptureConn{rejectedConn, recoveredConn}}

	cfg := newOpenAIWSV2TestConfig()
	cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
	cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 1
	cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 2
	pool := newOpenAIWSConnPool(cfg)
	pool.setClientDialerForTest(dialer)
	t.Cleanup(pool.Close)
	httpUpstream := &httpUpstreamRecorder{}
	svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: httpUpstream, cache: &stubGatewayCache{},
		openaiWSResolver: NewOpenAIWSProtocolResolver(cfg), toolCorrector: NewCodexToolCorrector(), openaiWSPool: pool}

	body := []byte(`{"model":"gpt-5.1","stream":true,"instructions":"test","input":[{"type":"reasoning","encrypted_content":"AAAA-synthetic-rejected-ZZZ1","summary":[{"type":"summary_text","text":"keep me"}]},{"type":"reasoning","encrypted_content":"BBBB-synthetic-other-YYY2","summary":[]},{"role":"user","content":"hello"}]}`)
	newCtx := func() (*gin.Context, *httptest.ResponseRecorder) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		c.Request.Header.Set("session_id", "sess-accel-lineage")
		SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
		return c, rec
	}

	firstCtx, _ := newCtx()
	_, err := svc.Forward(context.Background(), firstCtx, wsSSETestAccount(), body)
	require.Error(t, err)
	require.Equal(t, openAIOAuthWSSSEAccelerationReason, firstCtx.GetString("openai_ws_transport_reason"))
	require.Nil(t, httpUpstream.lastReq, "加速路径的语义拒绝不应回退 HTTP")

	secondCtx, _ := newCtx()
	_, err = svc.Forward(context.Background(), secondCtx, wsSSETestAccount(), body)
	require.NoError(t, err)

	rejectedConn.mu.Lock()
	firstWrites := append([]map[string]any(nil), rejectedConn.writes...)
	rejectedConn.mu.Unlock()
	recoveredConn.mu.Lock()
	secondWrites := append([]map[string]any(nil), recoveredConn.writes...)
	recoveredConn.mu.Unlock()
	require.Len(t, firstWrites, 1, "被拒的一轮不应在本请求内重放")
	require.Len(t, secondWrites, 1)

	firstPayload, _ := json.Marshal(firstWrites[0])
	secondPayload, _ := json.Marshal(secondWrites[0])
	require.True(t, gjson.GetBytes(firstPayload, "input.0.encrypted_content").Exists())
	require.False(t, gjson.GetBytes(secondPayload, "input.0.encrypted_content").Exists(), "下一轮进场应预剥离已被拒绝的密文")
	require.Equal(t, locatorB, gjson.GetBytes(secondPayload, "input.1.encrypted_content").String(), "other history is not rejected")
	require.Equal(t, "keep me", gjson.GetBytes(secondPayload, "input.0.summary.0.text").String())
}

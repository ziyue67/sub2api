package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIWSContextWindowSession_UsesFrameWindowBeforeHandshakeNormalization(t *testing.T) {
	for _, tc := range []struct {
		name, header, first, second, third string
		direct, keepPrevious               bool
	}{
		{name: "embedded window with handshake", header: "window-a", first: "window-a", second: "window-b", third: "window-b"},
		{name: "embedded window without handshake", first: "window-a", second: "window-b", third: "window-b"},
		{name: "initial window from handshake", header: "window-a", second: "window-b", third: "window-b"},
		{name: "unchanged frame window overrides handshake", header: "window-a", first: "window-b", second: "window-b", third: "window-b", keepPrevious: true},
		{name: "missing window after rollover", header: "window-a", first: "window-a", second: "window-b"},
		{name: "direct frame window", header: "window-a", first: "window-a", second: "window-b", third: "window-b", direct: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runOpenAIWSContextWindowSession(t, tc.header, tc.first, tc.second, tc.third, tc.direct, tc.keepPrevious)
		})
	}
}

func runOpenAIWSContextWindowSession(t *testing.T, header, first, second, third string, direct, keepPrevious bool) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{}
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	cfg.Gateway.OpenAIWS.Enabled = true
	cfg.Gateway.OpenAIWS.OAuthEnabled = true
	cfg.Gateway.OpenAIWS.APIKeyEnabled = true
	cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
	cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
	cfg.Gateway.OpenAIWS.MinIdlePerAccount = 0
	cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 1
	cfg.Gateway.OpenAIWS.QueueLimitPerConn = 8
	cfg.Gateway.OpenAIWS.DialTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 3

	captureConn := &openAIWSCaptureConn{
		events: [][]byte{
			[]byte(`{"type":"response.completed","response":{"id":"resp_ingress_turn_1","model":"gpt-5.1","usage":{"input_tokens":1,"output_tokens":1}}}`),
			[]byte(`{"type":"response.completed","response":{"id":"resp_ingress_turn_2","model":"gpt-5.1","usage":{"input_tokens":1,"output_tokens":1}}}`),
			[]byte(`{"type":"response.completed","response":{"id":"resp_ingress_turn_3","model":"gpt-5.1","usage":{"input_tokens":1,"output_tokens":1}}}`),
		},
	}
	captureDialer := &openAIWSCaptureDialer{conn: captureConn}
	pool := newOpenAIWSConnPool(cfg)
	defer pool.Close()
	pool.setClientDialerForTest(captureDialer)

	svc := &OpenAIGatewayService{
		cfg:              cfg,
		httpUpstream:     &httpUpstreamRecorder{},
		cache:            &stubGatewayCache{},
		openaiWSResolver: NewOpenAIWSProtocolResolver(cfg),
		toolCorrector:    NewCodexToolCorrector(),
		openaiWSPool:     pool,
	}

	account := &Account{
		ID:          114,
		Name:        "openai-ingress-window-boundary",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key": "sk-test",
		},
		Extra: map[string]any{
			"responses_websockets_v2_enabled": true,
		},
	}

	serverErrCh := make(chan error, 1)
	turnTerminalCh := make(chan string, 3)
	hooks := &OpenAIWSIngressHooks{
		AfterTurn: func(_ int, result *OpenAIForwardResult, turnErr error) {
			if turnErr == nil && result != nil {
				turnTerminalCh <- result.UpstreamTerminalEvent
			}
		},
	}
	wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, &coderws.AcceptOptions{
			CompressionMode: coderws.CompressionContextTakeover,
		})
		if err != nil {
			serverErrCh <- err
			return
		}
		defer func() {
			_ = conn.CloseNow()
		}()

		rec := httptest.NewRecorder()
		ginCtx, _ := gin.CreateTestContext(rec)
		req := r.Clone(r.Context())
		req.Header = req.Header.Clone()
		req.Header.Set("User-Agent", "unit-test-agent/1.0")
		ginCtx.Request = req

		readCtx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		msgType, firstMessage, readErr := conn.Read(readCtx)
		cancel()
		if readErr != nil {
			serverErrCh <- readErr
			return
		}
		if msgType != coderws.MessageText && msgType != coderws.MessageBinary {
			serverErrCh <- errors.New("unsupported websocket client message type")
			return
		}

		serverErrCh <- svc.ProxyResponsesWebSocketFromClient(r.Context(), ginCtx, conn, account, "sk-test", firstMessage, hooks)
	}))
	defer wsServer.Close()

	dialCtx, cancelDial := context.WithTimeout(context.Background(), 3*time.Second)
	headers := http.Header{}
	if header != "" {
		headers.Set(openAIWSTurnMetadataHeader, fmt.Sprintf(`{"window_id":%q}`, header))
	}
	clientConn, _, err := coderws.Dial(dialCtx, "ws"+strings.TrimPrefix(wsServer.URL, "http"), &coderws.DialOptions{HTTPHeader: headers})
	cancelDial()
	require.NoError(t, err)
	defer func() {
		_ = clientConn.CloseNow()
	}()

	writeMessage := func(payload string) {
		writeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		require.NoError(t, clientConn.Write(writeCtx, coderws.MessageText, []byte(payload)))
	}
	readMessage := func() []byte {
		readCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		msgType, message, readErr := clientConn.Read(readCtx)
		require.NoError(t, readErr)
		require.Equal(t, coderws.MessageText, msgType)
		return message
	}

	makePayload := func(window, previous string) string {
		payload := map[string]any{"type": "response.create", "model": "gpt-5.1", "store": true, "input": "new context"}
		if previous != "" {
			payload["previous_response_id"] = previous
		}
		if window != "" {
			metadata := map[string]any{}
			if direct {
				metadata["x-codex-window-id"] = window
			} else {
				metadata[openAIWSTurnMetadataHeader] = fmt.Sprintf(`{"window_id":%q}`, window)
			}
			payload["client_metadata"] = metadata
		}
		encoded, err := json.Marshal(payload)
		require.NoError(t, err)
		return string(encoded)
	}
	writeMessage(makePayload(first, ""))
	firstTurnEvent := readMessage()
	require.Equal(t, "response.completed", gjson.GetBytes(firstTurnEvent, "type").String())
	require.Equal(t, "resp_ingress_turn_1", gjson.GetBytes(firstTurnEvent, "response.id").String())

	writeMessage(makePayload(second, "resp_ingress_turn_1"))
	secondTurnEvent := readMessage()
	require.Equal(t, "response.completed", gjson.GetBytes(secondTurnEvent, "type").String())
	require.Equal(t, "resp_ingress_turn_2", gjson.GetBytes(secondTurnEvent, "response.id").String())
	require.Equal(t, "response.completed", <-turnTerminalCh, "首轮 turn 应保留成功终态")
	require.Equal(t, "response.completed", <-turnTerminalCh, "第二轮 turn 应保留成功终态")

	writeMessage(makePayload(third, "resp_ingress_turn_2"))
	thirdEvent := readMessage()
	require.Equal(t, "resp_ingress_turn_3", gjson.GetBytes(thirdEvent, "response.id").String())
	require.Equal(t, "response.completed", <-turnTerminalCh)
	_ = clientConn.Close(coderws.StatusNormalClosure, "done")

	select {
	case serverErr := <-serverErrCh:
		require.NoError(t, serverErr)
	case <-time.After(5 * time.Second):
		t.Fatal("等待 ingress websocket 结束超时")
	}

	metrics := svc.SnapshotOpenAIWSPoolMetrics()
	require.Equal(t, int64(1), metrics.AcquireTotal, "同一 ingress 会话多 turn 应只获取一次上游 lease")
	require.Equal(t, 1, captureDialer.DialCount(), "同一 ingress 会话应保持同一上游连接")
	captureConn.mu.Lock()
	defer captureConn.mu.Unlock()
	require.Len(t, captureConn.writes, 3)
	if keepPrevious {
		require.Equal(t, "resp_ingress_turn_1", captureConn.writes[1]["previous_response_id"])
	} else {
		require.NotContains(t, captureConn.writes[1], "previous_response_id")
	}
	require.Equal(t, "resp_ingress_turn_2", captureConn.writes[2]["previous_response_id"], "same or missing window must preserve the new continuation")
	if header != "" {
		metadata, ok := captureConn.writes[1]["client_metadata"].(map[string]any)
		require.True(t, ok)
		require.Equal(t, headers.Get(openAIWSTurnMetadataHeader), metadata[openAIWSTurnMetadataHeader], "window detection must preserve handshake identity normalization")
	}
}

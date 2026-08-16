package service

import (
	"bytes"
	"context"
	"encoding/json"
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

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func deepSeekWSTestConfig() *config.Config {
	cfg := deepSeekForwardTestConfig()
	cfg.JWT.Secret = "deepseek-ws-test-jwt-secret-32-bytes"
	cfg.Gateway.OpenAIWS.Enabled = true
	cfg.Gateway.OpenAIWS.ModeRouterV2Enabled = true
	cfg.Gateway.OpenAIWS.HTTPBridgeEnabled = true
	cfg.Gateway.OpenAIWS.IngressInterTurnIdleTimeoutSeconds = 2
	cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 2
	return cfg
}

func deepSeekWSTestAccount() *Account {
	account := deepSeekForwardTestAccount()
	account.Extra = map[string]any{
		DeepSeekUserIsolationModeKey:      DeepSeekUserIsolationModeAuthenticatedUser,
		DeepSeekResponsesWebSocketModeKey: DeepSeekResponsesWebSocketModeHTTPBridge,
	}
	return account
}

func newDeepSeekWSTestGinContext(ctx context.Context) *gin.Context {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil).WithContext(ctx)
	c.Request.Header.Set("User-Agent", "codex-test")
	return c
}

func TestDeepSeekResponsesWebSocketModeResolutionAndValidation(t *testing.T) {
	cfg := deepSeekWSTestConfig()
	require.True(t, DeepSeekResponsesWSHTTPBridgeEnabled(cfg))
	cfg.Gateway.OpenAIWS.HTTPBridgeEnabled = false
	require.False(t, DeepSeekResponsesWSHTTPBridgeEnabled(cfg))

	account := deepSeekWSTestAccount()
	require.Equal(t, DeepSeekResponsesWebSocketModeHTTPBridge, account.ResolveDeepSeekResponsesWebSocketMode(true))
	require.Equal(t, DeepSeekResponsesWebSocketModeOff, account.ResolveDeepSeekResponsesWebSocketMode(false))
	delete(account.Extra, DeepSeekResponsesWebSocketModeKey)
	require.Equal(t, DeepSeekResponsesWebSocketModeHTTPBridge, account.ResolveDeepSeekResponsesWebSocketMode(true))
	account.Extra[DeepSeekResponsesWebSocketModeKey] = DeepSeekResponsesWebSocketModeOff
	require.Equal(t, DeepSeekResponsesWebSocketModeOff, account.ResolveDeepSeekResponsesWebSocketMode(true))
	for _, invalid := range []any{"", "ctx_pool", "passthrough", "dedicated", nil, true} {
		account.Extra[DeepSeekResponsesWebSocketModeKey] = invalid
		require.Equal(t, DeepSeekResponsesWebSocketModeOff, account.ResolveDeepSeekResponsesWebSocketMode(true))
		_, err := normalizeDeepSeekAccountExtra(PlatformDeepSeek, account.Extra, DeepSeekUserIsolationModeAuthenticatedUser)
		require.Error(t, err)
	}
	_, err := normalizeDeepSeekAccountExtra(PlatformOpenAI, map[string]any{
		DeepSeekResponsesWebSocketModeKey: DeepSeekResponsesWebSocketModeHTTPBridge,
	}, DeepSeekUserIsolationModeOff)
	require.Error(t, err)
}

func TestAdminDeepSeekResponsesWebSocketModeExtraUpdatesFailClosed(t *testing.T) {
	const deepSeekAccountID int64 = 71
	const openAIAccountID int64 = 72
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{
		deepSeekAccountID: {
			ID:       deepSeekAccountID,
			Platform: PlatformDeepSeek,
			Extra:    map[string]any{DeepSeekUserIsolationModeKey: DeepSeekUserIsolationModeAuthenticatedUser},
		},
		openAIAccountID: {ID: openAIAccountID, Platform: PlatformOpenAI},
	}}
	svc := &adminServiceImpl{accountRepo: repo}

	err := svc.UpdateAccountExtra(context.Background(), deepSeekAccountID, map[string]any{
		DeepSeekResponsesWebSocketModeKey: "ctx_pool",
	})
	require.Equal(t, "DEEPSEEK_RESPONSES_WEBSOCKET_MODE_INVALID", infraerrors.Reason(err))
	require.Empty(t, repo.updates)

	err = svc.UpdateAccountExtra(context.Background(), openAIAccountID, map[string]any{
		DeepSeekResponsesWebSocketModeKey: DeepSeekResponsesWebSocketModeHTTPBridge,
	})
	require.Equal(t, "DEEPSEEK_RESPONSES_WEBSOCKET_MODE_PLATFORM_INVALID", infraerrors.Reason(err))
	require.Empty(t, repo.updates)

	err = svc.UpdateAccountExtra(context.Background(), deepSeekAccountID, map[string]any{
		DeepSeekResponsesWebSocketModeKey: "  HTTP_BRIDGE  ",
	})
	require.NoError(t, err)
	require.Len(t, repo.updates[deepSeekAccountID], 1)
	require.Equal(t, DeepSeekResponsesWebSocketModeHTTPBridge, repo.updates[deepSeekAccountID][0][DeepSeekResponsesWebSocketModeKey])
	require.NotContains(t, repo.updates[deepSeekAccountID][0], DeepSeekUserIsolationModeKey, "a WS-only patch must not persist an unrelated default")
}

func TestAdminBulkDeepSeekResponsesWebSocketModeUpdatesFailClosed(t *testing.T) {
	const deepSeekAccountID int64 = 81
	const openAIAccountID int64 = 82
	newRepo := func() *upstreamBillingProbeAccountRepo {
		return &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{
			deepSeekAccountID: {ID: deepSeekAccountID, Platform: PlatformDeepSeek},
			openAIAccountID:   {ID: openAIAccountID, Platform: PlatformOpenAI},
		}}
	}

	repo := newRepo()
	_, err := (&adminServiceImpl{accountRepo: repo}).BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{
		AccountIDs: []int64{deepSeekAccountID},
		Extra:      map[string]any{DeepSeekResponsesWebSocketModeKey: "passthrough"},
	})
	require.Equal(t, "DEEPSEEK_RESPONSES_WEBSOCKET_MODE_INVALID", infraerrors.Reason(err))
	require.Empty(t, repo.bulkUpdates)

	repo = newRepo()
	_, err = (&adminServiceImpl{accountRepo: repo}).BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{
		AccountIDs: []int64{openAIAccountID},
		Extra:      map[string]any{DeepSeekResponsesWebSocketModeKey: DeepSeekResponsesWebSocketModeOff},
	})
	require.Equal(t, "DEEPSEEK_RESPONSES_WEBSOCKET_MODE_PLATFORM_INVALID", infraerrors.Reason(err))
	require.Empty(t, repo.bulkUpdates)

	repo = newRepo()
	result, err := (&adminServiceImpl{accountRepo: repo}).BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{
		AccountIDs: []int64{deepSeekAccountID},
		Extra:      map[string]any{DeepSeekResponsesWebSocketModeKey: " HTTP_BRIDGE "},
	})
	require.NoError(t, err)
	require.Equal(t, 1, result.Success)
	require.Len(t, repo.bulkUpdates, 1)
	require.Equal(t, DeepSeekResponsesWebSocketModeHTTPBridge, repo.bulkUpdates[0].Extra[DeepSeekResponsesWebSocketModeKey])
}

func TestForwardDeepSeekResponsesWebSocketTurnUsesNativeHTTPResponses(t *testing.T) {
	ctx := context.WithValue(context.Background(), ctxkey.UserID, int64(42))
	c := newDeepSeekWSTestGinContext(ctx)
	sse := strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp_ds_ws","model":"deepseek-v4"}}`,
		"",
		`data: {"type":"response.output_item.done","output_index":0,"item":{"id":"msg_ds_ws","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"ok"}],"unknown":{"kept":true}}}`,
		"",
		`data: {"type":"response.completed","response":{"id":"resp_ds_ws","model":"deepseek-v4","status":"completed","output":[{"id":"msg_ds_ws","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"ok"}],"unknown":{"kept":true}}],"usage":{"input_tokens":7,"output_tokens":3,"input_tokens_details":{"cached_tokens":2},"output_tokens_details":{"reasoning_tokens":1}}}}`,
		"",
	}, "\n")
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type":          []string{"text/event-stream"},
			"x-deepseek-request-id": []string{"rid_ds_ws"},
		},
		Body: io.NopCloser(strings.NewReader(sse)),
	}}
	svc := &OpenAIGatewayService{cfg: deepSeekWSTestConfig(), httpUpstream: upstream}
	payload := []byte(`{"type":"response.create","generate":true,"model":"deepseek-v4","previous_response_id":"resp_local","stream":false,"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}],"unknown":{"kept":true},"user":"spoof"}`)
	var writes [][]byte
	result, err := svc.ForwardDeepSeekResponsesWebSocketTurn(ctx, c, deepSeekWSTestAccount(), "sk-deepseek-test", payload, 1, func(event []byte) error {
		writes = append(writes, append([]byte(nil), event...))
		return nil
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "http://deepseek.example/responses", upstream.lastReq.URL.String())
	require.True(t, HTTPUpstreamRedirectsDisabled(upstream.lastReq.Context()))
	require.Equal(t, "Bearer sk-deepseek-test", upstream.lastReq.Header.Get("Authorization"))
	require.False(t, gjson.GetBytes(upstream.lastBody, "type").Exists())
	require.False(t, gjson.GetBytes(upstream.lastBody, "generate").Exists())
	require.False(t, gjson.GetBytes(upstream.lastBody, "previous_response_id").Exists())
	require.True(t, gjson.GetBytes(upstream.lastBody, "stream").Bool())
	require.True(t, gjson.GetBytes(upstream.lastBody, "unknown.kept").Bool())
	require.True(t, strings.HasPrefix(gjson.GetBytes(upstream.lastBody, "user").String(), "dsu_v1_"))
	require.Len(t, writes, 3)
	require.Equal(t, "response.created", gjson.GetBytes(writes[0], "type").String())
	require.Equal(t, "response.output_item.done", gjson.GetBytes(writes[1], "type").String())
	require.Equal(t, "response.completed", gjson.GetBytes(writes[2], "type").String())
	require.True(t, gjson.GetBytes(writes[1], "item.unknown.kept").Bool())
	require.Equal(t, 7, result.Usage.InputTokens)
	require.Equal(t, 3, result.Usage.OutputTokens)
	require.Equal(t, 2, result.Usage.CacheReadInputTokens)
	require.Equal(t, 1, result.Usage.ReasoningTokens)
	require.Equal(t, deepSeekResponsesEndpoint, result.UpstreamEndpoint)
	require.Equal(t, "rid_ds_ws", result.RequestID)
	require.True(t, result.OpenAIWSMode)
	require.True(t, result.wsReplayInputExists)
	require.Len(t, result.wsReplayInput, 1)
}

func TestForwardDeepSeekResponsesWebSocketTurnAppliesAccountModelMapping(t *testing.T) {
	ctx := context.WithValue(context.Background(), ctxkey.UserID, int64(42))
	c := newDeepSeekWSTestGinContext(ctx)
	sse := "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_mapped\",\"model\":\"deepseek-account-model\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":4,\"output_tokens\":2}}}\n\n"
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(sse)),
	}}
	account := deepSeekWSTestAccount()
	account.Credentials["model_mapping"] = map[string]any{"deepseek-public-model": "deepseek-account-model"}
	svc := &OpenAIGatewayService{cfg: deepSeekWSTestConfig(), httpUpstream: upstream}

	result, err := svc.ForwardDeepSeekResponsesWebSocketTurn(
		ctx,
		c,
		account,
		account.GetDeepSeekAPIKey(),
		[]byte(`{"model":"deepseek-public-model","input":"hello"}`),
		4,
		func([]byte) error { return nil },
	)
	require.NoError(t, err)
	require.Equal(t, "deepseek-account-model", gjson.GetBytes(upstream.lastBody, "model").String())
	require.Equal(t, "deepseek-public-model", result.Model)
	require.Equal(t, "deepseek-account-model", result.BillingModel)
	require.Equal(t, "deepseek-account-model", result.UpstreamModel)
	require.Equal(t, "deepseek-ws:normal:turn:4:resp_mapped", result.RequestID)
}

func TestForwardDeepSeekResponsesWebSocketTurnDoesNotFailoverAfterSemanticOutput(t *testing.T) {
	ctx := context.WithValue(context.Background(), ctxkey.UserID, int64(42))
	c := newDeepSeekWSTestGinContext(ctx)
	sse := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_missing_usage\"}}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_missing_usage\",\"status\":\"completed\",\"output\":[]}}\n\n"
	upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(sse))}}
	svc := &OpenAIGatewayService{cfg: deepSeekWSTestConfig(), httpUpstream: upstream}
	var writes int
	result, err := svc.ForwardDeepSeekResponsesWebSocketTurn(ctx, c, deepSeekWSTestAccount(), "sk-deepseek-test", []byte(`{"model":"deepseek-v4","input":"hello"}`), 1, func([]byte) error {
		writes++
		return nil
	})
	require.Error(t, err)
	require.NotNil(t, result)
	require.Equal(t, 2, writes)
	var failoverErr *UpstreamFailoverError
	require.False(t, errors.As(err, &failoverErr))
}

func TestForwardDeepSeekResponsesWebSocketTurnRejectsInvalidSuccessWire(t *testing.T) {
	tests := []struct {
		name      string
		sse       string
		wantCyber bool
	}{
		{
			name: "cyber error followed by completed",
			sse: "data: {\"type\":\"error\",\"error\":{\"code\":\"cyber_policy\",\"message\":\"blocked\"}}\n\n" +
				"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_cyber\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":9,\"output_tokens\":2}}}\n\n",
			wantCyber: true,
		},
		{
			name: "conflicting response ids",
			sse: "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_first\"}}\n\n" +
				"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_second\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":3,\"output_tokens\":1}}}\n\n",
		},
		{
			name: "terminal type status mismatch",
			sse:  "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_bad_status\",\"status\":\"failed\",\"output\":[],\"usage\":{\"input_tokens\":3,\"output_tokens\":1}}}\n\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.WithValue(context.Background(), ctxkey.UserID, int64(42))
			c := newDeepSeekWSTestGinContext(ctx)
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(tt.sse)),
			}}
			svc := &OpenAIGatewayService{cfg: deepSeekWSTestConfig(), httpUpstream: upstream}
			var writes [][]byte
			result, err := svc.ForwardDeepSeekResponsesWebSocketTurn(ctx, c, deepSeekWSTestAccount(), "sk-deepseek-test", []byte(`{"model":"deepseek-v4","input":"hello"}`), 1, func(event []byte) error {
				writes = append(writes, append([]byte(nil), event...))
				return nil
			})
			require.Error(t, err)
			require.NotNil(t, result)
			require.NotEmpty(t, writes, "native upstream events should remain visible to the client")
			var failoverErr *UpstreamFailoverError
			require.False(t, errors.As(err, &failoverErr))
			if tt.wantCyber {
				mark := GetOpsCyberPolicy(c)
				require.NotNil(t, mark)
				require.Equal(t, "cyber_policy", mark.Code)
				require.Equal(t, 9, mark.UpstreamInTok)
				require.Equal(t, 2, mark.UpstreamOutTok)
				require.True(t, IsDeepSeekWSAccountNeutralError(err), "SSE cyber failures must not affect account health")
			}
		})
	}
}

func TestForwardDeepSeekResponsesWebSocketTurnMarksHTTPCyberPolicyBeforeFailover(t *testing.T) {
	ctx := context.WithValue(context.Background(), ctxkey.UserID, int64(42))
	c := newDeepSeekWSTestGinContext(ctx)
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusBadRequest,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":{"code":"cyber_policy","message":"blocked"}}`)),
	}}
	svc := &OpenAIGatewayService{cfg: deepSeekWSTestConfig(), httpUpstream: upstream}
	var writes [][]byte
	result, err := svc.ForwardDeepSeekResponsesWebSocketTurn(ctx, c, deepSeekWSTestAccount(), "sk-deepseek-test", []byte(`{"model":"deepseek-v4","input":"hello","reasoning":{"effort":"max"}}`), 1, func(event []byte) error {
		writes = append(writes, append([]byte(nil), event...))
		return nil
	})

	require.Error(t, err)
	require.NotNil(t, result)
	require.Equal(t, "deepseek-v4", result.Model)
	require.Equal(t, "deepseek-v4", result.BillingModel)
	require.Equal(t, "deepseek-v4", result.UpstreamModel)
	require.NotNil(t, result.ReasoningEffort)
	require.Equal(t, "max", *result.ReasoningEffort)
	require.True(t, result.Stream)
	require.True(t, result.OpenAIWSMode)
	var failoverErr *UpstreamFailoverError
	require.False(t, errors.As(err, &failoverErr), "cyber policy must never retry another account")
	require.True(t, IsDeepSeekWSAccountNeutralError(err), "cyber policy must not affect account health")
	var closeErr *OpenAIWSClientCloseError
	require.ErrorAs(t, err, &closeErr)
	require.Equal(t, coderws.StatusPolicyViolation, closeErr.StatusCode())
	require.Contains(t, result.RequestID, "deepseek-ws:cyber:turn:1:")
	require.Len(t, writes, 1)
	require.Equal(t, "error", gjson.GetBytes(writes[0], "type").String())
	mark := GetOpsCyberPolicy(c)
	require.NotNil(t, mark)
	require.Equal(t, "cyber_policy", mark.Code)
	require.Equal(t, http.StatusBadRequest, mark.UpstreamStatus)
}

func TestForwardDeepSeekResponsesWebSocketTurnMarksDeterministicClientErrorsAccountNeutral(t *testing.T) {
	for _, statusCode := range []int{http.StatusBadRequest, http.StatusUnprocessableEntity} {
		t.Run(fmt.Sprintf("status_%d", statusCode), func(t *testing.T) {
			ctx := context.WithValue(context.Background(), ctxkey.UserID, int64(42))
			c := newDeepSeekWSTestGinContext(ctx)
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: statusCode,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"error":{"type":"invalid_request_error","message":"invalid client field"}}`)),
			}}
			svc := &OpenAIGatewayService{cfg: deepSeekWSTestConfig(), httpUpstream: upstream}
			writes := 0
			result, err := svc.ForwardDeepSeekResponsesWebSocketTurn(
				ctx,
				c,
				deepSeekWSTestAccount(),
				"sk-deepseek-test",
				[]byte(`{"model":"deepseek-v4","input":"hello"}`),
				1,
				func([]byte) error { writes++; return nil },
			)

			require.Nil(t, result)
			require.Error(t, err)
			require.True(t, IsDeepSeekWSAccountNeutralError(err))
			var closeErr *OpenAIWSClientCloseError
			require.ErrorAs(t, err, &closeErr)
			require.Equal(t, coderws.StatusPolicyViolation, closeErr.StatusCode())
			var failoverErr *UpstreamFailoverError
			require.False(t, errors.As(err, &failoverErr))
			require.Equal(t, 1, writes)
		})
	}
}

func TestEnsureDeepSeekWSUsageRequestIDSeparatesTurnKinds(t *testing.T) {
	normalCtx := newDeepSeekWSTestGinContext(context.Background())
	normal := &OpenAIForwardResult{ResponseID: "resp_shared", OpenAIWSMode: true}
	ensureDeepSeekWSUsageRequestID(normalCtx, normal, 1)
	require.Equal(t, "deepseek-ws:normal:turn:1:resp_shared", normal.RequestID)

	compactCtx := newDeepSeekWSTestGinContext(context.Background())
	compact := &OpenAIForwardResult{ResponseID: "resp_shared", RequestKind: UsageRequestKindCompact, OpenAIWSMode: true}
	ensureDeepSeekWSUsageRequestID(compactCtx, compact, 1)
	require.Equal(t, "deepseek-ws:compact:turn:1:resp_shared", compact.RequestID)

	cyberCtx := newDeepSeekWSTestGinContext(context.Background())
	MarkOpsCyberPolicy(cyberCtx, CyberPolicyMark{Code: "cyber_policy"})
	cyber := &OpenAIForwardResult{ResponseID: "resp_shared", OpenAIWSMode: true}
	ensureDeepSeekWSUsageRequestID(cyberCtx, cyber, 1)
	require.Equal(t, "deepseek-ws:cyber:turn:1:resp_shared", cyber.RequestID)
}

func TestForwardDeepSeekResponsesWebSocketTurnClientWriteFailuresAreGoingAway(t *testing.T) {
	clientGone := errors.New("client websocket disconnected")

	t.Run("HTTP error event", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), ctxkey.UserID, int64(42))
		c := newDeepSeekWSTestGinContext(ctx)
		upstream := &httpUpstreamRecorder{resp: &http.Response{
			StatusCode: http.StatusBadRequest,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":{"type":"invalid_request_error","message":"invalid client field"}}`)),
		}}
		svc := &OpenAIGatewayService{cfg: deepSeekWSTestConfig(), httpUpstream: upstream}

		result, err := svc.ForwardDeepSeekResponsesWebSocketTurn(
			ctx,
			c,
			deepSeekWSTestAccount(),
			"sk-deepseek-test",
			[]byte(`{"model":"deepseek-v4","input":"hello"}`),
			1,
			func([]byte) error { return clientGone },
		)
		require.Nil(t, result)
		var closeErr *OpenAIWSClientCloseError
		require.ErrorAs(t, err, &closeErr)
		require.Equal(t, coderws.StatusGoingAway, closeErr.StatusCode())
	})

	t.Run("compact success event", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), ctxkey.UserID, int64(51))
		c := newDeepSeekWSTestGinContext(ctx)
		MarkDeepSeekCompaction(c, DeepSeekCompactionModeRemoteV2SSE)
		completed := `{"type":"response.completed","response":{"id":"resp_summary_disconnect","status":"completed","output":[{"id":"msg_summary","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"checkpoint summary"}]}],"usage":{"input_tokens":21,"output_tokens":5}}}`
		upstream := &httpUpstreamRecorder{resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader("data: " + completed + "\n\n")),
		}}
		svc := &OpenAIGatewayService{cfg: deepSeekWSTestConfig(), httpUpstream: upstream}
		payload := []byte(`{"model":"deepseek-v4","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"large history"}]},{"type":"compaction_trigger"}]}`)

		result, err := svc.ForwardDeepSeekResponsesWebSocketTurn(
			ctx,
			c,
			deepSeekWSTestAccount(),
			"sk-deepseek-test",
			payload,
			1,
			func([]byte) error { return clientGone },
		)
		require.NotNil(t, result)
		require.True(t, result.OpenAIWSMode)
		require.Equal(t, UsageRequestKindCompact, result.RequestKind)
		var closeErr *OpenAIWSClientCloseError
		require.ErrorAs(t, err, &closeErr)
		require.Equal(t, coderws.StatusGoingAway, closeErr.StatusCode())
	})
}

func TestForwardDeepSeekResponsesWebSocketTurnFailsClosedOnSplitCredentialCanary(t *testing.T) {
	ctx := context.WithValue(context.Background(), ctxkey.UserID, int64(73))
	cfg := deepSeekWSTestConfig()
	account := deepSeekWSTestAccount()
	derivedID, err := DeriveDeepSeekAuthenticatedUserID(ctx, cfg)
	require.NoError(t, err)

	tests := []struct {
		name        string
		secret      string
		byteByByte  bool
		changeIndex bool
	}{
		{name: "api key across changed item metadata", secret: account.GetDeepSeekAPIKey(), changeIndex: true},
		{name: "current derived user id", secret: derivedID},
		{name: "foreign derived user id byte by byte", secret: deepSeekUserIDPrefix + strings.Repeat("A", deepSeekUserIDEncodedDigestBytes), byteByByte: true, changeIndex: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			makeDelta := func(delta string, index int) string {
				itemID := "msg_secret"
				if tt.changeIndex && index > 0 {
					itemID = "msg_changed"
				}
				body, marshalErr := json.Marshal(map[string]any{
					"type": "response.output_text.delta", "item_id": itemID, "output_index": index, "content_index": 0, "delta": delta,
				})
				require.NoError(t, marshalErr)
				return "data: " + string(body) + "\n\n"
			}
			deltas := []string{tt.secret[:1], tt.secret[1:]}
			if tt.byteByByte {
				deltas = make([]string, 0, len(tt.secret))
				for _, char := range tt.secret {
					deltas = append(deltas, string(char))
				}
			}
			var sse strings.Builder
			_, _ = sse.WriteString("data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_split_secret\"}}\n\n")
			for index, delta := range deltas {
				_, _ = sse.WriteString(makeDelta(delta, index))
			}
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(sse.String())),
			}}
			svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: upstream}
			var writes [][]byte
			result, forwardErr := svc.ForwardDeepSeekResponsesWebSocketTurn(ctx, newDeepSeekWSTestGinContext(ctx), account, account.GetDeepSeekAPIKey(), []byte(`{"model":"deepseek-v4","input":"hello"}`), 1, func(event []byte) error {
				writes = append(writes, append([]byte(nil), event...))
				return nil
			})
			require.ErrorIs(t, forwardErr, errDeepSeekWSSensitiveDelta)
			require.NotNil(t, result)
			wire := string(bytes.Join(writes, nil))
			require.NotContains(t, wire, tt.secret)
			var failoverErr *UpstreamFailoverError
			require.False(t, errors.As(forwardErr, &failoverErr))
		})
	}
}

func TestForwardDeepSeekResponsesWebSocketTurnFailsClosedOnInterleavedCredentialCanary(t *testing.T) {
	ctx := context.WithValue(context.Background(), ctxkey.UserID, int64(73))
	account := deepSeekWSTestAccount()
	secret := account.GetDeepSeekAPIKey()
	makeDelta := func(itemID, delta string) string {
		body, err := json.Marshal(map[string]any{
			"type": "response.output_text.delta", "item_id": itemID, "output_index": 0, "content_index": 0, "delta": delta,
		})
		require.NoError(t, err)
		return "data: " + string(body) + "\n\n"
	}
	sse := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_interleaved_secret\"}}\n\n" +
		makeDelta("msg_a", secret[:1]) +
		makeDelta("msg_b", "benign interleaving") +
		makeDelta("msg_a", secret[1:])
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(sse)),
	}}
	svc := &OpenAIGatewayService{cfg: deepSeekWSTestConfig(), httpUpstream: upstream}
	var writes [][]byte
	result, err := svc.ForwardDeepSeekResponsesWebSocketTurn(ctx, newDeepSeekWSTestGinContext(ctx), account, secret, []byte(`{"model":"deepseek-v4","input":"hello"}`), 1, func(event []byte) error {
		writes = append(writes, append([]byte(nil), event...))
		return nil
	})

	require.ErrorIs(t, err, errDeepSeekWSSensitiveDelta)
	require.NotNil(t, result)
	require.NotContains(t, string(bytes.Join(writes, nil)), secret)
	var failoverErr *UpstreamFailoverError
	require.False(t, errors.As(err, &failoverErr))
}

func TestDeepSeekWSSensitiveDeltaGuardDetectsOverlappingPrefix(t *testing.T) {
	guard := &deepSeekWSSensitiveDeltaGuard{secrets: []string{"abcabx"}}
	makeDelta := func(delta string) []byte {
		body, err := json.Marshal(map[string]any{
			"type": "response.output_text.delta", "item_id": "msg_overlap", "delta": delta,
		})
		require.NoError(t, err)
		return body
	}
	var writes [][]byte
	write := func(message []byte) error {
		writes = append(writes, append([]byte(nil), message...))
		return nil
	}

	require.NoError(t, guard.writeOrHold(makeDelta("abcab"), "response.output_text.delta", write))
	require.NoError(t, guard.writeOrHold(makeDelta("ca"), "response.output_text.delta", write))
	require.ErrorIs(t, guard.writeOrHold(makeDelta("bx"), "response.output_text.delta", write), errDeepSeekWSSensitiveDelta)
	require.Empty(t, writes)
}

func TestDeepSeekWSSensitiveDeltaGuardReleasesCompletedStream(t *testing.T) {
	guard := &deepSeekWSSensitiveDeltaGuard{secrets: []string{"sk-secret"}}
	makeEvent := func(eventType, itemID, delta string) []byte {
		body, err := json.Marshal(map[string]any{"type": eventType, "item_id": itemID, "delta": delta})
		require.NoError(t, err)
		return body
	}
	var writes [][]byte
	write := func(message []byte) error {
		writes = append(writes, append([]byte(nil), message...))
		return nil
	}

	require.NoError(t, guard.writeOrHold(makeEvent("response.output_text.delta", "msg_a", "s"), "response.output_text.delta", write))
	require.NoError(t, guard.writeOrHold(makeEvent("response.output_text.done", "msg_a", ""), "response.output_text.done", write))
	require.Len(t, writes, 2, "a completed output cannot keep an unresolved prefix alive")
	for index := 0; index < deepSeekWSSensitiveHoldMaxEvents+10; index++ {
		require.NoError(t, guard.writeOrHold(makeEvent("response.output_text.delta", "msg_b", "normal output"), "response.output_text.delta", write))
	}
	require.Len(t, writes, deepSeekWSSensitiveHoldMaxEvents+12)
}

func TestForwardDeepSeekResponsesWebSocketTurnFlushesBenignHeldDelta(t *testing.T) {
	ctx := context.WithValue(context.Background(), ctxkey.UserID, int64(42))
	sse := strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp_benign_delta"}}`,
		"",
		`data: {"type":"response.output_text.delta","item_id":"msg_benign","output_index":0,"content_index":0,"delta":"this"}`,
		"",
		`data: {"type":"response.output_text.delta","item_id":"msg_benign","output_index":0,"content_index":0,"delta":" is safe"}`,
		"",
		`data: {"type":"response.completed","response":{"id":"resp_benign_delta","status":"completed","output":[],"usage":{"input_tokens":3,"output_tokens":2}}}`,
		"",
	}, "\n")
	upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(sse))}}
	svc := &OpenAIGatewayService{cfg: deepSeekWSTestConfig(), httpUpstream: upstream}
	var writes [][]byte
	result, err := svc.ForwardDeepSeekResponsesWebSocketTurn(ctx, newDeepSeekWSTestGinContext(ctx), deepSeekWSTestAccount(), "sk-deepseek-test", []byte(`{"model":"deepseek-v4","input":"hello"}`), 1, func(event []byte) error {
		writes = append(writes, append([]byte(nil), event...))
		return nil
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, writes, 4)
	require.Equal(t, "this", gjson.GetBytes(writes[1], "delta").String())
	require.Equal(t, " is safe", gjson.GetBytes(writes[2], "delta").String())
	require.Equal(t, "response.completed", gjson.GetBytes(writes[3], "type").String())
}

func TestForwardDeepSeekResponsesWebSocketTurnRedactsCredentialCanaries(t *testing.T) {
	ctx := context.WithValue(context.Background(), ctxkey.UserID, int64(73))
	cfg := deepSeekWSTestConfig()
	account := deepSeekWSTestAccount()
	derivedID, err := DeriveDeepSeekAuthenticatedUserID(ctx, cfg)
	require.NoError(t, err)
	apiKey := account.GetDeepSeekAPIKey()
	created, err := json.Marshal(map[string]any{
		"type": "response.created",
		"response": map[string]any{
			"id": "resp_canary",
		},
		"upstream_echo": apiKey + ":" + derivedID,
	})
	require.NoError(t, err)
	completed, err := json.Marshal(map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"id":     "resp_canary",
			"status": "completed",
			"output": []any{map[string]any{
				"type": "message",
				"role": "assistant",
				"content": []any{map[string]any{
					"type": "output_text",
					"text": "echo " + apiKey + " " + derivedID,
				}},
			}},
			"usage": map[string]any{"input_tokens": 2, "output_tokens": 1},
		},
	})
	require.NoError(t, err)
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": []string{"text/event-stream"},
			"X-Canary":     []string{"prefix " + apiKey + " " + derivedID + " suffix"},
		},
		Body: io.NopCloser(strings.NewReader("data: " + string(created) + "\n\n" + "data: " + string(completed) + "\n\n")),
	}}
	svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: upstream}
	var writes [][]byte
	result, err := svc.ForwardDeepSeekResponsesWebSocketTurn(ctx, newDeepSeekWSTestGinContext(ctx), account, apiKey, []byte(`{"model":"deepseek-v4","input":"hello"}`), 1, func(event []byte) error {
		writes = append(writes, append([]byte(nil), event...))
		return nil
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, writes, 2)
	for _, frame := range writes {
		require.NotContains(t, string(frame), apiKey)
		require.NotContains(t, string(frame), derivedID)
	}
	require.NotContains(t, result.ResponseHeaders.Get("X-Canary"), apiKey)
	require.NotContains(t, result.ResponseHeaders.Get("X-Canary"), derivedID)
	require.Contains(t, result.ResponseHeaders.Get("X-Canary"), deepSeekCredentialRedaction)
}

func TestForwardDeepSeekResponsesWebSocketTurnRejectsMismatchedSSEEventTypeBeforeWrite(t *testing.T) {
	ctx := context.WithValue(context.Background(), ctxkey.UserID, int64(42))
	c := newDeepSeekWSTestGinContext(ctx)
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body: io.NopCloser(strings.NewReader(
			"event: response.created\n" +
				`data: {"type":"response.output_text.delta","delta":"mismatch"}` + "\n\n",
		)),
	}}
	svc := &OpenAIGatewayService{cfg: deepSeekWSTestConfig(), httpUpstream: upstream}
	writes := 0
	result, err := svc.ForwardDeepSeekResponsesWebSocketTurn(ctx, c, deepSeekWSTestAccount(), "sk-deepseek-test", []byte(`{"model":"deepseek-v4","input":"hello"}`), 1, func([]byte) error {
		writes++
		return nil
	})
	require.Nil(t, result)
	require.Zero(t, writes)
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
}

func TestForwardDeepSeekResponsesWebSocketCompactionReusesNativeCore(t *testing.T) {
	ctx := context.WithValue(context.Background(), ctxkey.UserID, int64(51))
	c := newDeepSeekWSTestGinContext(ctx)
	MarkDeepSeekCompaction(c, DeepSeekCompactionModeRemoteV2SSE)
	completed := `{"type":"response.completed","response":{"id":"resp_summary","status":"completed","output":[{"id":"msg_summary","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"checkpoint summary"}]}],"usage":{"input_tokens":21,"output_tokens":5,"input_tokens_details":{"cached_tokens":4},"output_tokens_details":{"reasoning_tokens":2}}}}`
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid_summary"}},
		Body:       io.NopCloser(strings.NewReader("data: " + completed + "\n\n")),
	}}
	svc := &OpenAIGatewayService{cfg: deepSeekWSTestConfig(), httpUpstream: upstream}
	payload := []byte(`{"model":"deepseek-v4","reasoning":{"effort":"max"},"stream":true,"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"large history"}]},{"type":"compaction_trigger"}]}`)
	var writes [][]byte
	result, err := svc.ForwardDeepSeekResponsesWebSocketTurn(ctx, c, deepSeekWSTestAccount(), "sk-deepseek-test", payload, 1, func(event []byte) error {
		writes = append(writes, append([]byte(nil), event...))
		return nil
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "http://deepseek.example/responses", upstream.lastReq.URL.String())
	require.Equal(t, "1", upstream.lastReq.Header.Get("X-DeepSeek-Harness-Compact"))
	require.False(t, HasCompactionTriggerInInput(upstream.lastBody))
	compactInput := gjson.GetBytes(upstream.lastBody, "input").Array()
	require.NotEmpty(t, compactInput)
	require.Equal(t, deepSeekCompactInstruction, compactInput[len(compactInput)-1].Get("content.0.text").String())
	require.Equal(t, "max", gjson.GetBytes(upstream.lastBody, "reasoning.effort").String())
	require.True(t, strings.HasPrefix(gjson.GetBytes(upstream.lastBody, "user").String(), "dsu_v1_"))
	require.Len(t, writes, 2)
	require.Equal(t, "response.output_item.done", gjson.GetBytes(writes[0], "type").String())
	require.Equal(t, "compaction", gjson.GetBytes(writes[0], "item.type").String())
	require.NotEmpty(t, gjson.GetBytes(writes[0], "item.encrypted_content").String())
	require.Equal(t, "response.completed", gjson.GetBytes(writes[1], "type").String())
	require.Equal(t, 21, result.Usage.InputTokens)
	require.Equal(t, 5, result.Usage.OutputTokens)
	require.Equal(t, deepSeekResponsesEndpoint, result.UpstreamEndpoint)
	require.Equal(t, UsageRequestKindCompact, result.RequestKind)
	require.True(t, result.deepSeekWSCompaction)
	require.Len(t, result.wsReplayInput, 1)
}

func TestForwardDeepSeekResponsesWebSocketCompactionCyberSkipsFailover(t *testing.T) {
	ctx := context.WithValue(context.Background(), ctxkey.UserID, int64(51))
	c := newDeepSeekWSTestGinContext(ctx)
	MarkDeepSeekCompaction(c, DeepSeekCompactionModeRemoteV2SSE)
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusForbidden,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":{"code":"cyber_policy","message":"blocked"}}`)),
	}}
	svc := &OpenAIGatewayService{cfg: deepSeekWSTestConfig(), httpUpstream: upstream}
	payload := []byte(`{"model":"deepseek-v4","reasoning":{"effort":"max"},"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"large history"}]},{"type":"compaction_trigger"}]}`)
	writes := 0

	result, err := svc.ForwardDeepSeekResponsesWebSocketTurn(ctx, c, deepSeekWSTestAccount(), "sk-deepseek-test", payload, 2, func([]byte) error {
		writes++
		return nil
	})
	require.Error(t, err)
	require.True(t, IsDeepSeekWSAccountNeutralError(err))
	var closeErr *OpenAIWSClientCloseError
	require.ErrorAs(t, err, &closeErr)
	require.Equal(t, coderws.StatusPolicyViolation, closeErr.StatusCode())
	var failoverErr *UpstreamFailoverError
	require.False(t, errors.As(err, &failoverErr), "cyber policy must bypass compact failover even for 403")
	require.NotNil(t, result)
	require.Equal(t, UsageRequestKindCompact, result.RequestKind)
	require.Contains(t, result.RequestID, "deepseek-ws:cyber:turn:2:")
	require.Equal(t, "1", upstream.lastReq.Header.Get("X-DeepSeek-Harness-Compact"))
	require.Equal(t, 1, writes)
	mark := GetOpsCyberPolicy(c)
	require.NotNil(t, mark)
	require.Equal(t, http.StatusForbidden, mark.UpstreamStatus)
}

func TestProxyDeepSeekResponsesWebSocketReplaysFullToolContextAndResetsIndependentTurns(t *testing.T) {
	svc := &OpenAIGatewayService{cfg: deepSeekWSTestConfig()}
	serverErr := make(chan error, 1)
	var mu sync.Mutex
	var executed [][]byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, nil)
		if err != nil {
			serverErr <- err
			return
		}
		defer func() { _ = conn.CloseNow() }()
		_, first, err := conn.Read(r.Context())
		if err != nil {
			serverErr <- err
			return
		}
		c := newDeepSeekWSTestGinContext(r.Context())
		hooks := &DeepSeekWSIngressHooks{ExecuteTurn: func(_ context.Context, turn int, payload []byte, write func([]byte) error) (*OpenAIForwardResult, error) {
			mu.Lock()
			executed = append(executed, append([]byte(nil), payload...))
			mu.Unlock()
			responseID := fmt.Sprintf("resp_turn_%d", turn)
			var item string
			if turn == 1 {
				item = `{"type":"function_call","id":"fc_1","call_id":"call_1","name":"shell","arguments":"{}","unknown":{"kept":true}}`
			} else {
				item = `{"type":"message","id":"msg_2","role":"assistant","content":[{"type":"output_text","text":"done"}]}`
			}
			if err := write([]byte(`{"type":"response.output_item.done","output_index":0,"item":` + item + `}`)); err != nil {
				return nil, err
			}
			terminal := []byte(`{"type":"response.completed","response":{"id":"` + responseID + `","status":"completed","output":[` + item + `],"usage":{"input_tokens":3,"output_tokens":1}}}`)
			if err := write(terminal); err != nil {
				return nil, err
			}
			return &OpenAIForwardResult{ResponseID: responseID, UpstreamTerminalEvent: "response.completed", Usage: OpenAIUsage{InputTokens: 3, OutputTokens: 1}, wsReplayInput: []json.RawMessage{json.RawMessage(item)}, wsReplayInputExists: true}, nil
		}}
		serverErr <- svc.ProxyDeepSeekResponsesWebSocket(r.Context(), c, conn, first, hooks)
	}))
	defer server.Close()

	client, _, err := coderws.Dial(context.Background(), "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	require.NoError(t, err)
	writeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	require.NoError(t, client.Write(writeCtx, coderws.MessageText, []byte(`{"type":"response.create","model":"deepseek-v4","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"run"}]}]}`)))
	cancel()
	readDeepSeekWSTestEvent(t, client)
	readDeepSeekWSTestEvent(t, client)
	writeCtx, cancel = context.WithTimeout(context.Background(), 2*time.Second)
	require.NoError(t, client.Write(writeCtx, coderws.MessageText, []byte(`{"type":"response.create","model":"deepseek-v4","previous_response_id":"resp_turn_1","input":[{"type":"function_call_output","call_id":"call_1","output":"ok"}]}`)))
	cancel()
	readDeepSeekWSTestEvent(t, client)
	readDeepSeekWSTestEvent(t, client)
	writeCtx, cancel = context.WithTimeout(context.Background(), 2*time.Second)
	require.NoError(t, client.Write(writeCtx, coderws.MessageText, []byte(`{"type":"response.create","model":"deepseek-v4","input":"fresh context"}`)))
	cancel()
	readDeepSeekWSTestEvent(t, client)
	readDeepSeekWSTestEvent(t, client)
	_ = client.CloseNow()
	select {
	case err := <-serverErr:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("DeepSeek WebSocket proxy did not stop")
	}
	mu.Lock()
	defer mu.Unlock()
	require.Len(t, executed, 3)
	require.False(t, gjson.GetBytes(executed[1], "previous_response_id").Exists())
	require.True(t, gjson.GetBytes(executed[1], "stream").Bool())
	require.Equal(t, "function_call", gjson.GetBytes(executed[1], "input.1.type").String())
	require.Equal(t, "function_call_output", gjson.GetBytes(executed[1], "input.2.type").String())
	require.True(t, gjson.GetBytes(executed[1], "input.1.unknown.kept").Bool())
	require.Len(t, gjson.GetBytes(executed[2], "input").Array(), 1, "a turn without previous_response_id must start a fresh stateless context")
	require.Equal(t, "fresh context", gjson.GetBytes(executed[2], "input.0.content.0.text").String())
}

func TestProxyDeepSeekResponsesWebSocketCancelKeepsConnectionUsable(t *testing.T) {
	svc := &OpenAIGatewayService{cfg: deepSeekWSTestConfig()}
	serverErr := make(chan error, 1)
	var cancelled atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, nil)
		if err != nil {
			serverErr <- err
			return
		}
		defer func() { _ = conn.CloseNow() }()
		_, first, err := conn.Read(r.Context())
		if err != nil {
			serverErr <- err
			return
		}
		c := newDeepSeekWSTestGinContext(r.Context())
		hooks := &DeepSeekWSIngressHooks{ExecuteTurn: func(turnCtx context.Context, turn int, _ []byte, write func([]byte) error) (*OpenAIForwardResult, error) {
			if turn == 1 {
				if err := write([]byte(`{"type":"response.created","response":{"id":"resp_cancel_1"}}`)); err != nil {
					return nil, err
				}
				<-turnCtx.Done()
				cancelled.Store(true)
				return nil, context.Cause(turnCtx)
			}
			terminal := []byte(`{"type":"response.completed","response":{"id":"resp_after_cancel","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1}}}`)
			if err := write(terminal); err != nil {
				return nil, err
			}
			return &OpenAIForwardResult{ResponseID: "resp_after_cancel", UpstreamTerminalEvent: "response.completed", Usage: OpenAIUsage{InputTokens: 1, OutputTokens: 1}}, nil
		}}
		serverErr <- svc.ProxyDeepSeekResponsesWebSocket(r.Context(), c, conn, first, hooks)
	}))
	defer server.Close()
	client, _, err := coderws.Dial(context.Background(), "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	require.NoError(t, err)
	writeDeepSeekWSTestFrame(t, client, `{"type":"response.create","model":"deepseek-v4","input":"first"}`)
	require.Equal(t, "response.created", gjson.GetBytes(readDeepSeekWSTestEvent(t, client), "type").String())
	writeDeepSeekWSTestFrame(t, client, `{"type":"response.cancel","response_id":"resp_cancel_1"}`)
	require.Equal(t, "response.cancelled", gjson.GetBytes(readDeepSeekWSTestEvent(t, client), "type").String())
	require.Eventually(t, cancelled.Load, time.Second, 10*time.Millisecond)
	writeDeepSeekWSTestFrame(t, client, `{"type":"response.create","model":"deepseek-v4","input":"second"}`)
	require.Equal(t, "response.completed", gjson.GetBytes(readDeepSeekWSTestEvent(t, client), "type").String())
	_ = client.CloseNow()
	select {
	case err := <-serverErr:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("DeepSeek WebSocket proxy did not stop after cancel test")
	}
}

func readDeepSeekWSTestEvent(t *testing.T, conn *coderws.Conn) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	messageType, payload, err := conn.Read(ctx)
	require.NoError(t, err)
	require.Equal(t, coderws.MessageText, messageType)
	return payload
}

func writeDeepSeekWSTestFrame(t *testing.T, conn *coderws.Conn, payload string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, conn.Write(ctx, coderws.MessageText, []byte(payload)))
}

type deepSeekWSCancelUpstream struct {
	cancelled chan struct{}
	once      sync.Once
}

func (u *deepSeekWSCancelUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	<-req.Context().Done()
	u.once.Do(func() { close(u.cancelled) })
	return nil, req.Context().Err()
}

func (u *deepSeekWSCancelUpstream) DoWithTLS(req *http.Request, proxyURL string, accountID int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxyURL, accountID, concurrency)
}

func TestForwardDeepSeekResponsesWebSocketTurnCancellationReachesHTTPContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), ctxkey.UserID, int64(42)))
	upstream := &deepSeekWSCancelUpstream{cancelled: make(chan struct{})}
	svc := &OpenAIGatewayService{cfg: deepSeekWSTestConfig(), httpUpstream: upstream}
	c := newDeepSeekWSTestGinContext(ctx)
	done := make(chan error, 1)
	go func() {
		_, err := svc.ForwardDeepSeekResponsesWebSocketTurn(ctx, c, deepSeekWSTestAccount(), "sk-deepseek-test", []byte(`{"model":"deepseek-v4","input":"hello"}`), 1, func([]byte) error { return nil })
		done <- err
	}()
	cancel()
	select {
	case <-upstream.cancelled:
	case <-time.After(time.Second):
		t.Fatal("HTTP request context was not cancelled")
	}
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("cancelled turn did not return")
	}
}

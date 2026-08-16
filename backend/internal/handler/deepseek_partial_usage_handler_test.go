package handler

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/securityaudit"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type deepSeekPartialUsageHTTPUpstream struct {
	service.HTTPUpstream
	body       string
	statusCode int
	calls      int
	lastPath   string
	lastBody   []byte
}

type deepSeekCompactBlockingAuditEngine struct {
	scanText string
}

func deepSeekCompactCompletedResponsesSSE(t *testing.T, responseID, summary string, inputTokens, outputTokens int) string {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"id": responseID, "model": "deepseek-v4-flash", "status": "completed",
			"output": []any{map[string]any{
				"id": "msg_compact", "type": "message", "status": "completed", "role": "assistant",
				"content": []any{map[string]any{"type": "output_text", "text": summary}},
			}},
			"usage": map[string]any{
				"input_tokens": inputTokens, "output_tokens": outputTokens,
				"total_tokens":          inputTokens + outputTokens,
				"input_tokens_details":  map[string]any{"cached_tokens": 0},
				"output_tokens_details": map[string]any{"reasoning_tokens": 1},
			},
		},
	})
	require.NoError(t, err)
	return "event: response.completed\ndata: " + string(payload) + "\n\n"
}

func deepSeekCompactFailedResponsesSSE(t *testing.T, responseID string, inputTokens, outputTokens int) string {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"type": "response.failed",
		"response": map[string]any{
			"id": responseID, "model": "deepseek-v4-flash", "status": "failed", "output": []any{},
			"error": map[string]any{"code": "provider_error", "message": "provider failed"},
			"usage": map[string]any{
				"input_tokens": inputTokens, "output_tokens": outputTokens,
				"total_tokens": inputTokens + outputTokens,
			},
		},
	})
	require.NoError(t, err)
	return "event: response.failed\ndata: " + string(payload) + "\n\n"
}

func (e *deepSeekCompactBlockingAuditEngine) EffectiveMode() securityaudit.Mode {
	return securityaudit.ModeBlocking
}

func (e *deepSeekCompactBlockingAuditEngine) Enqueue(context.Context, securityaudit.Request) error {
	return nil
}

func (e *deepSeekCompactBlockingAuditEngine) Evaluate(_ context.Context, req securityaudit.Request) (*securityaudit.PromptDecision, error) {
	snapshot, err := securityaudit.ExtractBlockingPromptSnapshot(req, true)
	if err != nil {
		return nil, err
	}
	e.scanText = snapshot.ScanText
	if strings.Contains(snapshot.ScanText, "blocked compact tool output") {
		return &securityaudit.PromptDecision{
			Kind: securityaudit.DecisionBlock, ErrorCode: securityaudit.ErrorCodeBlocked, AllowNextStage: false,
		}, nil
	}
	return &securityaudit.PromptDecision{Kind: securityaudit.DecisionAllow, AllowNextStage: true}, nil
}

func (u *deepSeekPartialUsageHTTPUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	u.calls++
	if req != nil && req.URL != nil {
		u.lastPath = req.URL.Path
	}
	if req != nil && req.Body != nil {
		u.lastBody, _ = io.ReadAll(req.Body)
	}
	statusCode := u.statusCode
	if statusCode == 0 {
		statusCode = http.StatusOK
	}
	return &http.Response{
		StatusCode: statusCode,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(u.body)),
	}, nil
}

func newDeepSeekPartialUsageHandler(t *testing.T, upstreamBody string) (*OpenAIGatewayHandler, *openAIWSUsageHandlerUsageLogRepoStub, *service.APIKey, *deepSeekPartialUsageHTTPUpstream) {
	t.Helper()
	const groupID int64 = 7841
	account := service.Account{
		ID:          7842,
		Name:        "deepseek-partial-usage",
		Platform:    service.PlatformDeepSeek,
		Type:        service.AccountTypeAPIKey,
		Status:      service.StatusActive,
		Schedulable: true,
		GroupIDs:    []int64{groupID},
		Credentials: map[string]any{
			"api_key":  "sk-deepseek-partial",
			"base_url": "http://deepseek.partial.test",
		},
		Extra: map[string]any{
			service.DeepSeekUserIsolationModeKey: service.DeepSeekUserIsolationModeAuthenticatedUser,
		},
	}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Default.RateMultiplier = 1
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	cfg.Gateway.MaxAccountSwitches = 1
	cfg.JWT.Secret = "deepseek-handler-compact-test-jwt-secret"

	accountRepo := &openAIWSUsageHandlerAccountRepoStub{account: account}
	usageRepo := &openAIWSUsageHandlerUsageLogRepoStub{created: make(chan *service.UsageLog, 1)}
	billingCacheSvc := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingCacheSvc.Stop)
	upstream := &deepSeekPartialUsageHTTPUpstream{body: upstreamBody}
	gatewaySvc := service.NewOpenAIGatewayService(
		accountRepo,
		usageRepo,
		nil,
		nil,
		nil,
		nil,
		nil,
		cfg,
		nil,
		service.NewConcurrencyService(nil),
		service.NewBillingService(cfg, nil),
		nil,
		billingCacheSvc,
		upstream,
		&service.DeferredService{},
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
	)
	h := NewOpenAIGatewayHandler(
		gatewaySvc,
		service.NewConcurrencyService(nil),
		billingCacheSvc,
		service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg),
		nil,
		nil,
		nil,
		nil,
		cfg,
	)
	apiKey := &service.APIKey{
		ID:      7843,
		GroupID: func() *int64 { value := groupID; return &value }(),
		User:    &service.User{ID: 7844, Status: service.StatusActive},
		Group: &service.Group{
			ID:             groupID,
			Platform:       service.PlatformDeepSeek,
			Status:         service.StatusActive,
			RateMultiplier: 1,
		},
	}
	return h, usageRepo, apiKey, upstream
}

func deepSeekPartialUsageContext(path, body string, apiKey *service.APIKey) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), ctxkey.UserID, apiKey.User.ID))
	c.Set(string(middleware.ContextKeyAPIKey), apiKey)
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: apiKey.User.ID})
	return c, recorder
}

func TestDeepSeekUserIdentityAmbiguityFailsBeforeScheduling(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name      string
		composite bool
		path      string
		body      string
		invoke    func(*OpenAIGatewayHandler, *gin.Context)
	}{
		{
			name:   "direct chat duplicate user_id",
			path:   "/v1/chat/completions",
			body:   `{"model":"deepseek-v4-flash","messages":[{"role":"user","content":"hi"}],"user_id":"a","user_id":"b"}`,
			invoke: func(h *OpenAIGatewayHandler, c *gin.Context) { h.ChatCompletions(c) },
		},
		{
			name:      "composite responses noncanonical user",
			composite: true,
			path:      "/v1/responses",
			body:      `{"model":"deepseek-v4-pro","input":"hi","User":"spoofed"}`,
			invoke:    func(h *OpenAIGatewayHandler, c *gin.Context) { h.Responses(c) },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, usageRepo, apiKey, upstream := newDeepSeekPartialUsageHandler(t, "")
			if tt.composite {
				apiKey.Group.Platform = service.PlatformComposite
			}
			c, recorder := deepSeekPartialUsageContext(tt.path, tt.body, apiKey)
			tt.invoke(h, c)

			require.Equal(t, http.StatusBadRequest, recorder.Code)
			require.Zero(t, upstream.calls)
			select {
			case <-usageRepo.created:
				t.Fatal("invalid identity request must not create usage")
			default:
			}
		})
	}
}

func TestOpenAIChatCompletionsDeepSeekPartialStreamRecordsObservedUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstreamBody := "data: {\"id\":\"chat_partial\",\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n" +
		"data: {\"id\":\"chat_partial\",\"choices\":[],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":1,\"total_tokens\":4}}\n\n"
	h, usageRepo, apiKey, _ := newDeepSeekPartialUsageHandler(t, upstreamBody)
	c, recorder := deepSeekPartialUsageContext(
		"/v1/chat/completions",
		`{"model":"deepseek-v4-flash","messages":[{"role":"user","content":"hello"}],"reasoning_effort":"max","stream":true}`,
		apiKey,
	)

	h.ChatCompletions(c)

	select {
	case log := <-usageRepo.created:
		require.Equal(t, 3, log.InputTokens)
		require.Equal(t, 1, log.OutputTokens)
		require.True(t, log.Stream)
		require.NotNil(t, log.ReasoningEffort)
		require.Equal(t, "max", *log.ReasoningEffort)
	default:
		t.Fatal("expected partial DeepSeek Chat usage to be recorded")
	}
	require.Contains(t, recorder.Body.String(), "chat_partial")
	require.NotContains(t, recorder.Body.String(), `"error":{"type"`)
}

func TestOpenAIResponsesDeepSeekPartialStreamRecordsObservedUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstreamBody := "event: response.completed\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_partial\",\"status\":\"completed\",\"usage\":{\"input_tokens\":5,\"output_tokens\":2}}}\n"
	h, usageRepo, apiKey, _ := newDeepSeekPartialUsageHandler(t, upstreamBody)
	c, recorder := deepSeekPartialUsageContext(
		"/v1/responses",
		`{"model":"deepseek-v4-pro","input":"hello","reasoning":{"effort":"max"},"stream":true}`,
		apiKey,
	)

	h.Responses(c)

	select {
	case log := <-usageRepo.created:
		require.Equal(t, 5, log.InputTokens)
		require.Equal(t, 2, log.OutputTokens)
		require.True(t, log.Stream)
		require.NotNil(t, log.ReasoningEffort)
		require.Equal(t, "max", *log.ReasoningEffort)
	default:
		t.Fatal("expected partial DeepSeek Responses usage to be recorded")
	}
	require.Contains(t, recorder.Body.String(), "resp_partial")
	require.NotContains(t, recorder.Body.String(), `"error":{"type"`)
}

func TestOpenAIResponsesDeepSeekRemoteCompactionUsesNativeResponsesAndRecordsUsageOnce(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstreamBody := deepSeekCompactCompletedResponsesSSE(t, "resp_compact", "continue implementation", 31, 7)
	h, usageRepo, apiKey, upstream := newDeepSeekPartialUsageHandler(t, upstreamBody)
	requestBody, err := json.Marshal(map[string]any{
		"model": "deepseek-v4-flash", "stream": true, "reasoning": map[string]any{"effort": "max"},
		"input": []any{
			map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{
				"type": "input_text", "text": strings.Repeat("important engineering context ", 100),
			}}},
			map[string]any{"type": "compaction_trigger"},
		},
	})
	require.NoError(t, err)
	c, recorder := deepSeekPartialUsageContext("/v1/responses", string(requestBody), apiKey)
	c.Request.Header.Set("Accept", "text/event-stream")
	c.Request.Header.Set("X-Codex-Beta-Features", "remote_compaction_v2")

	h.Responses(c)

	select {
	case log := <-usageRepo.created:
		require.Equal(t, 31, log.InputTokens)
		require.Equal(t, 7, log.OutputTokens)
		require.True(t, log.Stream)
		require.NotNil(t, log.ReasoningEffort)
		require.Equal(t, "max", *log.ReasoningEffort)
	default:
		t.Fatal("expected DeepSeek compact usage to be recorded")
	}
	require.Equal(t, 1, upstream.calls)
	require.Equal(t, "/responses", upstream.lastPath)
	require.True(t, gjson.GetBytes(upstream.lastBody, "stream").Bool())
	require.Equal(t, "max", gjson.GetBytes(upstream.lastBody, "reasoning.effort").String())
	require.False(t, gjson.GetBytes(upstream.lastBody, "reasoning_effort").Exists())
	require.False(t, gjson.GetBytes(upstream.lastBody, "thinking").Exists())
	require.True(t, strings.HasPrefix(gjson.GetBytes(upstream.lastBody, "user").String(), "dsu_v1_"))
	require.False(t, strings.Contains(string(upstream.lastBody), "compaction_trigger"))
	upstreamInput := gjson.GetBytes(upstream.lastBody, "input").Array()
	require.NotEmpty(t, upstreamInput)
	require.Contains(t, upstreamInput[len(upstreamInput)-1].Get("content.0.text").String(), "CONTEXT CHECKPOINT COMPACTION")
	require.Equal(t, "text/event-stream", recorder.Header().Get("Content-Type"))
	// The same sole item appears once in output_item.done and once inside the
	// completed response object; there must be no second semantic item.
	require.Equal(t, 2, strings.Count(recorder.Body.String(), `"type":"compaction"`))
	require.Equal(t, 1, strings.Count(recorder.Body.String(), "event: response.output_item.done"))
	require.Contains(t, recorder.Body.String(), "event: response.completed")
	require.NotContains(t, recorder.Body.String(), "private")
	require.NotContains(t, recorder.Body.String(), "continue implementation")
}

func TestOpenAIResponsesDeepSeekLegacyCodexCompactionWires(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstreamBody := deepSeekCompactCompletedResponsesSSE(t, "resp_compact_legacy", "continue legacy task", 31, 7)
	longContext := strings.Repeat("important legacy Codex context ", 100)
	longContextJSON, err := json.Marshal(longContext)
	require.NoError(t, err)
	tests := []struct {
		name       string
		path       string
		body       string
		wantStream bool
		wantEffort string
	}{
		{
			name:       "body_signal_stream_without_beta_header",
			path:       "/v1/responses",
			body:       `{"model":"deepseek-v4-flash","stream":true,"reasoning":{"effort":"high"},"input":[{"type":"message","role":"user","content":"` + longContext + `"},{"type":"compaction_trigger"}]}`,
			wantStream: true,
			wantEffort: "high",
		},
		{
			name: "body_signal_unary",
			path: "/responses",
			body: `{"model":"deepseek-v4-flash","stream":false,"input":[{"type":"message","role":"user","content":"` + longContext + `"},{"type":"compaction_trigger"}]}`,
		},
		{
			name: "standalone_compact_unary_without_trigger",
			path: "/v1/responses/compact",
			body: `{"model":"deepseek-v4-flash","stream":true,"store":true,"prompt_cache_key":"legacy",` +
				`"input":[{"type":"message","role":"user","content":"` + longContext + `"}]}`,
		},
		{
			name: "standalone_compact_unary_string_input",
			path: "/responses/compact",
			body: `{"model":"deepseek-v4-flash","input":` + string(longContextJSON) + `}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, usageRepo, apiKey, upstream := newDeepSeekPartialUsageHandler(t, upstreamBody)
			c, recorder := deepSeekPartialUsageContext(tt.path, tt.body, apiKey)
			if tt.wantStream {
				c.Request.Header.Set("Accept", "text/event-stream")
			}

			h.Responses(c)

			select {
			case log := <-usageRepo.created:
				require.Equal(t, 31, log.InputTokens)
				require.Equal(t, 7, log.OutputTokens)
				require.Equal(t, tt.wantStream, log.Stream)
				if tt.wantEffort == "" {
					require.Nil(t, log.ReasoningEffort)
				} else {
					require.NotNil(t, log.ReasoningEffort)
					require.Equal(t, tt.wantEffort, *log.ReasoningEffort)
				}
			default:
				t.Fatal("expected legacy DeepSeek compact usage to be recorded")
			}
			require.Equal(t, 1, upstream.calls)
			require.Equal(t, "/responses", upstream.lastPath)
			require.Equal(t, tt.wantEffort, gjson.GetBytes(upstream.lastBody, "reasoning.effort").String())
			require.False(t, gjson.GetBytes(upstream.lastBody, "reasoning_effort").Exists())
			if tt.wantStream {
				require.Equal(t, "text/event-stream", recorder.Header().Get("Content-Type"))
				require.Equal(t, 1, strings.Count(recorder.Body.String(), "event: response.output_item.done"))
				require.Equal(t, 1, strings.Count(recorder.Body.String(), "event: response.completed"))
			} else {
				require.Equal(t, "application/json", recorder.Header().Get("Content-Type"))
				require.Equal(t, "response", gjson.GetBytes(recorder.Body.Bytes(), "object").String())
				require.Len(t, gjson.GetBytes(recorder.Body.Bytes(), "output").Array(), 1)
				require.Equal(t, "compaction", gjson.GetBytes(recorder.Body.Bytes(), "output.0.type").String())
				require.NotContains(t, recorder.Body.String(), "event: response.completed")
			}
		})
	}
}

func TestOpenAIResponsesDeepSeekRemoteCompactionInvalidSummaryBillsOnceWithoutRetry(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstreamBody := deepSeekCompactCompletedResponsesSSE(t, "resp_compact_empty", "   ", 11, 2)
	h, usageRepo, apiKey, upstream := newDeepSeekPartialUsageHandler(t, upstreamBody)
	requestBody := `{"model":"deepseek-v4-flash","stream":true,"reasoning":{"effort":"max"},"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"` + strings.Repeat("context ", 100) + `"}]},{"type":"compaction_trigger"}]}`
	c, recorder := deepSeekPartialUsageContext("/v1/responses", requestBody, apiKey)
	c.Request.Header.Set("Accept", "text/event-stream")
	c.Request.Header.Set("X-Codex-Beta-Features", "remote_compaction_v2")

	h.Responses(c)

	select {
	case log := <-usageRepo.created:
		require.Equal(t, 11, log.InputTokens)
		require.Equal(t, 2, log.OutputTokens)
		require.NotNil(t, log.ReasoningEffort)
		require.Equal(t, "max", *log.ReasoningEffort)
	default:
		t.Fatal("expected rejected DeepSeek compact summary usage to be recorded")
	}
	require.Equal(t, 1, upstream.calls)
	require.NotContains(t, recorder.Body.String(), `"type":"compaction"`)
	require.NotContains(t, recorder.Body.String(), "response.completed")
	require.Contains(t, recorder.Body.String(), "Upstream request failed")
}

func TestOpenAIResponsesDeepSeekRemoteCompactionUpstreamErrorBillsOnceAndFailsStream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstreamBody := deepSeekCompactFailedResponsesSSE(t, "resp_compact_failed", 13, 2)
	h, usageRepo, apiKey, upstream := newDeepSeekPartialUsageHandler(t, upstreamBody)
	requestBody := `{"model":"deepseek-v4-flash","stream":true,"reasoning":{"effort":"max"},"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"` + strings.Repeat("context ", 100) + `"}]},{"type":"compaction_trigger"}]}`
	c, recorder := deepSeekPartialUsageContext("/v1/responses", requestBody, apiKey)
	c.Request.Header.Set("Accept", "text/event-stream")
	c.Request.Header.Set("X-Codex-Beta-Features", "remote_compaction_v2")

	h.Responses(c)

	select {
	case log := <-usageRepo.created:
		require.Equal(t, 13, log.InputTokens)
		require.Equal(t, 2, log.OutputTokens)
		require.NotNil(t, log.ReasoningEffort)
		require.Equal(t, "max", *log.ReasoningEffort)
	default:
		t.Fatal("expected failed DeepSeek compact usage to be recorded")
	}
	require.Equal(t, 1, upstream.calls)
	require.Equal(t, http.StatusBadGateway, recorder.Code)
	require.Equal(t, "upstream_error", gjson.GetBytes(recorder.Body.Bytes(), "error.type").String())
	require.NotContains(t, recorder.Body.String(), `"type":"compaction"`)
	require.NotContains(t, recorder.Body.String(), "response.completed")
}

func TestOpenAIResponsesDeepSeekRemoteCompactionRejectsAmbiguousContentBeforeAudit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, usageRepo, apiKey, upstream := newDeepSeekPartialUsageHandler(t, "unused")
	requestBody := `{"model":"deepseek-v4-flash","stream":true,"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"benign","text":"malicious"}]},{"type":"compaction_trigger"}]}`
	c, recorder := deepSeekPartialUsageContext("/v1/responses", requestBody, apiKey)
	c.Request.Header.Set("Accept", "text/event-stream")
	c.Request.Header.Set("X-Codex-Beta-Features", "remote_compaction_v2")

	h.Responses(c)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Equal(t, "invalid_request_error", gjson.GetBytes(recorder.Body.Bytes(), "error.type").String())
	require.Equal(t, 0, upstream.calls)
	select {
	case <-usageRepo.created:
		t.Fatal("ambiguous compact input must not create usage")
	default:
	}
}

func TestOpenAIResponsesDeepSeekRemoteCompactionAuditsToolOutputBeforeUpstream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, usageRepo, apiKey, upstream := newDeepSeekPartialUsageHandler(t, "unused")
	engine := &deepSeekCompactBlockingAuditEngine{}
	h.securityAuditCoordinator = securityaudit.NewCoordinator(nil, engine)
	requestBody := `{"model":"deepseek-v4-flash","stream":true,"input":[` +
		`{"type":"message","role":"user","content":[{"type":"input_text","text":"benign request"}]},` +
		`{"type":"function_call","call_id":"call_1","name":"lookup","arguments":"{}"},` +
		`{"type":"function_call_output","call_id":"call_1","output":"blocked compact tool output"},` +
		`{"type":"compaction_trigger"}]}`
	c, recorder := deepSeekPartialUsageContext("/v1/responses", requestBody, apiKey)
	c.Request.Header.Set("Accept", "text/event-stream")
	c.Request.Header.Set("X-Codex-Beta-Features", "remote_compaction_v2")

	h.Responses(c)

	require.Equal(t, http.StatusForbidden, recorder.Code)
	require.Contains(t, recorder.Body.String(), securityaudit.ErrorCodeBlocked)
	require.Contains(t, engine.scanText, "blocked compact tool output")
	require.Equal(t, 0, upstream.calls)
	select {
	case <-usageRepo.created:
		t.Fatal("blocked compact tool output must not create usage")
	default:
	}
}

func TestOpenAIResponsesDeepSeekLegacyCompactAuditsToolOutputBeforeUpstream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, usageRepo, apiKey, upstream := newDeepSeekPartialUsageHandler(t, "unused")
	engine := &deepSeekCompactBlockingAuditEngine{}
	h.securityAuditCoordinator = securityaudit.NewCoordinator(nil, engine)
	requestBody := `{"model":"deepseek-v4-flash","input":[` +
		`{"type":"message","role":"user","content":[{"type":"input_text","text":"benign request"}]},` +
		`{"type":"function_call","call_id":"call_1","name":"lookup","arguments":"{}"},` +
		`{"type":"function_call_output","call_id":"call_1","output":"blocked compact tool output"}]}`
	c, recorder := deepSeekPartialUsageContext("/responses/compact", requestBody, apiKey)

	h.Responses(c)

	require.Equal(t, http.StatusForbidden, recorder.Code)
	require.Contains(t, recorder.Body.String(), securityaudit.ErrorCodeBlocked)
	require.Contains(t, engine.scanText, "blocked compact tool output")
	require.Equal(t, 0, upstream.calls)
	select {
	case <-usageRepo.created:
		t.Fatal("blocked legacy compact tool output must not create usage")
	default:
	}
}

func TestOpenAIResponsesDeepSeekRemoteCompactionRejectsClientInputBeforeScheduling(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, usageRepo, apiKey, upstream := newDeepSeekPartialUsageHandler(t, "unused")
	requestBody := `{"model":"deepseek-v4-flash","stream":true,"input":[{"type":"message","role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,aW1hZ2U="}]},{"type":"compaction_trigger"}]}`
	c, recorder := deepSeekPartialUsageContext("/v1/responses", requestBody, apiKey)
	c.Request.Header.Set("Accept", "text/event-stream")
	c.Request.Header.Set("X-Codex-Beta-Features", "remote_compaction_v2")

	h.Responses(c)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Contains(t, recorder.Body.String(), "does not support image content")
	require.Equal(t, 0, upstream.calls)
	select {
	case <-usageRepo.created:
		t.Fatal("client-side compact validation must not create usage")
	default:
	}
}

var _ service.HTTPUpstream = (*deepSeekPartialUsageHTTPUpstream)(nil)

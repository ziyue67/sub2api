package handler

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type deepSeekPartialUsageHTTPUpstream struct {
	service.HTTPUpstream
	body string
}

func (u *deepSeekPartialUsageHTTPUpstream) Do(*http.Request, string, int64, int) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(u.body)),
	}, nil
}

func newDeepSeekPartialUsageHandler(t *testing.T, upstreamBody string) (*OpenAIGatewayHandler, *openAIWSUsageHandlerUsageLogRepoStub, *service.APIKey) {
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
	}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Default.RateMultiplier = 1
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	cfg.Gateway.MaxAccountSwitches = 1

	accountRepo := &openAIWSUsageHandlerAccountRepoStub{account: account}
	usageRepo := &openAIWSUsageHandlerUsageLogRepoStub{created: make(chan *service.UsageLog, 1)}
	billingCacheSvc := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingCacheSvc.Stop)
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
		&deepSeekPartialUsageHTTPUpstream{body: upstreamBody},
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
	return h, usageRepo, apiKey
}

func deepSeekPartialUsageContext(path, body string, apiKey *service.APIKey) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(middleware.ContextKeyAPIKey), apiKey)
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: apiKey.User.ID})
	return c, recorder
}

func TestOpenAIChatCompletionsDeepSeekPartialStreamRecordsObservedUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstreamBody := "data: {\"id\":\"chat_partial\",\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n" +
		"data: {\"id\":\"chat_partial\",\"choices\":[],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":1,\"total_tokens\":4}}\n\n"
	h, usageRepo, apiKey := newDeepSeekPartialUsageHandler(t, upstreamBody)
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
	h, usageRepo, apiKey := newDeepSeekPartialUsageHandler(t, upstreamBody)
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

var _ service.HTTPUpstream = (*deepSeekPartialUsageHTTPUpstream)(nil)

package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/model"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type deepSeekFailoverRuleRepo struct {
	rules []*model.ErrorPassthroughRule
}

func (r *deepSeekFailoverRuleRepo) List(context.Context) ([]*model.ErrorPassthroughRule, error) {
	return r.rules, nil
}

func (r *deepSeekFailoverRuleRepo) GetByID(context.Context, int64) (*model.ErrorPassthroughRule, error) {
	return nil, nil
}

func (r *deepSeekFailoverRuleRepo) Create(_ context.Context, rule *model.ErrorPassthroughRule) (*model.ErrorPassthroughRule, error) {
	return rule, nil
}

func (r *deepSeekFailoverRuleRepo) Update(_ context.Context, rule *model.ErrorPassthroughRule) (*model.ErrorPassthroughRule, error) {
	return rule, nil
}

func (r *deepSeekFailoverRuleRepo) Delete(context.Context, int64) error {
	return nil
}

func TestOpenAIFailoverExhaustedUsesDeepSeekPlatformRuleAndVendorRequestID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	responseCode := http.StatusTeapot
	customMessage := "deepseek-specific upstream failure"
	ruleService := service.NewErrorPassthroughService(&deepSeekFailoverRuleRepo{rules: []*model.ErrorPassthroughRule{{
		ID:              1,
		Name:            "deepseek only",
		Enabled:         true,
		Priority:        1,
		ErrorCodes:      []int{http.StatusServiceUnavailable},
		MatchMode:       model.MatchModeAny,
		Platforms:       []string{service.PlatformDeepSeek},
		PassthroughCode: false,
		ResponseCode:    &responseCode,
		PassthroughBody: false,
		CustomMessage:   &customMessage,
	}}}, nil)
	h := &OpenAIGatewayHandler{errorPassthroughService: ruleService}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	h.handleFailoverExhausted(c, &service.UpstreamFailoverError{
		Platform:        service.PlatformDeepSeek,
		StatusCode:      http.StatusServiceUnavailable,
		ResponseBody:    []byte(`{"error":{"message":"provider unavailable"}}`),
		ResponseHeaders: http.Header{"X-Deepseek-Request-Id": []string{"ds-failover-rule-1"}},
	}, false)

	require.Equal(t, http.StatusTeapot, recorder.Code, "the DeepSeek-only passthrough rule must match the failed attempt platform")
	require.Equal(t, customMessage, gjson.Get(recorder.Body.String(), "error.message").String())
	require.Equal(t, "ds-failover-rule-1", recorder.Header().Get("X-Deepseek-Request-Id"))
}

func TestOpenAIFailoverExhaustedMapsDeepSeek402ToBillingError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	(&OpenAIGatewayHandler{}).handleFailoverExhausted(c, &service.UpstreamFailoverError{
		Platform:        service.PlatformDeepSeek,
		StatusCode:      http.StatusPaymentRequired,
		ResponseBody:    []byte(`{"error":{"message":"insufficient balance"}}`),
		ResponseHeaders: http.Header{"X-Deepseek-Request-Id": []string{"ds-payment-402"}},
	}, false)

	require.Equal(t, http.StatusPaymentRequired, recorder.Code)
	require.Equal(t, "billing_error", gjson.Get(recorder.Body.String(), "error.type").String())
	require.Contains(t, gjson.Get(recorder.Body.String(), "error.message").String(), "insufficient balance")
	require.Equal(t, "ds-payment-402", recorder.Header().Get("X-Deepseek-Request-Id"))
}

func TestMessagesFailoverExhaustedMapsDeepSeek402AndCopiesSafeHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	(&GatewayHandler{}).handleFailoverExhausted(c, &service.UpstreamFailoverError{
		Platform:     service.PlatformDeepSeek,
		StatusCode:   http.StatusPaymentRequired,
		ResponseBody: []byte(`{"error":{"message":"insufficient balance"}}`),
		ResponseHeaders: http.Header{
			"X-Deepseek-Request-Id": []string{"ds-messages-payment-402"},
			"Retry-After":           []string{"30"},
			"Authorization":         []string{"Bearer must-not-leak"},
		},
	}, service.PlatformDeepSeek, false)

	require.Equal(t, http.StatusPaymentRequired, recorder.Code)
	require.Equal(t, "billing_error", gjson.Get(recorder.Body.String(), "error.type").String())
	require.Equal(t, "ds-messages-payment-402", recorder.Header().Get("X-Deepseek-Request-Id"))
	require.Equal(t, "30", recorder.Header().Get("Retry-After"))
	require.Empty(t, recorder.Header().Get("Authorization"))
}

//go:build unit

package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCountOpenAIWebSearchCallsFromJSON_OnlyCompletedBuiltInCalls(t *testing.T) {
	body := []byte(`{
		"status":"completed",
		"output":[
			{"type":"web_search_call","id":"ws_1","status":"completed"},
			{"type":"web_search_call","id":"ws_2","status":"failed"},
			{"type":"function_call","id":"fn_1","name":"web_search","status":"completed"},
			{"type":"web_search_call","id":"ws_3"},
			{"type":"web_search_call","id":"ws_4","status":"in_progress"}
		]
	}`)
	require.Equal(t, 2, countOpenAIWebSearchCallsFromJSONBytes(body))
}

func TestCountOpenAIWebSearchCallsFromJSON_IncompleteResponseKeepsCompletedCalls(t *testing.T) {
	body := []byte(`{
		"status":"incomplete",
		"output":[
			{"type":"web_search_call","id":"ws_1","status":"completed"},
			{"type":"web_search_call","id":"ws_2","status":"incomplete"},
			{"type":"web_search_call","id":"ws_3"}
		]
	}`)
	require.Equal(t, 1, countOpenAIWebSearchCallsFromJSONBytes(body))
}

func TestCountOpenAIWebSearchCallsFromSSE_DeduplicatesDoneAndTerminal(t *testing.T) {
	body := "data: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":{\"type\":\"web_search_call\",\"id\":\"ws_1\",\"call_id\":\"call_1\",\"status\":\"completed\"}}\n\n" +
		"data: {\"type\":\"response.output_item.done\",\"output_index\":1,\"item\":{\"type\":\"web_search_call\",\"id\":\"ws_2\",\"call_id\":\"call_2\",\"status\":\"completed\"}}\n\n" +
		"data: {\"type\":\"response.output_item.done\",\"output_index\":2,\"item\":{\"type\":\"web_search_call\",\"id\":\"ws_3\",\"call_id\":\"call_3\",\"status\":\"completed\"}}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[{\"type\":\"web_search_call\",\"id\":\"ws_1\",\"call_id\":\"call_1\"},{\"type\":\"web_search_call\",\"id\":\"ws_2\",\"call_id\":\"call_2\"},{\"type\":\"web_search_call\",\"id\":\"ws_3\",\"call_id\":\"call_3\"}]}}\n\n"

	require.Equal(t, 3, countOpenAIWebSearchCallsFromSSEBody(body))
}

func TestCountOpenAIWebSearchCallsFromSSE_FailedTerminalKeepsCompletedCalls(t *testing.T) {
	body := "data: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":{\"type\":\"web_search_call\",\"id\":\"ws_1\",\"status\":\"completed\"}}\n\n" +
		"data: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\"}}\n\n"

	require.Equal(t, 1, countOpenAIWebSearchCallsFromSSEBody(body))
}

func TestHandleNonStreamingResponse_PropagatesOpenAIWebSearchCalls(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{
		"id":"resp_search",
		"status":"completed",
		"output":[
			{"type":"web_search_call","id":"ws_1","status":"completed"},
			{"type":"web_search_call","id":"ws_2","status":"completed"},
			{"type":"web_search_call","id":"ws_3","status":"completed"}
		],
		"usage":{"input_tokens":10,"output_tokens":2}
	}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(body)),
	}
	svc := &OpenAIGatewayService{cfg: &config.Config{}}

	result, err := svc.handleNonStreamingResponse(context.Background(), resp, c, &Account{Platform: PlatformOpenAI}, "gpt-5", "gpt-5")
	require.NoError(t, err)
	require.Equal(t, 3, result.webSearchCalls)
}

func TestCalculateOpenAIRecordUsageCost_WebSearchIsAdditiveToTokens(t *testing.T) {
	svc := &OpenAIGatewayService{billingService: newTestBillingService()}
	apiKey := &APIKey{Group: &Group{}}

	cost, err := svc.calculateOpenAIRecordUsageCost(
		context.Background(),
		&OpenAIForwardResult{WebSearchCalls: 3},
		apiKey,
		[]string{"claude-sonnet-4"},
		0.75,
		1,
		1,
		0.75,
		UsageTokens{InputTokens: 1000, OutputTokens: 500},
		"",
		boolPtr(false),
		time.Time{},
	)
	require.NoError(t, err)
	require.InDelta(t, 0.003, cost.InputCost, 1e-12)
	require.InDelta(t, 0.0075, cost.OutputCost, 1e-12)
	require.InDelta(t, 0.0405, cost.TotalCost, 1e-12)
	require.InDelta(t, 0.030375, cost.ActualCost, 1e-12)
}

func TestCalculateOpenAIRecordUsageCost_WebSearchIsAdditiveToImage(t *testing.T) {
	imagePrice := 0.04
	svc := &OpenAIGatewayService{billingService: newTestBillingService()}
	apiKey := &APIKey{Group: &Group{ImagePrice2K: &imagePrice}}

	cost, err := svc.calculateOpenAIRecordUsageCost(
		context.Background(),
		&OpenAIForwardResult{WebSearchCalls: 3, ImageCount: 1, ImageSize: "2K"},
		apiKey,
		[]string{"gpt-image-1"},
		1,
		0.5,
		1,
		0.75,
		UsageTokens{},
		"",
		boolPtr(false),
		time.Time{},
	)
	require.NoError(t, err)
	require.Equal(t, string(BillingModeImage), cost.BillingMode)
	require.InDelta(t, 0.07, cost.TotalCost, 1e-12)
	require.InDelta(t, 0.0425, cost.ActualCost, 1e-12)
}

func TestBuildOpenAIToolSurcharges_MatchesNewAPIFormula(t *testing.T) {
	svc := &OpenAIGatewayService{billingService: &BillingService{}}
	items := svc.buildOpenAIToolSurcharges(
		&OpenAIForwardResult{WebSearchCalls: 3},
		&APIKey{Group: &Group{}},
		0.75,
	)

	require.Equal(t, []ToolSurcharge{{
		Name: "web_search", Count: 3, Price: 10, RateMultiplier: 0.75, Cost: 0.0225, AccountCost: 0.03,
	}}, items)
}

func TestBuildOpenAIToolSurcharges_TracksAccountCostWhenUserPriceIsFree(t *testing.T) {
	zero := 0.0
	svc := &OpenAIGatewayService{billingService: &BillingService{}}
	items := svc.buildOpenAIToolSurcharges(
		&OpenAIForwardResult{WebSearchCalls: 2},
		&APIKey{Group: &Group{WebSearchPricePerCall: &zero}},
		0.75,
	)

	require.Equal(t, []ToolSurcharge{{
		Name: "web_search", Count: 2, Price: 0, RateMultiplier: 0.75, Cost: 0, AccountCost: 0.02,
	}}, items)
}

func TestBuildOpenAIToolSurcharges_TracksXAIUpstreamSearchCost(t *testing.T) {
	pricePer1K := 10.0
	svc := &OpenAIGatewayService{billingService: &BillingService{}}
	items := svc.buildOpenAIToolSurcharges(
		&OpenAIForwardResult{SearchCount: 3},
		&APIKey{Group: &Group{SearchPricePer1k: &pricePer1K}},
		0.75,
	)

	require.Equal(t, []ToolSurcharge{{
		Name: "web_search", Count: 3, Price: 10, RateMultiplier: 0.75, Cost: 0.0225, AccountCost: 0.015,
	}}, items)
}

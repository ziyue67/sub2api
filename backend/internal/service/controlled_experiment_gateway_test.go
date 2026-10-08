package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type controlledGatewayAccounts struct {
	AccountRepository
	account *Account
}

func (r controlledGatewayAccounts) GetByID(context.Context, int64) (*Account, error) {
	return r.account, nil
}

func controlledGatewayFixture(account *Account, gateway *OpenAIGatewayService, channel, model string) (*controlledExperimentGateway, ControlledRoute, ControlledExperimentSpec, []byte) {
	route := ControlledRoute{AccountID: account.ID, Channel: channel, MappedModel: model}
	spec := ControlledExperimentSpec{Model: model, ReasoningEffort: "high"}
	body, _ := controlledPayload(spec, ControlledTask{Prompt: "Return OK"}, nil)
	return &controlledExperimentGateway{accounts: controlledGatewayAccounts{account: account}, gateway: gateway, billing: NewBillingService(&config.Config{}, nil)}, route, spec, body
}

func controlledGatewayTerminal(model, effort string) string {
	return `{"type":"response.completed","response":{"id":"resp_fixture","status":"completed","model":"` + model + `","reasoning":{"effort":"` + effort + `"},"usage":{"input_tokens":100,"output_tokens":5,"input_tokens_details":{"cached_tokens":60}},"output":[{"type":"message","id":"msg_fixture","role":"assistant","content":[{"type":"output_text","text":"OK"}]}]}}`
}

func TestControlledExperimentGatewayNativeHTTPUsesForwardAndCacheAccounting(t *testing.T) {
	gin.SetMode(gin.TestMode)
	account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, Credentials: map[string]any{"api_key": "fixture-key", "base_url": "https://upstream.example"}}
	upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: " + controlledGatewayTerminal("gpt-5.4", "high") + "\n\n"))}}
	gateway := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream, cache: &stubGatewayCache{}}
	executor, route, spec, body := controlledGatewayFixture(account, gateway, "native_http", "gpt-5.4")
	turn, d := executor.Execute(context.Background(), route, spec, body, "fixture-session")
	require.Equal(t, "ok", d.Code, "%+v", d)
	require.Equal(t, "OK", turn.Text)
	require.Equal(t, 1, d.Submissions)
	require.Len(t, upstream.requests, 1)
	require.Equal(t, "native_http", d.ActualChannel)
	require.Equal(t, "upstream_declared", d.EffortEvidence)
	require.Equal(t, 60, d.Usage.CacheReadInputTokens)
	require.False(t, d.CostIncomplete)
	cost, err := executor.billing.CalculateCost("gpt-5.4", UsageTokens{InputTokens: 40, OutputTokens: 5, CacheReadTokens: 60}, 1)
	require.NoError(t, err)
	require.NotNil(t, d.CostUSD)
	require.Equal(t, cost.TotalCost, *d.CostUSD)
}

func TestControlledExperimentGatewayNativeHTTPDoesNotRetryCompatibilityErrors(t *testing.T) {
	account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Credentials: map[string]any{"access_token": "fixture-oauth"}}
	upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 400, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"code":"invalid_encrypted_content","message":"Invalid encrypted content"}}`))}}
	gateway := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream, cache: &stubGatewayCache{}}
	executor, route, spec, body := controlledGatewayFixture(account, gateway, "native_http", "gpt-5.4")
	_, d := executor.Execute(context.Background(), route, spec, body, "fixture-session")
	require.Equal(t, "upstream_rejected", d.Code)
	require.Equal(t, 400, d.UpstreamStatus)
	require.Equal(t, 1, d.Submissions)
	require.Len(t, upstream.requests, 1)
}

func TestControlledExperimentGatewayMissingTerminalRetainsActualAcceptedStatus(t *testing.T) {
	account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Credentials: map[string]any{"api_key": "fixture-key", "base_url": "https://upstream.example"}}
	upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_incomplete\"}}\n\n"))}}
	gateway := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream, cache: &stubGatewayCache{}}
	executor, route, spec, body := controlledGatewayFixture(account, gateway, "native_http", "gpt-5.4")
	_, d := executor.Execute(context.Background(), route, spec, body, "fixture-session")
	require.Equal(t, "transport_unknown", d.Code)
	require.Equal(t, 200, d.UpstreamStatus)
	require.Equal(t, 502, d.ForwardingStatus)
	require.Equal(t, 1, d.Submissions)
	require.Len(t, upstream.requests, 1)
	require.True(t, d.CostIncomplete)
}

func TestControlledExperimentGatewayFrozenProxyIdentityRejectsBeforeSend(t *testing.T) {
	account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Credentials: map[string]any{"api_key": "fixture-key", "base_url": "https://upstream.example"}, Proxy: &Proxy{Protocol: "http", Host: "127.0.0.1", Port: 7890}}
	upstream := &httpUpstreamRecorder{}
	gateway := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
	executor, route, spec, body := controlledGatewayFixture(account, gateway, "native_http", "gpt-5.4")
	identity, err := executor.identitySnapshot(context.Background(), account)
	require.NoError(t, err)
	route.identityReference = identity
	account.Proxy.Host = "changed.example"
	_, d := executor.Execute(context.Background(), route, spec, body, "fixture-session")
	require.Equal(t, "account_identity_changed_since_preflight", d.Code)
	require.Zero(t, d.Submissions)
	require.Empty(t, upstream.requests)
}

func TestControlledExperimentGatewayWSUsesOneFrameAndNoHTTPFallback(t *testing.T) {
	account := wsSSETestAccount()
	conn := &openAIWSCaptureConn{events: [][]byte{[]byte(controlledGatewayTerminal("gpt-5.4", "high"))}}
	dialer := &wsSSETestDialer{conn: conn}
	upstream := &httpUpstreamRecorder{}
	gateway := wsSSETestService(t, dialer, upstream)
	executor, route, spec, body := controlledGatewayFixture(account, gateway, "native_ws", "gpt-5.4")
	turn, d := executor.Execute(context.Background(), route, spec, body, "fixture-session")
	require.Equal(t, "ok", d.Code, "%+v", d)
	require.Equal(t, "OK", turn.Text)
	require.Equal(t, "native_ws", d.ActualChannel)
	require.Equal(t, 1, d.Submissions)
	require.Len(t, conn.writes, 1)
	require.Empty(t, upstream.requests)

	rejectedDialer := &wsSSETestDialer{status: 426, err: io.EOF}
	rejected := wsSSETestService(t, rejectedDialer, upstream)
	executor, route, spec, body = controlledGatewayFixture(account, rejected, "native_ws", "gpt-5.4")
	_, d = executor.Execute(context.Background(), route, spec, body, "another-session")
	require.NotEqual(t, "ok", d.Code)
	require.Equal(t, 0, d.Submissions)
	require.EqualValues(t, 1, rejectedDialer.calls.Load())
	require.Empty(t, upstream.requests)
}

func TestControlledExperimentGatewayPrismKeepsAdapterEvidenceAndUnavailableUsage(t *testing.T) {
	var submissions atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"data":[{"id":"gpt-6-luna"}]}`)
			return
		}
		submissions.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		require.Len(t, r.Header.Get("X-Prism-Caller-ID"), 64)
		require.Len(t, r.Header.Get("X-Prism-Turn-ID"), 64)
		_, _ = io.WriteString(w, "data: "+controlledGatewayTerminal("gpt-6-luna", "high")+"\n\n")
	}))
	defer server.Close()
	gateway, account := prismTestService(server.URL)
	account.Status = StatusActive
	account.Schedulable = true
	executor, route, spec, body := controlledGatewayFixture(account, gateway, "prism", "gpt-6-luna")
	turn, d := executor.Execute(context.Background(), route, spec, body, "9d8ad823-69d3-4c0d-a0b2-23e4f9509e21")
	require.Equal(t, "ok", d.Code, "%+v", d)
	require.Equal(t, "OK", turn.Text)
	require.EqualValues(t, 1, submissions.Load())
	require.Equal(t, 1, d.Submissions)
	require.Equal(t, "prism", d.ActualChannel)
	require.Equal(t, "adapter_declared", d.EffortEvidence)
	require.Equal(t, "unavailable", d.UsageSource)
	require.Nil(t, d.CostUSD)
	require.True(t, d.CostIncomplete)
}

func TestControlledExperimentGatewayRespectsProtocolSwitchesBeforeSend(t *testing.T) {
	for _, channel := range []string{"prism", "bps"} {
		t.Run(channel, func(t *testing.T) {
			flag := "openai_prism_browser"
			setting := SettingKeyPrismBrowserEnabled
			reason := "prism_not_enabled_for_account_model"
			model := "gpt-6-luna"
			if channel == "bps" {
				flag = "openai_excel_bps"
				setting = SettingKeyExcelBPSEnabled
				reason = "bps_not_enabled_for_account_model"
				model = "gpt-5.4"
			}
			repo := &excelBPSImageSettingsRepo{values: map[string]string{setting: "false"}}
			upstream := &httpUpstreamRecorder{}
			account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Credentials: map[string]any{"access_token": "fixture-oauth"}, Extra: map[string]any{flag: true}}
			gateway := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream, settingService: NewSettingService(repo, &config.Config{})}
			executor, route, spec, body := controlledGatewayFixture(account, gateway, channel, model)
			preflight := executor.Preflight(context.Background(), route, spec)
			require.False(t, preflight.Available)
			require.Equal(t, reason, preflight.Reason)
			_, diagnostic := executor.Execute(context.Background(), route, spec, body, "9d8ad823-69d3-4c0d-a0b2-23e4f9509e21")
			require.Equal(t, "local_forwarding_rejected", diagnostic.Code)
			require.Zero(t, diagnostic.Submissions)
			require.Empty(t, upstream.requests)
			require.Empty(t, gateway.controlledRouteReason(context.Background(), account, spec.Model, "native_http"))
			require.NoError(t, repo.SetMultiple(context.Background(), map[string]string{setting: "true"}))
			require.Empty(t, gateway.controlledRouteReason(context.Background(), account, spec.Model, channel))
		})
	}
}

func TestControlledExperimentGatewayBPSUsesOneSubmission(t *testing.T) {
	account := excelAccount()
	delete(account.Extra, "openai_passthrough")
	upstream := &httpUpstreamRecorder{resp: bpsCompletionResponse(200, "data: "+controlledGatewayTerminal("gpt-5.6-sol", "high")+"\n\n")}
	gateway := openAIClientToolsTestService(upstream)
	executor, route, spec, body := controlledGatewayFixture(account, gateway, "bps", "gpt-5.6-sol")
	turn, d := executor.Execute(context.Background(), route, spec, body, "fixture-session")
	require.Equal(t, "ok", d.Code, "%+v", d)
	require.Equal(t, "OK", turn.Text)
	require.Equal(t, "bps", d.ActualChannel)
	require.Equal(t, 1, d.Submissions)
	require.Len(t, upstream.requests, 1)
	require.Equal(t, "/basispoints/api/responses", upstream.lastReq.URL.Path)
	require.Equal(t, "adapter_declared", d.EffortEvidence)
}

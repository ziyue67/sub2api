package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Excel-only credentials must never reach a native upstream or lose account health.
// Every upstream response and credential is synthetic; no network is used.
func TestExcelOAuthRejectsUnselectedNativeModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, enabled := range []bool{true, false} {
		for _, manual := range []bool{false, true} {
			for _, passthrough := range []bool{false, true} {
				t.Run(fmt.Sprintf("bps=%t/manual=%t/passthrough=%t", enabled, manual, passthrough), func(t *testing.T) {
					upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 401, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"detail":"Unauthorized"}`))}}
					gateway := openAIClientToolsTestService(upstream)
					repo, _ := setupExcelBPSAuth(gateway)
					account := excelAccount()
					account.Credentials["client_id"] = openai.ExcelClientID
					account.Credentials["refresh_token"] = "synthetic-refresh"
					account.Extra["openai_excel_bps"] = enabled
					account.Extra["openai_excel_bps_models"] = []string{"gpt-5.6-sol"}
					account.Extra["openai_passthrough"] = passthrough
					require.False(t, account.IsExcelBPSEnabledForModel("gpt-6.1-sol"))
					c, _ := gin.CreateTestContext(httptest.NewRecorder())
					c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
					var err error
					if manual {
						svc := &AccountTestService{openaiGatewayService: gateway, accountRepo: repo, httpUpstream: upstream, cfg: gateway.cfg}
						err = svc.testOpenAIAccountConnection(c, account, "gpt-6.1-sol", "Reply OK", "")
					} else {
						_, err = gateway.Forward(context.Background(), c, account, []byte(`{"model":"gpt-6.1-sol","input":"Reply OK"}`))
					}
					require.ErrorContains(t, err, errExcelOAuthRouteUnavailable.Error())
					require.Empty(t, upstream.requests)
					require.Zero(t, repo.errorCalls)
					require.Zero(t, repo.tempCalls)
					require.Equal(t, StatusActive, account.Status)
					require.True(t, account.Schedulable)
				})
			}
		}
	}
}

func TestExcelOAuthSchedulingAndAliases(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		a := excelAccount()
		a.Credentials["client_id"] = openai.ExcelClientID
		a.Extra["openai_passthrough"] = passthrough
		a.Extra["openai_excel_bps_models"] = []string{"gpt-5.6-sol"}
		a.Credentials["model_mapping"] = map[string]any{"alias": "gpt-5.6-sol", "native": "gpt-6.1-sol"}
		require.True(t, a.IsModelSupported("alias"))
		require.False(t, a.IsModelSupported("native"))
		require.False(t, a.IsModelSupported("gpt-6.1-sol"))
		a.Extra["openai_excel_bps"] = false
		require.False(t, a.IsModelSupported("alias"))
		// Existing Codex grants retain the mixed-route contract.
		delete(a.Credentials, "client_id")
		require.True(t, a.IsModelSupported("native"))
	}
}

func TestExcelOAuthGlobalDisableBlocksBeforeSend(t *testing.T) {
	upstream := &httpUpstreamRecorder{}
	gateway := openAIClientToolsTestService(upstream)
	repo, _ := setupExcelBPSAuth(gateway)
	settings := &excelBPSImageSettingsRepo{values: map[string]string{SettingKeyExcelBPSEnabled: "false"}}
	gateway.settingService = NewSettingService(settings, &config.Config{})
	a := excelAccount()
	a.Credentials["client_id"] = openai.ExcelClientID
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	_, err := gateway.Forward(context.Background(), c, a, []byte(`{"model":"gpt-5.6-sol","input":"OK"}`))
	require.ErrorIs(t, err, errExcelOAuthRouteUnavailable)
	require.Equal(t, http.StatusBadRequest, c.Writer.Status())
	require.Empty(t, upstream.requests)
	require.Zero(t, repo.errorCalls)
	scheduler := &defaultOpenAIAccountScheduler{service: gateway}
	ok, reason := scheduler.isAccountRequestCompatibleReason(context.Background(), a, OpenAIAccountScheduleRequest{RequestedModel: "gpt-5.6-sol"})
	require.False(t, ok)
	require.Equal(t, "excel_bps_disabled", reason)
}

func TestExcelOAuthNativeEntryPointsReject(t *testing.T) {
	a := excelAccount()
	a.Credentials["client_id"] = openai.ExcelClientID
	gateway := openAIClientToolsTestService(&httpUpstreamRecorder{})
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	_, err := gateway.buildUpstreamRequest(context.Background(), c, a, []byte(`{}`), "synthetic", false, "", false)
	require.ErrorIs(t, err, errExcelOAuthRouteUnavailable)
	_, err = gateway.buildUpstreamRequestOpenAIPassthrough(context.Background(), c, a, []byte(`{}`), "synthetic")
	require.ErrorIs(t, err, errExcelOAuthRouteUnavailable)
	_, err = gateway.buildOpenAIResponsesWSURL(a)
	require.ErrorIs(t, err, errExcelOAuthRouteUnavailable)
	_, _, err = gateway.buildOpenAIWSHeaders(context.Background(), c, a, "synthetic", OpenAIWSProtocolDecision{}, false, "", "", "", "", "")
	require.ErrorIs(t, err, errExcelOAuthRouteUnavailable)
	svc := &AccountTestService{openaiGatewayService: gateway}
	err = svc.testOpenAICompactConnection(c, a, "gpt-5.6-sol")
	require.ErrorContains(t, err, errExcelOAuthRouteUnavailable.Error())
	a.Extra["openai_excel_bps"] = false
	err = svc.testOpenAIImageOAuth(c, context.Background(), a, "gpt-image-2", "A red square")
	require.ErrorContains(t, err, errExcelOAuthRouteUnavailable.Error())
	_, err = gateway.forwardOpenAIImagesOAuth(context.Background(), c, a, &OpenAIImagesRequest{Model: "gpt-image-2", Prompt: "A red square", Endpoint: openAIImagesGenerationsEndpoint}, "")
	require.ErrorIs(t, err, errExcelOAuthRouteUnavailable)
}

func TestExcelOAuthCatalogNeverQueriesCodex(t *testing.T) {
	for _, manifest := range []bool{false, true} {
		t.Run(fmt.Sprintf("manifest=%t", manifest), func(t *testing.T) {
			upstream := &httpUpstreamRecorder{resp: ordinaryModelsUpstreamResponse(bpsAccessFixture)}
			gateway := openAIClientToolsTestService(upstream)
			a := excelAccount()
			a.Credentials["client_id"] = openai.ExcelClientID
			a.Extra["openai_excel_bps_models"] = []string{"gpt-7-new"}
			var result *OpenAIModelsResponse
			var err error
			if manifest {
				result, err = gateway.FetchCodexModelsManifest(context.Background(), a, "", "")
			} else {
				result, err = gateway.FetchOpenAIModelsList(context.Background(), a)
			}
			require.NoError(t, err)
			require.Contains(t, string(result.Body), "gpt-7-new")
			require.NotContains(t, string(result.Body), "gpt-6-astra")
			require.Len(t, upstream.requests, 1)
			require.Equal(t, "/basispoints/api/responses/access", upstream.lastReq.URL.Path)
			a.Extra["openai_excel_bps"] = false
			if manifest {
				_, err = gateway.FetchCodexModelsManifest(context.Background(), a, "", "")
			} else {
				_, err = gateway.FetchOpenAIModelsList(context.Background(), a)
			}
			require.ErrorIs(t, err, errExcelOAuthRouteUnavailable)
			require.Len(t, upstream.requests, 1)
		})
	}
}

func TestExcelOAuthBPSAuthErrorStillDisables(t *testing.T) {
	upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 401, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"detail":"Unauthorized"}`))}}
	gateway := openAIClientToolsTestService(upstream)
	repo, _ := setupExcelBPSAuth(gateway)
	a := excelAccount()
	a.Credentials["client_id"] = openai.ExcelClientID
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	_, err := gateway.Forward(context.Background(), c, a, []byte(`{"model":"gpt-5.6-sol","input":"OK"}`))
	require.Error(t, err)
	require.Equal(t, "/basispoints/api/responses", upstream.lastReq.URL.Path)
	require.Equal(t, 1, repo.errorCalls)
}

func TestExcelOAuthSelectedModelBPSControl(t *testing.T) {
	for _, model := range []string{"gpt-5.6-sol", "alias"} {
		for _, manual := range []bool{false, true} {
			t.Run(fmt.Sprintf("model=%s/manual=%t", model, manual), func(t *testing.T) {
				wire := "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_control\",\"status\":\"completed\",\"model\":\"gpt-5.6-sol\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"OK\"}]}],\"usage\":{\"input_tokens\":10,\"output_tokens\":2}}}\n\n"
				wire = "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"OK\"}\n\n" + wire
				upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(wire))}}
				gateway := openAIClientToolsTestService(upstream)
				repo, _ := setupExcelBPSAuth(gateway)
				account := excelAccount()
				account.Credentials["client_id"] = openai.ExcelClientID
				account.Credentials["refresh_token"] = "synthetic-refresh"
				account.Credentials["model_mapping"] = map[string]any{"alias": "gpt-5.6-sol"}
				account.Extra["openai_excel_bps_models"] = []string{"gpt-5.6-sol"}
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
				var err error
				if manual {
					svc := &AccountTestService{openaiGatewayService: gateway, accountRepo: repo, httpUpstream: upstream, cfg: gateway.cfg}
					err = svc.testOpenAIAccountConnection(c, account, model, "Reply OK", "")
				} else {
					_, err = gateway.Forward(context.Background(), c, account, []byte(fmt.Sprintf(`{"model":%q,"input":"Reply OK"}`, model)))
				}
				require.NoError(t, err)
				require.Len(t, upstream.requests, 1)
				require.Equal(t, "/basispoints/api/responses", upstream.lastReq.URL.Path)
				require.Zero(t, repo.errorCalls)
				require.Zero(t, repo.tempCalls)
				require.Contains(t, rec.Body.String(), "OK")
				t.Logf("outbound=%s SetError=%d result=OK", upstream.lastReq.URL, repo.errorCalls)
			})
		}
	}
}

func TestExcelOAuthIsolationPreservesCodexMixedRoute(t *testing.T) {
	upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 401, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"detail":"Unauthorized"}`))}}
	gateway := openAIClientToolsTestService(upstream)
	repo, _ := setupExcelBPSAuth(gateway)
	a := excelAccount() // Primary credentials belong to Codex, not Excel.
	a.Extra["openai_excel_bps_models"] = []string{"gpt-5.6-sol"}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	_, err := gateway.Forward(context.Background(), c, a, []byte(`{"model":"gpt-6.1-sol","input":"OK"}`))
	require.Error(t, err)
	require.NotErrorIs(t, err, errExcelOAuthRouteUnavailable)
	require.Len(t, upstream.requests, 1)
	require.Equal(t, "/backend-api/codex/responses", upstream.lastReq.URL.Path)
	require.Equal(t, 1, repo.errorCalls)
}

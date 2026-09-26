package service

import (
	"encoding/json"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http/httptest"
	"testing"
)

func TestBuildExcelBPSAccountTestBodyUsesResponsesContract(t *testing.T) {
	raw, err := buildExcelBPSAccountTestBody("gpt-6-astra", "糖果题")
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if body["model"] != "gpt-6-astra" || body["stream"] != true || body["store"] != false {
		t.Fatalf("unexpected body: %#v", body)
	}
	input, _ := body["input"].([]any)
	item, _ := input[0].(map[string]any)
	contentItems, _ := item["content"].([]any)
	content, _ := contentItems[0].(map[string]any)
	if content["text"] != "糖果题" {
		t.Fatalf("prompt was not preserved: %#v", content)
	}
	reasoning, ok := body["reasoning"].(map[string]any)
	if !ok || reasoning["effort"] != "medium" {
		t.Fatalf("missing reasoning effort: %#v", body["reasoning"])
	}
}

func TestExcelBPSAccountOnlyUsesOAuth(t *testing.T) {
	oauth := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{"openai_excel_bps": true}}
	apiKey := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Extra: map[string]any{"openai_excel_bps": true}}
	if !oauth.IsExcelBPSEnabled() || apiKey.IsExcelBPSEnabled() {
		t.Fatal("Excel BPS gate must be OAuth-only")
	}
}

func TestExcelBPSManualTestSuppliesProxySessionIdentity(t *testing.T) {
	upstream := &httpUpstreamRecorder{}
	gateway := openAIClientToolsTestService(upstream)
	svc := &AccountTestService{openaiGatewayService: gateway}
	account := excelAccount()
	account.Extra["openai_excel_bps_mihomo"] = true
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/api/v1/admin/accounts/300/test", nil)
	err := svc.testExcelBPSAccountConnection(c, account, "gpt-6-astra", "Reply OK")
	// Missing managed proxy is expected in this fixture, but not a missing session.
	require.ErrorContains(t, err, "basispoints_proxy_unavailable")
	require.NotContains(t, err.Error(), "basispoints_session_required")
	require.Nil(t, upstream.lastReq)
	require.Empty(t, c.Request.Header.Get("Session-Id"), "test must not mutate the inbound request")
}

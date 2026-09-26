package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

var bpsProbeTestNonce = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)

type bpsProbeUpstream struct {
	mode              string
	bodies            [][]byte
	nonce             string
	disableAfterFirst *Account
}

func (u *bpsProbeUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	if req.URL.Host != "bps.openai.com" {
		return nil, fmt.Errorf("probe used %s instead of BPS", req.URL.Host)
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	u.bodies = append(u.bodies, body)
	if u.nonce == "" {
		u.nonce = bpsProbeTestNonce.FindString(string(body))
	}
	if u.nonce == "" {
		return nil, fmt.Errorf("probe request is missing a nonce")
	}
	step := len(u.bodies)
	if step == 1 && u.disableAfterFirst != nil {
		u.disableAfterFirst.Extra["openai_excel_bps"] = false
	}
	var output []any
	switch step {
	case 1, 3:
		answer := u.nonce
		if step == 1 && u.mode == "wrong_basic" || step == 3 && u.mode == "wrong_final" {
			answer = "wrong answer"
		}
		output = []any{map[string]any{"type": "message", "role": "assistant", "content": []any{
			map[string]any{"type": "output_text", "text": answer},
		}}}
	case 2:
		if !bytes.Contains(body, []byte(bpsAccountProbeTool)) {
			return nil, fmt.Errorf("probe tool was not declared")
		}
		name, nonce := bpsAccountProbeTool, u.nonce
		if u.mode == "wrong_tool" {
			name = "other_tool"
		}
		if u.mode == "wrong_argument" {
			nonce = "wrong nonce"
		}
		envelope, _ := json.Marshal(map[string]any{"name": name, "arguments": map[string]any{"nonce": nonce}})
		output = []any{map[string]any{"type": "function_call", "id": "fc_probe", "call_id": "call_probe",
			"name": "run_officejs", "status": "completed", "arguments": map[string]any{"code": string(envelope), "summary": "Echo nonce"}}}
	default:
		return nil, fmt.Errorf("unexpected fourth probe request")
	}
	if step == 3 && !bytes.Contains(body, []byte(`"function_call_output"`)) {
		return nil, fmt.Errorf("probe did not return the tool result")
	}
	eventType, status := "response.completed", "completed"
	if step == 1 && u.mode == "failed_terminal" {
		eventType, status = "response.failed", "failed"
	}
	event, _ := json.Marshal(map[string]any{"type": eventType, "response": map[string]any{
		"id": fmt.Sprintf("resp_probe_%d", step), "status": status, "model": "gpt-6-astra", "output": output,
	}})
	wire := "event: " + eventType + "\ndata: " + string(event) + "\n\n"
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(wire))}, nil
}

func (u *bpsProbeUpstream) DoWithTLS(req *http.Request, proxyURL string, accountID int64, accountConcurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxyURL, accountID, accountConcurrency)
}

func bpsProbeTestService(upstream *bpsProbeUpstream) *AccountTestService {
	gateway := &OpenAIGatewayService{httpUpstream: upstream, cfg: &config.Config{Security: config.SecurityConfig{
		URLAllowlist: config.URLAllowlistConfig{Enabled: false},
	}}}
	return &AccountTestService{openaiGatewayService: gateway}
}

func TestExcelBPSToolProbeRoundtrip(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := &bpsProbeUpstream{}
	svc := bpsProbeTestService(upstream)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/300/test", nil).WithContext(context.Background())
	require.NoError(t, svc.testExcelBPSToolRoundtrip(c, excelAccount(), "gpt-6-astra"))
	require.Equal(t, "text/event-stream", rec.Header().Get("Content-Type"))
	require.Len(t, upstream.bodies, 3)
	require.Contains(t, rec.Body.String(), `"type":"test_complete"`)
	require.NotContains(t, rec.Body.String(), upstream.nonce)
	require.Contains(t, string(upstream.bodies[1]), bpsAccountProbeTool)
	require.Contains(t, string(upstream.bodies[2]), `"function_call_output"`)
}

func TestExcelBPSToolProbeRejectsIncompleteStages(t *testing.T) {
	for _, tc := range []struct {
		mode      string
		wantCalls int
	}{
		{"wrong_basic", 1},
		{"failed_terminal", 1},
		{"wrong_tool", 3},
		{"wrong_argument", 2},
		{"wrong_final", 3},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			upstream := &bpsProbeUpstream{mode: tc.mode}
			svc := bpsProbeTestService(upstream)
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/300/test", nil)
			require.Error(t, svc.testExcelBPSToolRoundtrip(c, excelAccount(), "gpt-6-astra"))
			require.Len(t, upstream.bodies, tc.wantCalls)
			require.Contains(t, rec.Body.String(), `"type":"error"`)
			require.NotContains(t, rec.Body.String(), `"type":"test_complete"`)
		})
	}
}

func TestExcelBPSToolProbeRejectsModelsOutsideBPS(t *testing.T) {
	account := excelAccount()
	account.Extra["openai_excel_bps_models"] = []string{"gpt-6-astra"}
	upstream := &bpsProbeUpstream{}
	svc := bpsProbeTestService(upstream)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/300/test", nil)
	require.Error(t, svc.testExcelBPSToolRoundtrip(c, account, "gpt-5.6-sol"))
	require.Empty(t, upstream.bodies, "probe must not fall back to another upstream")
}

func TestExcelBPSToolProbeModeDoesNotFallBackToNative(t *testing.T) {
	account := excelAccount()
	delete(account.Extra, "openai_excel_bps")
	upstream := &bpsProbeUpstream{}
	svc := bpsProbeTestService(upstream)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/300/test", nil)
	require.Error(t, svc.testOpenAIAccountConnection(c, account, "gpt-6-astra", "", AccountTestModeBPSTools))
	require.Empty(t, upstream.bodies)
}

func TestExcelBPSToolProbeStopsWhenBPSIsDisabledBetweenSteps(t *testing.T) {
	account := excelAccount()
	upstream := &bpsProbeUpstream{disableAfterFirst: account}
	svc := bpsProbeTestService(upstream)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/300/test", nil)
	require.Error(t, svc.testExcelBPSToolRoundtrip(c, account, "gpt-6-astra"))
	require.Len(t, upstream.bodies, 1, "the next step must not use native Codex")
	require.NotContains(t, rec.Body.String(), `"type":"test_complete"`)
}

func TestExcelBPSToolProbeRejectsNativeFallbackBeforeDispatch(t *testing.T) {
	upstream := &bpsProbeUpstream{}
	svc := bpsProbeTestService(upstream)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/300/test", nil)
	c.Set(bpsAccountProbeRequiredContextKey, true)
	body := []byte(`{"model":"gpt-6-astra","input":"test","tools":[{"type":"image_generation"}]}`)
	_, err := svc.openaiGatewayService.Forward(c, c, excelAccount(), body)
	require.ErrorContains(t, err, "probe path is unavailable")
	require.Empty(t, upstream.bodies)
}

func TestBPSProbeExactCallRequiresPlaintextDeclaredTool(t *testing.T) {
	const nonce = "12345678-1234-1234-1234-123456789abc"
	for _, tc := range []struct {
		name   string
		call   map[string]any
		wantOK bool
	}{
		{"valid", map[string]any{"type": "function_call", "name": bpsAccountProbeTool, "call_id": "call_probe", "arguments": `{"nonce":"` + nonce + `"}`, "encrypted_function_args": []string{}}, true},
		{"missing plaintext marker", map[string]any{"type": "function_call", "name": bpsAccountProbeTool, "call_id": "call_probe", "arguments": `{"nonce":"` + nonce + `"}`}, false},
		{"encrypted arguments", map[string]any{"type": "function_call", "name": bpsAccountProbeTool, "call_id": "call_probe", "arguments": `{"nonce":"` + nonce + `"}`, "encrypted_function_args": []string{"nonce"}}, false},
		{"wrong nonce", map[string]any{"type": "function_call", "name": bpsAccountProbeTool, "call_id": "call_probe", "arguments": `{"nonce":"wrong"}`, "encrypted_function_args": []string{}}, false},
		{"extra arguments", map[string]any{"type": "function_call", "name": bpsAccountProbeTool, "call_id": "call_probe", "arguments": `{"nonce":"` + nonce + `","extra":true}`, "encrypted_function_args": []string{}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(tc.call)
			require.NoError(t, err)
			id, ok := bpsProbeExactCall(bpsAccountProbeResponse{Output: []json.RawMessage{raw}}, nonce)
			require.Equal(t, tc.wantOK, ok)
			if tc.wantOK {
				require.Equal(t, "call_probe", id)
			}
		})
	}
}

func TestExcelBPSProbeAdmissionBoundsConcurrentAccounts(t *testing.T) {
	svc := &AccountTestService{}
	first, ok := svc.beginExcelBPSProbe(1)
	require.True(t, ok)
	defer first()
	_, ok = svc.beginExcelBPSProbe(1)
	require.False(t, ok, "one account must not have overlapping probes")
	second, ok := svc.beginExcelBPSProbe(2)
	require.True(t, ok)
	defer second()
	third, ok := svc.beginExcelBPSProbe(3)
	require.True(t, ok)
	defer third()
	_, ok = svc.beginExcelBPSProbe(4)
	require.False(t, ok, "only three probes may run on one instance")
	first()
	first()
	fourth, ok := svc.beginExcelBPSProbe(4)
	require.True(t, ok)
	fourth()
	_, ok = svc.beginExcelBPSProbe(0)
	require.False(t, ok)
}

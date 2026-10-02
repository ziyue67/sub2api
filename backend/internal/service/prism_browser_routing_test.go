package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestPrismBrowserResponsesURL(t *testing.T) {
	tests := []struct {
		name string
		base string
		want string
	}{
		{name: "v1 base", base: "http://127.0.0.1:8319/v1", want: "http://127.0.0.1:8319/v1/responses"},
		{name: "responses suffix", base: "http://adapter.example/v1/responses/", want: "http://adapter.example/v1/responses"},
		{name: "empty", base: " ", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := prismBrowserResponsesURL(tt.base); got != tt.want {
				t.Fatalf("prismBrowserResponsesURL(%q) = %q, want %q", tt.base, got, tt.want)
			}
		})
	}
}

func TestPrismBrowserSessionIDUsesExistingCodexIdentity(t *testing.T) {
	identity := func(keyID, accountID int64, headers map[string]string, body string) (string, error) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		c.Set("api_key", &APIKey{ID: keyID})
		for k, v := range headers {
			c.Request.Header.Set(k, v)
		}
		return prismBrowserSessionID(c, accountID, []byte(body))
	}
	first, err := identity(7, 42, map[string]string{"session-id": "fixture"}, "{}")
	require.NoError(t, err)
	require.Len(t, first, 64)
	for _, tc := range []struct {
		name             string
		keyID, accountID int64
		headers          map[string]string
		body             string
		same             bool
	}{
		{"same session header alias", 7, 42, map[string]string{"session_id": "fixture"}, "{}", true},
		{"same session body", 7, 42, nil, `{"client_metadata":{"session_id":"fixture"}}`, true},
		{"private header cannot override", 7, 42, map[string]string{"session-id": "fixture", "X-Prism-Session-ID": "forged"}, "{}", true},
		{"other key", 8, 42, map[string]string{"session-id": "fixture"}, "{}", false},
		{"other account", 7, 43, map[string]string{"session-id": "fixture"}, "{}", false},
		{"other session", 7, 42, map[string]string{"session-id": "different"}, "{}", false},
		{"thread wins over shared session", 7, 42, map[string]string{"session-id": "fixture"}, `{"client_metadata":{"thread_id":"thread-a"}}`, false},
		{"thread namespace differs", 7, 42, map[string]string{"conversation_id": "fixture"}, "{}", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := identity(tc.keyID, tc.accountID, tc.headers, tc.body)
			require.NoError(t, err)
			require.NotEmpty(t, got)
			require.Equal(t, tc.same, first == got)
		})
	}
	threadA, err := identity(7, 42, map[string]string{"session-id": "shared"}, `{"client_metadata":{"thread_id":"a"}}`)
	require.NoError(t, err)
	threadB, err := identity(7, 42, map[string]string{"session-id": "shared"}, `{"client_metadata":{"thread_id":"b"}}`)
	require.NoError(t, err)
	require.NotEqual(t, threadA, threadB)
	for _, headers := range []map[string]string{nil, {"X-Prism-Session-ID": first}} {
		got, err := identity(7, 42, headers, "{}")
		require.NoError(t, err)
		require.Empty(t, got, "without a conversation, unrelated requests must use fresh projects")
	}
	_, err = identity(0, 42, map[string]string{"session-id": "fixture"}, "{}")
	require.Error(t, err)
}

func TestPrismBrowserSessionIDRejectsAmbiguousIdentity(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Set("api_key", &APIKey{ID: 7})
	c.Request.Header.Add("session_id", "one")
	c.Request.Header.Add("session_id", "two")
	_, err := prismBrowserSessionID(c, 42, nil)
	require.Error(t, err)
	c.Request.Header.Del("session_id")
	c.Request.Header.Set("session-id", "one")
	c.Request.Header.Set("session_id", "two")
	_, err = prismBrowserSessionID(c, 42, nil)
	require.Error(t, err)
	c.Request.Header.Del("session_id")
	_, err = prismBrowserSessionID(c, 42, []byte(`{"client_metadata":{"session_id":"conflict"}}`))
	require.Error(t, err)
}

func TestPrismBrowserSessionForwardAndStatelessAdminTest(t *testing.T) {
	const body = `{"model":"gpt-5.6-sol","input":"fixture"}`
	const terminal = `{"id":"resp_fixture","status":"completed","model":"gpt-5.6-sol","usage":null,"output":[{"content":[{"text":"21"}]}]}`
	requests := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, err := io.ReadAll(r.Body)
		if err != nil || string(got) != body {
			t.Error("adapter did not receive the original request body")
		}
		requests <- r.Header.Get("X-Prism-Session-ID")
		_, _ = io.WriteString(w, terminal)
	}))
	defer server.Close()
	s, account := prismTestService(server.URL)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Request.Header.Set("session-id", "fixture-session")
	c.Request.Header.Set("X-Prism-Session-ID", "forged-adapter-key")
	c.Set("api_key", &APIKey{ID: 7})
	expected, err := prismBrowserSessionID(c, account.ID, []byte(body))
	require.NoError(t, err)
	_, err = s.forwardPrismBrowser(context.Background(), c, account, []byte(body), time.Now())
	require.NoError(t, err)
	require.Equal(t, expected, <-requests)
	require.NotEqual(t, "fixture-session", expected)
	_, _, _, err = s.callPrismBrowser(context.Background(), account, []byte(body))
	require.NoError(t, err)
	require.Empty(t, <-requests, "admin tests always create a fresh project")
}

func TestPrismBrowserInvalidSessionDoesNotDispatch(t *testing.T) {
	s, account := prismTestService("http://127.0.0.1:1")
	for _, value := range []string{"with\tcontrol", "with\ncontrol", strings.Repeat("x", 256)} {
		t.Run(value, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			c.Request.Header.Set("session-id", value)
			c.Set("api_key", &APIKey{ID: 7})
			_, err := s.forwardPrismBrowser(context.Background(), c, account, []byte(`{"model":"gpt-5.6-sol","input":"fixture"}`), time.Now())
			require.Error(t, err)
			require.Equal(t, http.StatusBadRequest, w.Code, "invalid identity must be rejected before contacting the adapter")
		})
	}
}

func prismTestService(endpoint string) (*OpenAIGatewayService, *Account) {
	cfg := &config.Config{}
	cfg.Gateway.PrismBrowser = config.GatewayPrismBrowserConfig{Enabled: true, BaseURL: endpoint + "/v1", APIKey: "fixture-bridge-key"}
	return &OpenAIGatewayService{cfg: cfg}, &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Credentials: map[string]any{"access_token": "fixture-oauth"}, Extra: map[string]any{"openai_prism_browser": true}}
}

func TestPrismBrowserCallProtectsCredentialBoundary(t *testing.T) {
	var count int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		if r.URL.Path != "/v1/responses" || r.Header.Get("Authorization") != "Bearer fixture-bridge-key" ||
			r.Header.Get("X-Prism-OAuth-Token") != "fixture-oauth" || r.Header.Get("X-Prism-Account-ID") != "42" {
			t.Error("unexpected adapter request")
		}
		w.Header().Set("Location", "http://127.0.0.1:1/credential-leak")
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	s, account := prismTestService(server.URL)
	if _, _, _, err := s.callPrismBrowser(context.Background(), account, []byte(`{"input":"test"}`)); err == nil {
		t.Fatal("redirect must be rejected")
	}
	if count != 1 {
		t.Fatalf("request count = %d", count)
	}
	s.cfg.Gateway.PrismBrowser.Enabled = false
	if !accountHasPrismBrowser(account) {
		t.Fatal("disabled server config must not erase account intent")
	}
	if _, _, _, err := s.callPrismBrowser(context.Background(), account, nil); err == nil {
		t.Fatal("disabled adapter must fail closed")
	}
	if count != 1 {
		t.Fatal("disabled adapter submitted a request")
	}
}

func TestPrismBrowserForwardTerminalAndUsage(t *testing.T) {
	const terminal = `{"id":"resp_fixture","status":"completed","model":"gpt-5.6-sol","usage":null,"output":[{"content":[{"text":"21"}]}]}`
	for _, stream := range []bool{false, true} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if stream {
				_, _ = io.WriteString(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":"+terminal+"}\n\n")
			} else {
				_, _ = io.WriteString(w, terminal)
			}
		}))
		s, account := prismTestService(server.URL)
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		body := `{"model":"gpt-5.6-sol","input":"candy","stream":false}`
		if stream {
			body = strings.Replace(body, "false", "true", 1)
		}
		result, err := s.forwardPrismBrowser(context.Background(), c, account, []byte(body), time.Now())
		server.Close()
		if err != nil || w.Code != http.StatusOK || result == nil || !result.UsageUnavailable || result.ResponseID != "resp_fixture" {
			t.Fatalf("unexpected result: result=%+v status=%d err=%v", result, w.Code, err)
		}
		if err := s.RecordUsage(context.Background(), &OpenAIRecordUsageInput{Result: result}); err == nil {
			t.Fatal("unknown usage must not enter billing as zero tokens")
		}
	}
	for _, raw := range []string{`event: response.completed`, `data: {"type":"response.failed"}`, "data: not-json"} {
		if _, err := prismBrowserTerminal([]byte(raw), "gpt-5.6-sol", true); err == nil {
			t.Fatal("invalid stream accepted")
		}
	}
	if _, err := prismBrowserTerminal([]byte(terminal), "gpt-6-astra", false); err == nil {
		t.Fatal("model substitution accepted")
	}
}

func TestPrismBrowserAdapterURLStaysOnLoopback(t *testing.T) {
	valid := []string{"http://127.0.0.1:8319/v1", "http://[::1]:8319/v1/responses"}
	for _, input := range valid {
		if _, err := prismBrowserAdapterURL(input); err != nil {
			t.Fatalf("valid adapter %q rejected: %v", input, err)
		}
	}
	invalid := []string{
		"https://127.0.0.1:8319/v1", "http://localhost:8319/v1",
		"http://adapter.example:8319/v1", "http://127.0.0.2:8319/v1",
		"http://127.0.0.1:8319/v1?next=evil", "http://user@127.0.0.1:8319/v1",
		"http://127.0.0.1:8319/other", "http://127.0.0.1/v1",
	}
	for _, input := range invalid {
		if _, err := prismBrowserAdapterURL(input); err == nil {
			t.Fatalf("unsafe adapter %q accepted", input)
		}
	}
}

func TestAccountUsesPrismBrowserRequiresServerAndAccountSwitch(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.PrismBrowser.Enabled = true

	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{"openai_prism_browser": true}}
	if !accountUsesPrismBrowser(account, cfg) {
		t.Fatal("enabled OpenAI account should use Prism browser adapter")
	}

	account.Extra["openai_prism_browser"] = false
	if accountUsesPrismBrowser(account, cfg) {
		t.Fatal("disabled account switch should keep the normal route")
	}

	account.Extra["openai_prism_browser"] = true
	cfg.Gateway.PrismBrowser.Enabled = false
	if accountUsesPrismBrowser(account, cfg) {
		t.Fatal("disabled server adapter must prevent Prism routing")
	}

	cfg.Gateway.PrismBrowser.Enabled = true
	account.Type = AccountTypeAPIKey
	if accountUsesPrismBrowser(account, cfg) {
		t.Fatal("API key accounts must not use the OAuth Prism bridge")
	}
}

func TestPrismBrowserAdapterMisconfigurationIsNotTheClientsAuthFailure(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     int
		body       string
		wantStatus int
	}{
		{name: "bridge key mismatch", status: http.StatusUnauthorized, body: `{"error":{"type":"unauthorized"}}`, wantStatus: http.StatusBadGateway},
		{name: "adapter path mismatch", status: http.StatusNotFound, body: `{"error":{"type":"not_found"}}`, wantStatus: http.StatusBadGateway},
		{name: "request refused before dispatch", status: http.StatusUnprocessableEntity, body: `{"error":{"type":"unsupported_request","message":"Prism adapter does not yet support tools or server-side conversation state"}}`, wantStatus: http.StatusUnprocessableEntity},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			s, account := prismTestService(server.URL)
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

			_, err := s.forwardPrismBrowser(context.Background(), c, account, []byte(`{"model":"gpt-5.6-sol","input":"hi"}`), time.Now())

			require.Error(t, err)
			require.Equal(t, tc.wantStatus, w.Code)
			if tc.wantStatus == http.StatusBadGateway {
				require.Contains(t, w.Body.String(), `"prism_unavailable"`, "the client's own API key was not rejected")
				return
			}
			require.JSONEq(t, tc.body, w.Body.String())
		})
	}
}

func TestPrismBrowserAccountTestExplainsAdapterRefusal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(w, `{"error":{"type":"pending_turn","message":"Previous Prism turn outcome is unknown; inspect it before a new request"}}`)
	}))
	defer server.Close()
	gateway, account := prismTestService(server.URL)
	svc := &AccountTestService{openaiGatewayService: gateway}
	for _, tc := range []struct {
		name    string
		enabled bool
		want    string
	}{
		{name: "adapter refusal", enabled: true, want: "Prism adapter returned HTTP 409 (pending_turn): Previous Prism turn outcome is unknown"},
		{name: "gateway switch off", enabled: false, want: "Prism adapter request failed: prism adapter is disabled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gateway.cfg.Gateway.PrismBrowser.Enabled = tc.enabled
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/42/test", nil)

			require.Error(t, svc.testPrismBrowserConnection(c, account, "gpt-5.6-sol", "hi"))
			require.Contains(t, rec.Body.String(), tc.want)
		})
	}
}

// A WebSocket session placed on a Prism account is closed right after selection,
// so the scheduler must keep such sessions on the other accounts, as it does for
// Excel BPS models. HTTP requests still reach the Prism account.
func TestPrismBrowserAccountsAreNotScheduledForWebSocketSessions(t *testing.T) {
	ctx := context.Background()
	groupID := int64(26001)
	const prismID, nativeID = int64(26011), int64(26012)
	accounts := []Account{
		{ID: prismID, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true,
			Concurrency: 1, Priority: 0, GroupIDs: []int64{groupID},
			Credentials: map[string]any{"access_token": "fixture-oauth"},
			Extra:       map[string]any{"openai_prism_browser": true, "openai_oauth_responses_websockets_v2_enabled": true}},
		{ID: nativeID, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true,
			Concurrency: 1, Priority: 5, GroupIDs: []int64{groupID},
			Credentials: map[string]any{"access_token": "fixture-oauth"},
			Extra:       map[string]any{"openai_oauth_responses_websockets_v2_enabled": true}},
	}
	release := func(selection *AccountSelectionResult) {
		if selection != nil && selection.ReleaseFunc != nil {
			selection.ReleaseFunc()
		}
	}
	for _, mode := range []string{"advanced", "load_batch", "priority"} {
		t.Run(mode, func(t *testing.T) {
			resetOpenAIAdvancedSchedulerSettingCacheForTest()
			cfg := newSchedulerTestOpenAIWSV2Config()
			cfg.Gateway.Scheduling.LoadBatchEnabled = mode == "load_batch"
			svc := &OpenAIGatewayService{
				accountRepo:        schedulerTestOpenAIAccountRepo{accounts: accounts},
				cache:              &schedulerTestGatewayCache{},
				cfg:                cfg,
				concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{}),
			}
			if mode == "advanced" {
				svc.rateLimitService = newOpenAIAdvancedSchedulerRateLimitService("true")
			}
			// An earlier HTTP turn bound the session to the Prism account.
			require.NoError(t, svc.setStickySessionAccountID(ctx, &groupID, "prism-ws-session", prismID, time.Hour))

			selection, _, err := svc.SelectAccountWithSchedulerForCapability(ctx, &groupID, "", "prism-ws-session", "gpt-5.6-sol", nil,
				OpenAIUpstreamTransportResponsesWebsocketV2Ingress, OpenAIEndpointCapabilityChatCompletions, false, false, true)
			require.NoError(t, err)
			release(selection)
			require.Equal(t, nativeID, selection.Account.ID)

			selection, _, err = svc.SelectAccountWithSchedulerForCapability(ctx, &groupID, "", "", "gpt-5.6-sol", map[int64]struct{}{nativeID: {}},
				OpenAIUpstreamTransportResponsesWebsocketV2Ingress, OpenAIEndpointCapabilityChatCompletions, false, false, true)
			release(selection)
			require.Error(t, err, "the Prism account must not take a WebSocket session")

			selection, _, err = svc.SelectAccountWithSchedulerForCapability(ctx, &groupID, "", "", "gpt-5.6-sol", map[int64]struct{}{nativeID: {}},
				OpenAIUpstreamTransportAny, OpenAIEndpointCapabilityChatCompletions, false, false, true)
			require.NoError(t, err)
			release(selection)
			require.Equal(t, prismID, selection.Account.ID)
		})
	}
}

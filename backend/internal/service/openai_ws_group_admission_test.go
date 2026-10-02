package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// groupAdmissionRepo is the authoritative turn-admission reader. When later is
// set, every read after the first returns it: an admin moved the account to
// another group after the scheduler picked it.
type groupAdmissionRepo struct {
	AccountRepository
	mu           sync.Mutex
	first, later *Account
	reads        int
}

func (r *groupAdmissionRepo) GetOpenAITurnAdmission(context.Context, int64) (*Account, *Account, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reads++
	if r.reads > 1 && r.later != nil {
		return r.later, nil, nil
	}
	return r.first, nil, nil
}

func (r *groupAdmissionRepo) Reads() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reads
}

func groupAdmissionAccount(groupIDs ...int64) *Account {
	return &Account{
		ID: 252, Name: "group-admission", Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Status: StatusActive, Schedulable: true, Concurrency: 1, GroupIDs: groupIDs,
		Credentials: map[string]any{"access_token": "test-token", "chatgpt_account_id": "test-account"},
		Extra:       map[string]any{"openai_oauth_responses_websockets_v2_enabled": true},
	}
}

func newGroupAdmissionWSv2Service(t *testing.T, repo *groupAdmissionRepo, conn *openAIWSCaptureConn) (*OpenAIGatewayService, *openAIWSCaptureDialer) {
	t.Helper()
	cfg := passthroughLifecycleConfig()
	cfg.Gateway.OpenAIWS.OAuthEnabled = true
	cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
	cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 1
	dialer := &openAIWSCaptureDialer{conn: conn}
	pool := newOpenAIWSConnPool(cfg)
	pool.setClientDialerForTest(dialer)
	t.Cleanup(pool.Close)
	return &OpenAIGatewayService{
		cfg:                        cfg,
		httpUpstream:               &httpUpstreamRecorder{},
		cache:                      &stubGatewayCache{},
		openaiWSResolver:           NewOpenAIWSProtocolResolver(cfg),
		toolCorrector:              NewCodexToolCorrector(),
		openaiWSPool:               pool,
		accountRepo:                repo,
		requireLatestTurnAdmission: true,
	}, dialer
}

func requireGroupMembershipDenied(t *testing.T, err error) {
	t.Helper()
	var denied *OpenAITurnAdmissionError
	require.ErrorAs(t, err, &denied)
	require.Equal(t, "group_membership_changed", denied.Reason)
}

// Regression for #252: the Pelican and manual account tests carry no API key.
// Over HTTP such a send is not bound to any group; the native Codex WS path
// used to judge it as a group-0 request and reject every grouped account.
func TestExcelBPSAccountTestOutsideBPSModelsPassesWSGroupAdmission(t *testing.T) {
	gin.SetMode(gin.TestMode)
	account := groupAdmissionAccount(7)
	account.Extra["openai_excel_bps"] = true
	account.Extra["openai_excel_bps_models"] = []any{"gpt-6-astra", "gpt-5.6-sol", "gpt-5.6-terra"}
	conn := &openAIWSCaptureConn{events: [][]byte{
		[]byte(`{"type":"response.output_text.delta","delta":"OK"}`),
		[]byte(`{"type":"response.completed","response":{"id":"resp_252","model":"gpt-6.1-sol","usage":{"input_tokens":1,"output_tokens":1}}}`),
	}}
	gateway, _ := newGroupAdmissionWSv2Service(t, &groupAdmissionRepo{first: account}, conn)
	svc := &AccountTestService{openaiGatewayService: gateway}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = (&http.Request{Header: make(http.Header)}).WithContext(context.Background())

	err := svc.testExcelBPSAccountConnection(c, account, "gpt-6.1-sol", "Reply OK")

	require.NoError(t, err, rec.Body.String())
	require.NotNil(t, conn.lastWrite, "the test must reach the native WS upstream")
	require.Contains(t, rec.Body.String(), `"type":"test_complete"`)
}

func TestOpenAIWSv2GroupAdmissionFollowsAPIKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	keyGroup := int64(7)
	for _, tc := range []struct {
		name        string
		apiKey      *APIKey
		laterGroups []int64
		wantDenied  bool
	}{
		{name: "keyless test of a grouped account"},
		{name: "user request inside its group", apiKey: &APIKey{GroupID: &keyGroup}},
		{name: "user request after the account left the group", apiKey: &APIKey{GroupID: &keyGroup}, laterGroups: []int64{8}, wantDenied: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := groupAdmissionAccount(7)
			repo := &groupAdmissionRepo{first: account}
			if tc.laterGroups != nil {
				moved := *account
				moved.GroupIDs = tc.laterGroups
				repo.later = &moved
			}
			conn := &openAIWSCaptureConn{events: [][]byte{
				[]byte(`{"type":"response.completed","response":{"id":"resp_252","model":"gpt-5.1","usage":{"input_tokens":1,"output_tokens":1}}}`),
			}}
			svc, dialer := newGroupAdmissionWSv2Service(t, repo, conn)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
			if tc.apiKey != nil {
				c.Set("api_key", tc.apiKey)
			}

			result, err := svc.Forward(context.Background(), c, account, []byte(`{"model":"gpt-5.1","stream":false,"input":"hello"}`))

			if tc.wantDenied {
				// Forward's entry check passed; the pool's pre-dial check stops
				// the handshake, so nothing reaches upstream.
				requireGroupMembershipDenied(t, err)
				require.Equal(t, 2, repo.Reads())
				require.Zero(t, dialer.DialCount())
				require.Nil(t, conn.lastWrite)
				return
			}
			require.NoError(t, err)
			require.True(t, result.OpenAIWSMode)
			require.NotNil(t, conn.lastWrite)
		})
	}
}

func startGroupAdmissionIngressServer(t *testing.T, ctx context.Context, svc *OpenAIGatewayService, account *Account, apiKey *APIKey) <-chan error {
	t.Helper()
	errs := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, nil)
		if err != nil {
			errs <- err
			return
		}
		defer func() { _ = conn.CloseNow() }()
		_, first, err := ReadOpenAIWSClientMessage(ctx, conn, 3*time.Second, coderws.StatusPolicyViolation, "missing first response.create message")
		if err != nil {
			errs <- err
			return
		}
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = r.Clone(ctx)
		if apiKey != nil {
			c.Set("api_key", apiKey)
		}
		errs <- svc.ProxyResponsesWebSocketFromClient(ctx, c, conn, account, "sk-test", first, nil)
	}))
	t.Cleanup(server.Close)
	client, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.CloseNow() })
	require.NoError(t, client.Write(ctx, coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-5.1","input":"hello"}`)))
	return errs
}

// The client WS entry point builds the same pool callbacks. Its requests always
// carry an API key in production; the keyless case pins the shared rule.
func TestOpenAIWSIngressGroupAdmissionFollowsAPIKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	keyGroup := int64(7)
	for _, tc := range []struct {
		name       string
		apiKey     *APIKey
		groupIDs   []int64
		wantDenied bool
	}{
		{name: "keyless session of a grouped account", groupIDs: []int64{7}},
		{name: "user session inside its group", apiKey: &APIKey{GroupID: &keyGroup}, groupIDs: []int64{7}},
		{name: "user session on an account outside its group", apiKey: &APIKey{GroupID: &keyGroup}, groupIDs: []int64{8}, wantDenied: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			cfg := passthroughLifecycleConfig()
			cfg.Gateway.OpenAIWS.OAuthEnabled = true
			cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 3
			cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
			cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 1
			account := groupAdmissionAccount(tc.groupIDs...)
			account.Extra = map[string]any{"openai_oauth_responses_websockets_v2_mode": OpenAIWSIngressModeCtxPool}
			upstream := newStagedPassthroughConn()
			svc := newPassthroughLifecycleService(cfg, upstream)
			svc.accountRepo = &groupAdmissionRepo{first: account}
			svc.requireLatestTurnAdmission = true
			pool := newOpenAIWSConnPool(cfg)
			pool.setClientDialerForTest(&stagedPassthroughDialer{conn: &turnAdmissionNativeConn{upstream}})
			svc.openaiWSPool = pool
			defer pool.Close()

			errs := startGroupAdmissionIngressServer(t, ctx, svc, account, tc.apiKey)

			if tc.wantDenied {
				select {
				case err := <-errs:
					requireGroupMembershipDenied(t, err)
				case <-upstream.writes:
					t.Fatal("a turn outside the API key's group reached upstream")
				case <-ctx.Done():
					t.Fatal("the denied session did not end")
				}
				return
			}
			select {
			case <-upstream.writes:
			case err := <-errs:
				t.Fatalf("session ended before the turn reached upstream: %v", err)
			case <-ctx.Done():
				t.Fatal("the turn did not reach upstream")
			}
		})
	}
}

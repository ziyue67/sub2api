//go:build unit

package handler

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type mixedGroupRepo struct {
	service.GroupRepository
	groups map[int64]*service.Group
}

func (r mixedGroupRepo) GetByIDLite(_ context.Context, id int64) (*service.Group, error) {
	return r.groups[id], nil
}
func (r mixedGroupRepo) GetByID(ctx context.Context, id int64) (*service.Group, error) {
	return r.GetByIDLite(ctx, id)
}

type mixedGroupProbe struct{}

func (mixedGroupProbe) ProbeAPIKeyGroup(context.Context, *service.Group, string, string) (service.APIKeyGroupAvailability, error) {
	return service.APIKeyGroupAvailability{Available: true}, nil
}

type mixedGroupUpstream struct {
	service.HTTPUpstream
	calls     []int64
	paths     []string
	streaming bool
}

func (u *mixedGroupUpstream) Do(req *http.Request, _ string, id int64, _ int) (*http.Response, error) {
	u.calls = append(u.calls, id)
	u.paths = append(u.paths, req.URL.Path)
	if id == 101 {
		return &http.Response{StatusCode: 503, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"synthetic unavailable","type":"server_error"}}`))}, nil
	}
	if u.streaming {
		body := "data: " + `{"id":"chatcmpl-test","object":"chat.completion.chunk","model":"deepseek-v4.1-flash","choices":[{"index":0,"delta":{"role":"assistant","content":"fallback OK"},"finish_reason":null}]}` + "\n\n" +
			"data: " + `{"id":"chatcmpl-test","object":"chat.completion.chunk","model":"deepseek-v4.1-flash","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":20,"completion_tokens":5,"total_tokens":25}}` + "\n\ndata: [DONE]\n\n"
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	}
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"id":"chatcmpl-test","object":"chat.completion","model":"deepseek-v4.1-flash","choices":[{"index":0,"message":{"role":"assistant","content":"fallback OK"},"finish_reason":"stop"}],"usage":{"prompt_tokens":20,"completion_tokens":5,"total_tokens":25}}`))}, nil
}
func (u *mixedGroupUpstream) DoWithTLS(req *http.Request, proxy string, id int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxy, id, concurrency)
}

func TestOpenAITextMixedGroupFailover(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, mode := range []string{"chat/completions", "responses", "chat/completions-stream", "responses-stream"} {
		endpoint := strings.TrimSuffix(mode, "-stream")
		streaming := strings.HasSuffix(mode, "-stream")
		for _, platforms := range [][2]string{{service.PlatformDeepseek, service.PlatformOpenAI}, {service.PlatformOpenAI, service.PlatformDeepseek}} {
			t.Run(mode+"/"+platforms[0]+"-to-"+platforms[1], func(t *testing.T) {
				groups := mixedGroupRepo{groups: map[int64]*service.Group{
					1: {ID: 1, Platform: platforms[0], Status: service.StatusActive, RateMultiplier: 0.06, Hydrated: true},
					2: {ID: 2, Platform: platforms[1], Status: service.StatusActive, RateMultiplier: 0.065, Hydrated: true},
				}}
				var accounts []service.Account
				for i, platform := range platforms {
					accounts = append(accounts, service.Account{ID: int64(101 + i), Platform: platform, Type: service.AccountTypeAPIKey,
						Status: service.StatusActive, Schedulable: true, GroupIDs: []int64{int64(i + 1)},
						Credentials: map[string]any{"api_key": "synthetic-test-key", "base_url": "https://mock.example.test", "api_protocol": "chat_completions", "model_mapping": map[string]any{"deepseek-v4.1-flash": "deepseek-v4.1-flash"}}})
				}
				cfg := &config.Config{RunMode: config.RunModeSimple}
				cfg.Default.RateMultiplier = 1
				cfg.Gateway.MaxAccountSwitches = 1
				keys := service.NewAPIKeyService(nil, nil, groups, nil, nil, nil, cfg)
				keys.SetGroupRouter(mixedGroupProbe{})
				primary := int64(1)
				key := &service.APIKey{ID: 7, UserID: 7, AllowLongContext: true, GroupID: &primary, Group: groups.groups[1],
					User: &service.User{ID: 7, Status: service.StatusActive, Balance: 10},
					GroupRouting: &domain.APIKeyGroupRouting{Strategy: service.APIKeyRoutingLowestRate, Failover: true, Routes: []domain.APIKeyGroupRoute{
						{GroupID: 1, Priority: 1, Weight: 1, Enabled: true}, {GroupID: 2, Priority: 2, Weight: 1, Enabled: true},
					}}}
				selected, err := keys.SelectRequestGroup(context.Background(), key, []string{"deepseek-v4.1-flash"}, "token", nil, nil, false)
				require.NoError(t, err)
				upstream := &mixedGroupUpstream{streaming: streaming}
				usage := &openAIWSUsageHandlerUsageLogRepoStub{created: make(chan *service.UsageLog, 4)}
				billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
				t.Cleanup(billing.Stop)
				gateway := service.NewOpenAIGatewayService(&openAIWSFailoverHandlerAccountRepoStub{accounts: accounts}, nil, usage, nil, nil, nil, nil, nil, cfg, nil, nil,
					service.NewBillingService(cfg, nil), nil, billing, upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil)
				t.Cleanup(gateway.StopOpenAICodexTicketHarvester)
				t.Cleanup(gateway.CloseOpenAIWSPool)
				h := NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(nil), billing, keys, nil, nil, nil, nil, cfg)
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				body := `{"model":"deepseek-v4.1-flash","messages":[{"role":"user","content":"hello"}],"stream":false}`
				if endpoint == "responses" {
					body = `{"model":"deepseek-v4.1-flash","input":"hello","stream":false}`
				}
				if streaming {
					body = strings.ReplaceAll(body, `"stream":false`, `"stream":true`)
				}
				c.Request = httptest.NewRequest("POST", "/v1/"+endpoint, strings.NewReader(body))
				c.Request.Header.Set("Content-Type", "application/json")
				c.Set(string(middleware.ContextKeyAPIKey), selected)
				c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 7})
				if endpoint == "responses" {
					h.Responses(c)
				} else {
					h.ChatCompletions(c)
				}
				require.Equal(t, []int64{101, 102}, upstream.calls, rec.Body.String())
				require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
				require.Contains(t, rec.Body.String(), "fallback OK")
				for _, path := range upstream.paths {
					require.True(t, strings.HasSuffix(path, "/chat/completions"), "explicit account protocol must remain Chat Completions: %s", path)
				}
				if streaming {
					if endpoint == "responses" {
						require.Contains(t, rec.Body.String(), "response.completed")
					} else {
						require.Contains(t, rec.Body.String(), "[DONE]")
					}
				}
				require.Equal(t, int64(2), *selected.GroupID)
				select {
				case log := <-usage.created:
					require.Equal(t, int64(2), *log.GroupID)
					require.Equal(t, int64(102), log.AccountID)
					require.InDelta(t, 0.065, log.RateMultiplier, 0.000001)
					require.EqualValues(t, 20, log.InputTokens)
					require.EqualValues(t, 5, log.OutputTokens)
				case <-time.After(3 * time.Second):
					t.Fatal("missing fallback usage record")
				}
				require.Empty(t, usage.created, "failed upstream without usage must not create another bill")
			})
		}
	}
}

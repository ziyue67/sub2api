package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestWSNodeOwnerNativeIngress(t *testing.T) {
	for _, name := range []string{"same-boot-two-turns", "default-disabled-two-turns", "unknown-owner-no-upstream-send", "foreign-boot-no-send", "lost-local-binding-no-send", "lost-pool-connection-no-send", "cache-failure-no-send", "publication-failure-no-replay"} {
		t.Run(name, func(t *testing.T) {
			foreign := name == "unknown-owner-no-upstream-send"
			seeded := name == "foreign-boot-no-send" || name == "lost-local-binding-no-send" || name == "lost-pool-connection-no-send" || name == "cache-failure-no-send"
			rejected := foreign || seeded
			publicationFailure := name == "publication-failure-no-replay"
			gin.SetMode(gin.TestMode)
			cache := &wsNodeOwnerCacheStub{}
			if publicationFailure {
				cache.err = errors.New("fixture publication failure")
			}
			svc := ownerTestService(cache, "node-a")
			if name == "default-disabled-two-turns" {
				svc.cfg.Runtime.WSNodeOwner.Enabled = false
				cache.err = errors.New("disabled gate must not call owner cache")
			}
			ref := WSNodeOwnerRef{GroupID: 17, APIKeyID: 101, AccountID: 201, ResponseID: "resp_foreign"}
			if seeded {
				publisher := svc
				if name == "foreign-boot-no-send" {
					publisher = ownerTestService(cache, "node-a")
				}
				publisher.getOpenAIWSStateStore().BindResponseConn(ref.ResponseID, "previous-pool-conn", time.Minute)
				require.NoError(t, publisher.bindWSNodeOwner(context.Background(), ref))
				svc.getOpenAIWSStateStore().BindResponseConn(ref.ResponseID, "previous-pool-conn", time.Minute)
				if name == "lost-local-binding-no-send" {
					svc.getOpenAIWSStateStore().DeleteResponseConn(ref.ResponseID)
				}
				if name == "cache-failure-no-send" {
					cache.err = errors.New("fixture private cache failure")
				}
			}
			cfg := svc.cfg
			cfg.Gateway.OpenAIWS.Enabled = true
			cfg.Gateway.OpenAIWS.APIKeyEnabled = true
			cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
			cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
			cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 1
			cfg.Gateway.OpenAIWS.QueueLimitPerConn = 8
			cfg.Gateway.OpenAIWS.DialTimeoutSeconds = 3
			cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 3
			cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 3
			capture := &openAIWSCaptureConn{events: [][]byte{
				[]byte(`{"type":"response.completed","response":{"id":"resp_owner_1","model":"gpt-6.1-sol","usage":{"input_tokens":1,"output_tokens":1}}}`),
				[]byte(`{"type":"response.completed","response":{"id":"resp_owner_2","model":"gpt-6.1-sol","usage":{"input_tokens":1,"output_tokens":1}}}`),
			}}
			dialer := &openAIWSCaptureDialer{conn: capture}
			svc.openaiWSPool = newOpenAIWSConnPool(cfg)
			svc.openaiWSPool.setClientDialerForTest(dialer)
			svc.openaiWSResolver = NewOpenAIWSProtocolResolver(cfg)
			svc.toolCorrector = NewCodexToolCorrector()
			account := &Account{ID: 201, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
				Concurrency: 1, GroupIDs: []int64{17}, Credentials: map[string]any{"api_key": "fixture-not-a-key"}, Extra: map[string]any{"responses_websockets_v2_enabled": true}}
			result := make(chan error, 1)
			var completed atomic.Int64
			var afterTurn atomic.Int64
			hooks := &OpenAIWSIngressHooks{AfterTurn: func(_ int, r *OpenAIForwardResult, err error) {
				afterTurn.Add(1)
				if err == nil && r != nil {
					completed.Add(1)
				}
			}}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := coderws.Accept(w, r, nil)
				if err != nil {
					result <- err
					return
				}
				defer func() { _ = conn.CloseNow() }()
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = r
				groupID := int64(17)
				c.Set("api_key", &APIKey{ID: 101, GroupID: &groupID})
				ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
				defer cancel()
				_, first, err := conn.Read(ctx)
				if err == nil {
					err = svc.ProxyResponsesWebSocketFromClient(ctx, c, conn, account, "fixture-not-a-key", first, hooks)
				}
				result <- err
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			client, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
			require.NoError(t, err)
			defer func() { _ = client.CloseNow() }()
			first := `{"type":"response.create","model":"gpt-6.1-sol","store":false,"stream":false}`
			if rejected {
				first = `{"type":"response.create","model":"gpt-6.1-sol","store":false,"previous_response_id":"resp_foreign"}`
			}
			require.NoError(t, client.Write(ctx, coderws.MessageText, []byte(first)))
			if !rejected {
				_, event, err := client.Read(ctx)
				require.NoError(t, err)
				require.Equal(t, "resp_owner_1", gjson.GetBytes(event, "response.id").String())
				if !publicationFailure {
					require.NoError(t, client.Write(ctx, coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-6.1-sol","store":false,"previous_response_id":"resp_owner_1","stream":false}`)))
					_, event, err = client.Read(ctx)
					require.NoError(t, err)
					require.Equal(t, "resp_owner_2", gjson.GetBytes(event, "response.id").String())
					require.Eventually(t, func() bool { return completed.Load() == 2 }, time.Second, time.Millisecond)
					_ = client.Close(coderws.StatusNormalClosure, "fixture done")
				}
			}
			select {
			case err := <-result:
				if rejected {
					require.ErrorIs(t, err, ErrOpenAIWSNodeOwner)
					var retry *UpstreamFailoverError
					require.False(t, errors.As(err, &retry), "owner rejection must not trigger account failover")
					require.NotContains(t, err.Error(), "fixture private")
					require.Zero(t, dialer.DialCount())
					require.Empty(t, capture.writes)
					require.Zero(t, completed.Load())
					if name == "lost-pool-connection-no-send" {
						require.LessOrEqual(t, afterTurn.Load(), int64(1), "failed acquisition may only release the admitted turn once")
					} else {
						require.Zero(t, afterTurn.Load(), "pre-admission refusal must not account a model turn")
					}
				} else if publicationFailure {
					require.ErrorIs(t, err, ErrOpenAIWSNodeOwner)
					require.Len(t, capture.writes, 1, "completed generation must not replay after owner publication fails")
					require.EqualValues(t, 1, completed.Load(), "existing AfterTurn must account for the completed call exactly once")
					require.EqualValues(t, 1, afterTurn.Load(), "publication failure must not duplicate the accounting callback")
				} else {
					require.NoError(t, err)
					require.Equal(t, 1, dialer.DialCount())
					require.Len(t, capture.writes, 2)
					require.EqualValues(t, 2, completed.Load())
					require.EqualValues(t, 2, afterTurn.Load())
					if name != "default-disabled-two-turns" {
						require.Equal(t, "resp_owner_1", capture.writes[1]["previous_response_id"], "native continuation must not strip and replay")
					}
				}
			case <-ctx.Done():
				t.Fatal("isolated WS owner fixture timed out")
			}
		})
	}
}

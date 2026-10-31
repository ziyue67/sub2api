//go:build integration

package repository_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestIssue594ReviewRealHTTPSelectionSlotsOutputAndUsage(t *testing.T) {
	f := newConnectionHTTPFixture(t)
	ctx := context.Background()
	redis := repository.Issue594TestRedis()
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, f.db)))
	user, err := client.User.Create().SetEmail(fmt.Sprintf("issue594-%d@example.invalid", f.accountID)).SetPasswordHash("synthetic-not-a-login").SetRole(service.RoleUser).SetStatus(service.StatusActive).SetBalance(1000).SetConcurrency(4).Save(ctx)
	require.NoError(t, err)
	group, err := client.Group.Create().SetName(fmt.Sprintf("issue594-group-%d", f.accountID)).SetPlatform(service.PlatformOpenAI).SetSubscriptionType(service.SubscriptionTypeStandard).SetStatus(service.StatusActive).SetRateMultiplier(1).Save(ctx)
	require.NoError(t, err)
	keyRepo := repository.NewAPIKeyRepository(client, f.db)
	key := &service.APIKey{UserID: user.ID, GroupID: &group.ID, Key: fmt.Sprintf("sk-issue594-synthetic-%d", f.accountID), Name: "review HTTP", Status: service.StatusActive, ConcurrencyLimit: 4}
	require.NoError(t, keyRepo.Create(ctx, key))
	_, err = f.db.Exec(`INSERT INTO account_groups(account_id,group_id) VALUES($1,$2)`, f.accountID, group.ID)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = f.db.Exec(`DELETE FROM usage_logs WHERE account_id=$1`, f.accountID)
		_, _ = f.db.Exec(`DELETE FROM accounts WHERE id=$1`, f.accountID)
		_, _ = f.db.Exec(`DELETE FROM api_keys WHERE id=$1`, key.ID)
		_, _ = f.db.Exec(`DELETE FROM groups WHERE id=$1`, group.ID)
		_, _ = f.db.Exec(`DELETE FROM users WHERE id=$1`, user.ID)
	})
	first := f.seedHistoricalAPIKey(t, "HTTP one", "synthetic-http-one")
	second := f.seedHistoricalAPIKey(t, "HTTP two", "synthetic-http-two")
	connectionResponse(t, f.request("PATCH", "/"+first.ID, `{"revision":1,"action":"select"}`))
	var mu sync.Mutex
	accepted := map[string]string{}
	active, maxActive := 0, 0
	hold := make(chan struct{})
	entered := make(chan struct{})
	var holdOnce sync.Once
	t.Cleanup(func() { holdOnce.Do(func() { close(hold) }) })
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, e := io.ReadAll(r.Body)
		require.NoError(t, e)
		marker := gjson.GetBytes(body, "input").String()
		expected := "Bearer synthetic-http-one"
		if strings.Contains(marker, "two") {
			expected = "Bearer synthetic-http-two"
		}
		require.Equal(t, expected, r.Header.Get("Authorization"), "selected UUID must map to its own token on the wire")
		require.Empty(t, r.Header.Get("Cookie"))
		require.Empty(t, r.Header.Get("x-codex-turn-state"))
		mu.Lock()
		accepted[marker] = expected
		active++
		if active > maxActive {
			maxActive = active
		}
		mu.Unlock()
		defer func() { mu.Lock(); active--; mu.Unlock() }()
		if marker == "hold-one" {
			close(entered)
			select {
			case <-hold:
			case <-r.Context().Done():
				return
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("x-request-id", "synthetic-"+marker)
		_, _ = fmt.Fprintf(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_%s\",\"status\":\"completed\",\"model\":\"gpt-5.4\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"confirmed-%s\"}]}],\"usage\":{\"input_tokens\":3,\"output_tokens\":1,\"total_tokens\":4}}}\n\n", marker, marker)
	}))
	defer upstream.Close()
	transport := &issue594OfflineInference{t: t, endpoint: strings.TrimPrefix(upstream.URL, "http://")}
	cfg := &config.Config{}
	cfg.Default.RateMultiplier = 1
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Gateway.MaxAccountSwitches = 1
	cfg.Gateway.Scheduling.FallbackWaitTimeout = 3 * time.Second
	cfg.Gateway.Scheduling.FallbackMaxWaiting = 2
	cfg.Gateway.Scheduling.StickySessionWaitTimeout = 3 * time.Second
	cfg.Gateway.Scheduling.StickySessionMaxWaiting = 2
	users := repository.NewUserRepository(client, f.db)
	groups := repository.NewGroupRepository(client, f.db)
	slots := repository.NewConcurrencyCache(redis, 1, 60)
	concurrency := service.NewConcurrencyService(slots)
	billing := service.NewBillingCacheService(repository.NewBillingCache(redis), users, nil, keyRepo, nil, nil, cfg, nil)
	t.Cleanup(billing.Stop)
	deferred := service.NewDeferredService(f.accounts, nil, time.Hour)
	prices := service.NewBillingService(cfg, nil)
	fixturePricing, pricingErr := prices.GetModelPricing("gpt-5.4")
	require.NoError(t, pricingErr)
	require.Greater(t, fixturePricing.InputPricePerToken, 0.0)
	require.Greater(t, fixturePricing.OutputPricePerToken, 0.0)
	gateway := service.NewOpenAIGatewayService(f.accounts, nil, repository.NewUsageLogRepository(client, f.db), repository.NewUsageBillingRepository(client, f.db), users, nil, nil, repository.NewGatewayCache(redis), cfg, nil, concurrency, prices, nil, billing, transport, deferred, nil, nil, nil, nil, nil, nil, nil, service.WithOpenAIAuthConnections(f.svc))
	t.Cleanup(gateway.StopOpenAICodexTicketHarvester)
	t.Cleanup(gateway.CloseOpenAIWSPool)
	keyService := service.NewAPIKeyService(keyRepo, users, groups, nil, nil, nil, cfg)
	h := handler.NewOpenAIGatewayHandler(gateway, concurrency, billing, keyService, nil, nil, nil, nil, cfg)
	router := gin.New()
	router.Use(gin.HandlerFunc(middleware.NewAPIKeyAuthMiddleware(keyService, nil, cfg)))
	router.POST("/v1/responses", h.Responses)
	request := func(marker string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(fmt.Sprintf(`{"model":"gpt-5.4","input":%q,"stream":false}`, marker)))
		req.Header.Set("Authorization", "Bearer "+key.Key)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Session-Id", marker)
		router.ServeHTTP(rec, req)
		return rec
	}
	one := request("first-one")
	require.Equal(t, 200, one.Code, one.Body.String())
	require.Contains(t, one.Body.String(), "confirmed-first-one")
	require.Eventually(t, func() bool {
		var n int
		_ = f.db.QueryRow(`SELECT count(*) FROM usage_logs WHERE account_id=$1`, f.accountID).Scan(&n)
		return n == 1
	}, 5*time.Second, 20*time.Millisecond)
	var assigned string
	require.NoError(t, f.db.QueryRow(`SELECT connection_id::text FROM openai_auth_connection_usage WHERE account_id=$1 AND api_key_id=$2`, f.accountID, key.ID).Scan(&assigned))
	require.Equal(t, first.ID, assigned)
	// A running request owns the parent slot. Selecting another connection does
	// not create a second allowance or an independent concurrency pool.
	held := make(chan *httptest.ResponseRecorder, 1)
	go func() { held <- request("hold-one") }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("real handler never reached synthetic upstream")
	}
	used, e := slots.GetAccountConcurrency(ctx, f.accountID)
	require.NoError(t, e)
	require.Equal(t, 1, used)
	connectionResponse(t, f.request("PATCH", "/"+second.ID, `{"revision":1,"action":"select"}`))
	parallel := make(chan *httptest.ResponseRecorder, 1)
	go func() { parallel <- request("parallel-two") }()
	select {
	case early := <-parallel:
		t.Fatalf("shared-slot waiter returned before release: status=%d body=%s", early.Code, early.Body.String())
	case <-time.After(150 * time.Millisecond):
	}
	mu.Lock()
	_, leaked := accepted["parallel-two"]
	require.False(t, leaked)
	mu.Unlock()
	holdOnce.Do(func() { close(hold) })
	completed := <-held
	require.Equal(t, 200, completed.Code, completed.Body.String())
	require.Contains(t, completed.Body.String(), "confirmed-hold-one")
	// Explicit bounded queue configuration requires semantic completion after release.
	select {
	case result := <-parallel:
		require.Equal(t, 200, result.Code, result.Body.String())
		require.Contains(t, result.Body.String(), "confirmed-parallel-two")
	case <-time.After(10 * time.Second):
		t.Fatal("parent-slot waiter did not finish")
	}
	two := request("second-two")
	require.Equal(t, 200, two.Code, two.Body.String())
	require.Contains(t, two.Body.String(), "confirmed-second-two")
	mu.Lock()
	require.Equal(t, 1, maxActive)
	mu.Unlock()
	var rows, attributed int
	require.NoError(t, f.db.QueryRow(`SELECT count(*) FROM usage_logs WHERE account_id=$1`, f.accountID).Scan(&rows))
	require.NoError(t, f.db.QueryRow(`SELECT count(*) FROM openai_auth_connection_usage WHERE account_id=$1 AND api_key_id=$2`, f.accountID, key.ID).Scan(&attributed))
	require.Equal(t, rows, attributed, "handler's real billing path must attribute every usage request")
	var balance float64
	require.NoError(t, f.db.QueryRow(`SELECT balance FROM users WHERE id=$1`, user.ID).Scan(&balance))
	require.Less(t, balance, 1000.0, "actual existing billing must execute")
	var settledCost float64
	require.NoError(t, f.db.QueryRow(`SELECT COALESCE(SUM(actual_cost),0) FROM usage_logs WHERE account_id=$1`, f.accountID).Scan(&settledCost))
	require.Greater(t, settledCost, 0.0)
	require.InDelta(t, settledCost, 1000-balance, 1e-9, "usage rows and actual balance settlement must agree")

	_, err = f.db.Exec(`UPDATE accounts SET rate_limited_at=NOW(),rate_limit_reset_at=NOW()+INTERVAL '5 minutes' WHERE id=$1`, f.accountID)
	require.NoError(t, err)
	denied := request("quota-two")
	require.NotEqual(t, 200, denied.Code)
	mu.Lock()
	_, sent := accepted["quota-two"]
	require.False(t, sent, "peer selection cannot bypass a shared account quota window")
	mu.Unlock()
	require.Eventually(t, func() bool { n, e := slots.GetAccountConcurrency(ctx, f.accountID); return e == nil && n == 0 }, 5*time.Second, 20*time.Millisecond)
}

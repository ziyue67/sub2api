//go:build integration

package repository_test

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Actual HTTP handler -> canonical service -> PostgreSQL; no mocked persistence.
func TestChannelOperationsHTTPPostgresWorkflow(t *testing.T) {
	db := repository.ChannelOperationsTestDatabase()
	repo := repository.NewChannelRepository(db)
	ch := &service.Channel{Name: fmt.Sprintf("HTTP-operations-%d", time.Now().UnixNano()), Status: service.StatusActive, BillingModelSource: service.BillingModelSourceRequested}
	require.NoError(t, repo.Create(context.Background(), ch))
	var groupID, accountID int64
	require.NoError(t, db.QueryRow(`INSERT INTO groups(name,platform) VALUES($1,'openai') RETURNING id`, ch.Name).Scan(&groupID))
	require.NoError(t, db.QueryRow(`INSERT INTO accounts(name,platform,type,credentials) VALUES('HTTP account','openai','apikey','{"api_key":"synthetic-fixture-only"}') RETURNING id`).Scan(&accountID))
	_, err := db.Exec(`INSERT INTO account_groups(account_id,group_id) VALUES($1,$2)`, accountID, groupID)
	require.NoError(t, err)
	require.NoError(t, repo.SetGroupIDs(context.Background(), ch.ID, []int64{groupID}))
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM accounts WHERE id=$1`, accountID)
		_, _ = db.Exec(`DELETE FROM channels WHERE id=$1`, ch.ID)
		_, _ = db.Exec(`DELETE FROM groups WHERE id=$1`, groupID)
	})
	h := admin.NewChannelHandler(service.NewChannelService(repo, nil, nil, nil, nil), nil, nil)
	router := gin.New()
	router.POST("/channels", h.Create)
	router.GET("/channels/:id", h.GetByID)
	router.PUT("/channels/:id", h.Update)
	router.DELETE("/channels/:id", h.Delete)
	router.POST("/channels/:id/operations", h.MutateOperations)
	router.GET("/channels/:id/operations", h.OperationsCatalog)
	router.GET("/snapshot", h.OperationsSnapshot)
	request := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, req)
		return w
	}
	base := fmt.Sprintf("/channels/%d", ch.ID)
	w := request("POST", base+"/operations", `{"action":"create_upstream","revision":0,"name":"HTTP Provider"}`)
	require.Equal(t, 200, w.Code, w.Body.String())
	upstream := func() int64 {
		catalog, err := repo.(service.ChannelOperationsRepository).ChannelOperationsCatalog(context.Background(), ch.ID, service.ChannelOperationsQuery{Page: 1, PageSize: 20, UpstreamPage: 1, Membership: "all"})
		require.NoError(t, err)
		return catalog.Upstreams[0].ID
	}()
	w = request("POST", base+"/operations", fmt.Sprintf(`{"action":"assign","revision":1,"upstream_id":%d,"account_id":%d}`, upstream, accountID))
	require.Equal(t, 200, w.Code, w.Body.String())
	w = request("GET", base+"/operations?page_size=1&membership=assigned", "")
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), `"revision":2`)
	require.NotContains(t, w.Body.String(), "synthetic-fixture-only")
	require.NotContains(t, w.Body.String(), "credentials")
	w = request("POST", base+"/operations", fmt.Sprintf(`{"action":"detach","revision":1,"expected_upstream_id":%d,"account_id":%d,"confirm":true}`, upstream, accountID))
	require.Equal(t, 409, w.Code)
	w = request("POST", base+"/operations", fmt.Sprintf(`{"action":"detach","revision":2,"expected_upstream_id":%d,"account_id":%d}`, upstream, accountID))
	require.Equal(t, 400, w.Code)
	w = request("POST", base+"/operations", fmt.Sprintf(`{"action":"detach","revision":2,"expected_upstream_id":%d,"account_id":%d,"confirm":true}`, upstream, accountID))
	require.Equal(t, 200, w.Code)
	w = request("PUT", base, `{"name":"HTTP renamed channel","description":"Basic edit preserves group bindings"}`)
	require.Equal(t, 200, w.Code, w.Body.String())
	channel, err := repo.GetByID(context.Background(), ch.ID)
	require.NoError(t, err)
	require.Equal(t, []int64{groupID}, channel.GroupIDs)
	w = request("GET", "/snapshot?channel_id="+fmt.Sprint(ch.ID)+"&from="+time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)+"&until="+time.Now().UTC().Format(time.RFC3339), "")
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), `"status":"ready"`)
	w = request("DELETE", base, "")
	require.Equal(t, 200, w.Code, w.Body.String())
	var n int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM accounts WHERE id=$1`, accountID).Scan(&n))
	require.Equal(t, 1, n)
	// The canonical create endpoint remains usable with conservative defaults.
	name := fmt.Sprintf("HTTP-created-%d", time.Now().UnixNano())
	w = request("POST", "/channels", fmt.Sprintf(`{"name":%q}`, name))
	require.Equal(t, 200, w.Code, w.Body.String())
	_, err = db.Exec(`DELETE FROM channels WHERE name=$1`, name)
	require.NoError(t, err)
}

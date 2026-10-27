package admin

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Embed the canonical repository contract; only operations and channel lookup
// are used here. PostgreSQL behavior is exercised by repository integration tests.
type channelOperationsHTTPRepository struct {
	service.ChannelRepository
	called   bool
	mutation service.ChannelOperationsMutation
	err      error
}

func (r *channelOperationsHTTPRepository) GetByID(context.Context, int64) (*service.Channel, error) {
	return &service.Channel{ID: 7}, nil
}
func (r *channelOperationsHTTPRepository) ChannelOperationsCatalog(context.Context, int64, service.ChannelOperationsQuery) (*service.ChannelOperationsCatalog, error) {
	r.called = true
	return &service.ChannelOperationsCatalog{}, nil
}
func (r *channelOperationsHTTPRepository) MutateChannelOperations(_ context.Context, _ int64, m service.ChannelOperationsMutation) (int64, error) {
	r.called = true
	r.mutation = m
	return 43, r.err
}
func (r *channelOperationsHTTPRepository) ReadChannelOperations(context.Context, service.ChannelOperationsFilter) *service.ChannelOperationsSnapshot {
	r.called = true
	return &service.ChannelOperationsSnapshot{}
}
func channelOperationsHTTP(t *testing.T, repo *channelOperationsHTTPRepository) *gin.Engine {
	t.Helper()
	router := gin.New()
	h := NewChannelHandler(service.NewChannelService(repo, nil, nil, nil, nil), nil, nil)
	router.GET("/channels/:id/operations", h.OperationsCatalog)
	router.POST("/channels/:id/operations", h.MutateOperations)
	router.GET("/snapshot", h.OperationsSnapshot)
	return router
}
func TestChannelOperationsHTTPValidationBeforeRepository(t *testing.T) {
	for _, tc := range []struct{ method, path, body string }{
		{"GET", "/channels/not-an-id/operations", ""}, {"GET", "/channels/7/operations?page=0", ""},
		{"GET", "/channels/7/operations?page_size=101", ""}, {"GET", "/channels/7/operations?membership=credentials", ""},
		{"POST", "/channels/7/operations", `{"action":"delete_upstream","revision":0,"upstream_id":2}`},
		{"POST", "/channels/7/operations", `{"action":"assign","revision":0,"upstream_id":2,"account_id":9,"expected_upstream_id":3}`},
		{"POST", "/channels/7/operations", `{"action":"create_upstream","name":"Provider"}`},
		{"POST", "/channels/7/operations", `{"action":"probe","revision":0}`},
		{"POST", "/channels/7/operations", `{"revision":`},
		{"GET", "/snapshot?from=bad&until=bad", ""},
		{"GET", "/snapshot?from=2026-01-01T00:00:00Z&until=2026-04-01T00:00:00Z", ""},
	} {
		t.Run(tc.path+tc.body, func(t *testing.T) {
			repo := &channelOperationsHTTPRepository{}
			router := channelOperationsHTTP(t, repo)
			w := httptest.NewRecorder()
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(w, req)
			require.Equal(t, 400, w.Code)
			require.False(t, repo.called)
		})
	}
}
func TestChannelOperationsHTTPConflictAndExplicitZeroRevision(t *testing.T) {
	repo := &channelOperationsHTTPRepository{err: service.ErrChannelOperationsConflict}
	router := channelOperationsHTTP(t, repo)
	request := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/channels/7/operations", strings.NewReader(`{"action":"detach","revision":0,"account_id":9,"expected_upstream_id":3,"confirm":true}`))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, req)
		return w
	}
	require.Equal(t, 409, request().Code)
	require.True(t, repo.called)
	require.NotNil(t, repo.mutation.Revision)
	require.Zero(t, *repo.mutation.Revision)
	repo.err = nil
	w := request()
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), `"revision":43`)
}

//go:build unit

package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type autoConfigHistoryRepoStub struct {
	service.AccountOpsRepository
	items  []service.AutoConfigEvent
	err    error
	before int64
	kind   string
	limit  int
}

func (r *autoConfigHistoryRepoStub) ListAutoConfigEvents(_ context.Context, before int64, kind string, limit int) ([]service.AutoConfigEvent, error) {
	r.before, r.kind, r.limit = before, kind, limit
	return r.items, r.err
}
func TestAutoConfigHistoryEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		query  string
		status int
	}{
		{"?limit=1&before=12&kind=concurrency_upgraded", http.StatusOK},
		{"?before=-1", http.StatusBadRequest}, {"?before=bad", http.StatusBadRequest},
		{"?before=9223372036854775808", http.StatusBadRequest},
		{"?limit=0", http.StatusBadRequest}, {"?limit=101", http.StatusBadRequest},
		{"?kind=raw_upstream_response", http.StatusBadRequest},
	} {
		t.Run(tc.query, func(t *testing.T) {
			repo := &autoConfigHistoryRepoStub{items: []service.AutoConfigEvent{{ID: 11}, {ID: 10}}}
			h := NewAccountOpsHandler(service.NewAccountOpsService(nil, repo, nil), nil)
			router := gin.New()
			router.GET("/events", h.ListAutoConfigEvents)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/events"+tc.query, nil))
			require.Equal(t, tc.status, w.Code)
			if tc.status == http.StatusOK {
				require.Equal(t, int64(12), repo.before)
				require.Equal(t, service.AutoConfigEventUpgrade, repo.kind)
				require.Equal(t, 2, repo.limit)
				var body struct {
					Data struct {
						Items   []service.AutoConfigEvent `json:"items"`
						HasMore bool                      `json:"has_more"`
					} `json:"data"`
				}
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
				require.Len(t, body.Data.Items, 1)
				require.True(t, body.Data.HasMore)
			} else {
				require.Zero(t, repo.limit, "invalid queries must not access storage")
			}
		})
	}
}
func TestAutoConfigHistoryEndpointSanitizesStorageError(t *testing.T) {
	repo := &autoConfigHistoryRepoStub{err: errors.New("private connection details")}
	h := NewAccountOpsHandler(service.NewAccountOpsService(nil, repo, nil), nil)
	router := gin.New()
	router.GET("/events", h.ListAutoConfigEvents)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/events", nil))
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	require.NotContains(t, w.Body.String(), "private connection details")
}

package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type pelicanHistoryCall struct {
	beforeID  int64
	limit     int
	accountID int64
}

type pelicanHistoryHandlerResults struct {
	service.ScheduledTestResultRepository
	calls []pelicanHistoryCall
}

func (r *pelicanHistoryHandlerResults) ListPelicanHistory(_ context.Context, beforeID int64, limit int, accountID int64) ([]*service.PelicanHistoryResult, error) {
	r.calls = append(r.calls, pelicanHistoryCall{beforeID: beforeID, limit: limit, accountID: accountID})
	return []*service.PelicanHistoryResult{{AccountID: accountID, AccountName: "Pelican account"}}, nil
}

func TestPelicanHistoryHandlerAccountFilter(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name   string
		target string
		status int
		call   *pelicanHistoryCall
	}{
		{name: "all accounts by default", target: "/pelican-test-results", status: http.StatusOK, call: &pelicanHistoryCall{limit: 101}},
		{name: "one account with cursor", target: "/pelican-test-results?account_id=42&before_id=9", status: http.StatusOK, call: &pelicanHistoryCall{beforeID: 9, limit: 101, accountID: 42}},
		{name: "negative account", target: "/pelican-test-results?account_id=-1", status: http.StatusBadRequest},
		{name: "non-numeric account", target: "/pelican-test-results?account_id=abc", status: http.StatusBadRequest},
		{name: "empty account", target: "/pelican-test-results?account_id=", status: http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			results := &pelicanHistoryHandlerResults{}
			h := NewScheduledTestHandler(service.NewScheduledTestService(nil, results))
			r := gin.New()
			r.GET("/pelican-test-results", h.ListPelicanHistory)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, tc.target, nil))
			require.Equal(t, tc.status, w.Code, w.Body.String())
			if tc.call == nil {
				require.Empty(t, results.calls, "rejected filters must not query history")
				return
			}
			require.Equal(t, []pelicanHistoryCall{*tc.call}, results.calls)
			var page service.PelicanHistoryPage
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &page))
			require.Len(t, page.Items, 1)
			require.Equal(t, tc.call.accountID, page.Items[0].AccountID)
		})
	}
}

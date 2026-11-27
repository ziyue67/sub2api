package admin

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/requesttiming"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type issue605ContentRepo struct {
	service.UsageLogRepository
	ids []int64
}

func (r *issue605ContentRepo) ListWithFilters(context.Context, pagination.PaginationParams, usagestats.UsageLogFilters) ([]service.UsageLog, *pagination.PaginationResult, error) {
	return []service.UsageLog{{ID: 605, Model: "fixture"}}, &pagination.PaginationResult{Total: 1}, nil
}
func (r *issue605ContentRepo) ReadUsageContentTimings(_ context.Context, ids []int64) (map[int64]requesttiming.ContentObservation, error) {
	r.ids = ids
	v := requesttiming.UnknownContentObservation()
	value := 12.0
	v.ReasoningFirstMS = &value
	return map[int64]requesttiming.ContentObservation{605: v}, nil
}

func TestIssue605ActualAdminUsageListContentDTO(t *testing.T) {
	r := &issue605ContentRepo{}
	h := NewUsageHandler(service.NewUsageService(r, nil, nil, nil), nil, nil, nil)
	engine := gin.New()
	engine.GET("/usage", h.List)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest("GET", "/usage", nil))
	require.Equal(t, 200, w.Code)
	require.Equal(t, []int64{605}, r.ids)
	require.Contains(t, w.Body.String(), `"reasoning_first_ms":12`)
	require.Contains(t, w.Body.String(), `"visible_text_first_ms":null`)
	require.Contains(t, w.Body.String(), `"reasoning_streaming_capability":"unknown"`)
	require.Contains(t, w.Body.String(), `"client_socket"`)
	require.Contains(t, w.Body.String(), `"min_rtt_ms":null`)
}

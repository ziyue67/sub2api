package admin

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type ownershipHandlerRepo struct {
	reusePlanRepo
	calls int
}

func (r *ownershipHandlerRepo) QualityOwnership(_ context.Context, id int64, request *service.QualityOwnershipRequest) (*service.QualityOwnershipPreview, error) {
	r.calls++
	if request != nil {
		return nil, infraerrors.Conflict("QUALITY_OWNERSHIP_PREVIEW_CHANGED", "preview again")
	}
	return &service.QualityOwnershipPreview{PlanID: id, Entries: []service.QualityOwnershipEntry{}}, nil
}
func TestQualityOwnershipRequiresExplicitConfirmationAndReturnsFencedConflict(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &ownershipHandlerRepo{}
	h := NewScheduledTestHandler(service.NewScheduledTestService(repo, nil))
	router := gin.New()
	router.GET("/plans/:id", h.QualityOwnershipPreview)
	router.POST("/plans/:id", h.QualityOwnershipReconcile)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", "/plans/7", nil))
	require.Equal(t, 200, w.Code)
	require.Equal(t, 1, repo.calls)
	for _, body := range []string{`{}`, `{"confirm":false,"fence":"` + strings.Repeat("a", 64) + `"}`} {
		req := httptest.NewRequest("POST", "/plans/7", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		router.ServeHTTP(w, req)
		require.Equal(t, 400, w.Code)
		require.Equal(t, 1, repo.calls)
	}
	req := httptest.NewRequest("POST", "/plans/7", strings.NewReader(`{"confirm":true,"fence":"`+strings.Repeat("a", 64)+`"}`))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, 409, w.Code)
	require.Contains(t, w.Body.String(), "QUALITY_OWNERSHIP_PREVIEW_CHANGED")
}

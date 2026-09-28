package admin

import (
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// PelicanGroupTestHandler manages the Pelican group tests that feed the user showcase.
type PelicanGroupTestHandler struct {
	svc *service.PelicanGroupTestService
}

func NewPelicanGroupTestHandler(svc *service.PelicanGroupTestService) *PelicanGroupTestHandler {
	return &PelicanGroupTestHandler{svc: svc}
}

// ListPlans GET /api/v1/admin/pelican-group-tests
func (h *PelicanGroupTestHandler) ListPlans(c *gin.Context) {
	plans, err := h.svc.ListPlans(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, plans)
}

// CreatePlan POST /api/v1/admin/pelican-group-tests
func (h *PelicanGroupTestHandler) CreatePlan(c *gin.Context) {
	var input service.PelicanGroupTestPlanInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "invalid request body")
		return
	}
	plan, err := h.svc.CreatePlan(c.Request.Context(), input)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, plan)
}

// UpdatePlan PUT /api/v1/admin/pelican-group-tests/:id
// The group of an existing plan cannot change; group_id in the body is ignored.
func (h *PelicanGroupTestHandler) UpdatePlan(c *gin.Context) {
	id, ok := pelicanGroupTestID(c, "id")
	if !ok {
		return
	}
	var input service.PelicanGroupTestPlanInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "invalid request body")
		return
	}
	plan, err := h.svc.UpdatePlan(c.Request.Context(), id, input)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, plan)
}

// DeletePlan DELETE /api/v1/admin/pelican-group-tests/:id
// The group leaves the showcase; its snapshots are removed by the next cleanup round.
func (h *PelicanGroupTestHandler) DeletePlan(c *gin.Context) {
	id, ok := pelicanGroupTestID(c, "id")
	if !ok {
		return
	}
	if err := h.svc.DeletePlan(c.Request.Context(), id); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"deleted": true})
}

// RunPlan POST /api/v1/admin/pelican-group-tests/:id/run
// Starts a run in the background and returns at once; 409 while a run is in progress.
func (h *PelicanGroupTestHandler) RunPlan(c *gin.Context) {
	id, ok := pelicanGroupTestID(c, "id")
	if !ok {
		return
	}
	if err := h.svc.RunNow(c.Request.Context(), id); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"started": true})
}

// ListResults GET /api/v1/admin/pelican-group-test-results?plan_id=&page=&page_size=
// Newest first, without HTML; plan_id 0 or missing lists every plan.
func (h *PelicanGroupTestHandler) ListResults(c *gin.Context) {
	var planID int64
	if raw := c.Query("plan_id"); raw != "" {
		var err error
		planID, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || planID < 0 {
			response.BadRequest(c, "invalid plan_id")
			return
		}
	}
	page, pageSize := response.ParsePagination(c)
	items, total, err := h.svc.ListResults(c.Request.Context(), planID, page, pageSize)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Paginated(c, items, total, page, pageSize)
}

// GetResult GET /api/v1/admin/pelican-group-test-results/:id
// Returns the raw model output; the frontend renders it in a sandboxed iframe.
func (h *PelicanGroupTestHandler) GetResult(c *gin.Context) {
	id, ok := pelicanGroupTestID(c, "id")
	if !ok {
		return
	}
	result, err := h.svc.GetResult(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

func pelicanGroupTestID(c *gin.Context, param string) (int64, bool) {
	id, err := strconv.ParseInt(c.Param(param), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "invalid id")
		return 0, false
	}
	return id, true
}

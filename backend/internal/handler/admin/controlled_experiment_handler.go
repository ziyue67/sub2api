package admin

import (
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type ControlledExperimentHandler struct {
	svc *service.ControlledExperimentService
}

func NewControlledExperimentHandler(svc *service.ControlledExperimentService) *ControlledExperimentHandler {
	return &ControlledExperimentHandler{svc: svc}
}

// Experiments expose cross-account evidence and generate paid requests, so the
// observer role does not receive this admin-only surface.
func (h *ControlledExperimentHandler) FullAdmin(c *gin.Context) {
	if _, scoped := service.ObserverGroupIDs(c.Request.Context()); scoped {
		response.ErrorFrom(c, service.ErrObserverScope)
		c.Abort()
		return
	}
	c.Next()
}

func (h *ControlledExperimentHandler) Catalog(c *gin.Context) {
	tasks, err := service.ControlledExperimentTasks()
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	items := make([]gin.H, 0, len(tasks))
	for _, task := range tasks {
		items = append(items, gin.H{"id": task.ID, "split": task.Split, "family": task.Family, "category": task.Category, "prompt": task.Prompt, "max_turns": task.MaxTurns, "grader_kind": task.Grader.Kind, "sql_cases": len(task.Grader.Cases)})
	}
	response.Success(c, gin.H{"version": service.ControlledSuiteVersion, "seed": 20261007, "tasks": items})
}

func (h *ControlledExperimentHandler) Create(c *gin.Context) {
	var input service.ControlledExperimentInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "invalid experiment body")
		return
	}
	run, err := h.svc.Create(c.Request.Context(), input)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, run)
}

func (h *ControlledExperimentHandler) List(c *gin.Context) {
	before, err := strconv.ParseInt(c.DefaultQuery("before_id", "0"), 10, 64)
	if err != nil || before < 0 {
		response.BadRequest(c, "invalid before_id")
		return
	}
	items, err := h.svc.List(c.Request.Context(), before)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, items)
}

func controlledExperimentID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "invalid experiment ID")
		return 0, false
	}
	return id, true
}

func (h *ControlledExperimentHandler) Report(c *gin.Context) {
	id, ok := controlledExperimentID(c)
	if !ok {
		return
	}
	report, err := h.svc.Report(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, report)
}

func (h *ControlledExperimentHandler) Start(c *gin.Context) {
	id, ok := controlledExperimentID(c)
	if !ok {
		return
	}
	if err := h.svc.Start(c.Request.Context(), id); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"started": true})
}

func (h *ControlledExperimentHandler) Stop(c *gin.Context) {
	id, ok := controlledExperimentID(c)
	if !ok {
		return
	}
	if err := h.svc.RequestStop(c.Request.Context(), id); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"stop_requested": true})
}

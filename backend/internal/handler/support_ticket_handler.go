package handler

import (
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// SupportTicketHandler serves support tickets ("网站工单") to users and admins.
// Every service call checks the feature switch, so disabled tickets answer
// SUPPORT_TICKET_DISABLED everywhere.
type SupportTicketHandler struct {
	service *service.SupportTicketService
}

func NewSupportTicketHandler(svc *service.SupportTicketService) *SupportTicketHandler {
	return &SupportTicketHandler{service: svc}
}

type supportTicketReplyRequest struct {
	Body string `json:"body"`
	// Status after an admin reply: replied (default), processing or closed.
	Status string `json:"status"`
}

type supportTicketStatusRequest struct {
	Status string `json:"status"`
}

func supportTicketCaller(c *gin.Context) (int64, bool) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "user not found in context")
		return 0, false
	}
	return subject.UserID, true
}

func supportTicketID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "invalid ticket id")
		return 0, false
	}
	return id, true
}

func supportTicketRespond(c *gin.Context, data any, err error) {
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, data)
}

// ---- users ----

func (h *SupportTicketHandler) Summary(c *gin.Context) {
	userID, ok := supportTicketCaller(c)
	if !ok {
		return
	}
	summary, err := h.service.UserSummary(c.Request.Context(), userID)
	supportTicketRespond(c, summary, err)
}

func (h *SupportTicketHandler) List(c *gin.Context) {
	userID, ok := supportTicketCaller(c)
	if !ok {
		return
	}
	page, pageSize := response.ParsePagination(c)
	result, err := h.service.ListForUser(c.Request.Context(), userID, c.Query("status"), page, pageSize)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Paginated(c, result.Items, result.Total, result.Page, result.PageSize)
}

func (h *SupportTicketHandler) Create(c *gin.Context) {
	userID, ok := supportTicketCaller(c)
	if !ok {
		return
	}
	var req service.SupportTicketCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request body")
		return
	}
	detail, err := h.service.Create(c.Request.Context(), userID, req)
	supportTicketRespond(c, detail, err)
}

func (h *SupportTicketHandler) Get(c *gin.Context) {
	userID, ok := supportTicketCaller(c)
	if !ok {
		return
	}
	id, ok := supportTicketID(c)
	if !ok {
		return
	}
	detail, err := h.service.GetForUser(c.Request.Context(), userID, id)
	supportTicketRespond(c, detail, err)
}

func (h *SupportTicketHandler) Reply(c *gin.Context) {
	userID, ok := supportTicketCaller(c)
	if !ok {
		return
	}
	id, ok := supportTicketID(c)
	if !ok {
		return
	}
	var req supportTicketReplyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request body")
		return
	}
	detail, err := h.service.ReplyAsUser(c.Request.Context(), userID, id, req.Body)
	supportTicketRespond(c, detail, err)
}

func (h *SupportTicketHandler) Close(c *gin.Context) {
	userID, ok := supportTicketCaller(c)
	if !ok {
		return
	}
	id, ok := supportTicketID(c)
	if !ok {
		return
	}
	detail, err := h.service.CloseAsUser(c.Request.Context(), userID, id)
	supportTicketRespond(c, detail, err)
}

func (h *SupportTicketHandler) Reopen(c *gin.Context) {
	userID, ok := supportTicketCaller(c)
	if !ok {
		return
	}
	id, ok := supportTicketID(c)
	if !ok {
		return
	}
	detail, err := h.service.ReopenAsUser(c.Request.Context(), userID, id)
	supportTicketRespond(c, detail, err)
}

// ---- admins ----

func (h *SupportTicketHandler) AdminSummary(c *gin.Context) {
	summary, err := h.service.AdminSummary(c.Request.Context())
	supportTicketRespond(c, summary, err)
}

func (h *SupportTicketHandler) AdminList(c *gin.Context) {
	page, pageSize := response.ParsePagination(c)
	result, err := h.service.ListForAdmin(c.Request.Context(), service.SupportTicketListFilter{
		Status: c.Query("status"), Category: c.Query("category"), Keyword: c.Query("keyword"), Page: page, PageSize: pageSize,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Paginated(c, result.Items, result.Total, result.Page, result.PageSize)
}

func (h *SupportTicketHandler) AdminGet(c *gin.Context) {
	id, ok := supportTicketID(c)
	if !ok {
		return
	}
	detail, err := h.service.GetForAdmin(c.Request.Context(), id)
	supportTicketRespond(c, detail, err)
}

func (h *SupportTicketHandler) AdminReply(c *gin.Context) {
	adminID, ok := supportTicketCaller(c)
	if !ok {
		return
	}
	id, ok := supportTicketID(c)
	if !ok {
		return
	}
	var req supportTicketReplyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request body")
		return
	}
	detail, err := h.service.ReplyAsAdmin(c.Request.Context(), adminID, id, req.Body, req.Status)
	supportTicketRespond(c, detail, err)
}

func (h *SupportTicketHandler) AdminSetStatus(c *gin.Context) {
	id, ok := supportTicketID(c)
	if !ok {
		return
	}
	var req supportTicketStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request body")
		return
	}
	detail, err := h.service.SetStatusAsAdmin(c.Request.Context(), id, req.Status)
	supportTicketRespond(c, detail, err)
}

func (h *SupportTicketHandler) AdminDelete(c *gin.Context) {
	id, ok := supportTicketID(c)
	if !ok {
		return
	}
	if err := h.service.Delete(c.Request.Context(), id); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"deleted": true})
}

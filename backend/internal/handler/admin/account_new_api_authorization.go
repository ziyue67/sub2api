package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"net/http"
	"strconv"
)

func (h *AccountHandler) newAPIConfigID(c *gin.Context) (int64, bool) {
	id, e := strconv.ParseInt(c.Param("id"), 10, 64)
	if e != nil || id <= 0 {
		response.BadRequest(c, "Invalid account ID")
		return 0, false
	}
	if h.upstreamBillingProbe == nil {
		response.ErrorFrom(c, service.ErrUpstreamBillingProbeUnavailable)
		return 0, false
	}
	return id, true
}
func (h *AccountHandler) GetNewAPIConfig(c *gin.Context) {
	id, ok := h.newAPIConfigID(c)
	if !ok {
		return
	}
	out, e := h.upstreamBillingProbe.GetNewAPIConfig(c.Request.Context(), id)
	if e != nil {
		response.ErrorFrom(c, e)
		return
	}
	response.Success(c, out)
}
func (h *AccountHandler) newAPIConfigWrite(c *gin.Context, save bool) {
	id, ok := h.newAPIConfigID(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64*1024)
	var req service.NewAPIConfigRequest
	if e := c.ShouldBindJSON(&req); e != nil {
		response.BadRequest(c, "Invalid New API configuration request")
		return
	}
	var out *service.NewAPIPreview
	var e error
	if save {
		out, e = h.upstreamBillingProbe.SaveNewAPIConfig(c.Request.Context(), id, req)
	} else {
		out, e = h.upstreamBillingProbe.PreviewNewAPIConfig(c.Request.Context(), id, req)
	}
	if e != nil {
		response.ErrorFrom(c, e)
		return
	}
	response.Success(c, out)
}
func (h *AccountHandler) PreviewNewAPIConfig(c *gin.Context) { h.newAPIConfigWrite(c, false) }
func (h *AccountHandler) SaveNewAPIConfig(c *gin.Context)    { h.newAPIConfigWrite(c, true) }
func (h *AccountHandler) DeleteNewAPIConfig(c *gin.Context) {
	id, ok := h.newAPIConfigID(c)
	if !ok {
		return
	}
	if e := h.upstreamBillingProbe.DeleteNewAPIConfig(c.Request.Context(), id); e != nil {
		response.ErrorFrom(c, e)
		return
	}
	response.Success(c, gin.H{"account_id": id, "configured": false})
}

package admin

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/serverless"
	"github.com/gin-gonic/gin"
)

func (h *SettingHandler) GetServerless(c *gin.Context) {
	m := h.settingService.Serverless
	if m == nil {
		response.Error(c, 503, "Serverless manager unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()
	cfg, err := m.Config(ctx)
	if err != nil {
		response.Error(c, 503, "Routing configuration unavailable")
		return
	}
	pods, err := m.Pods(ctx)
	if err != nil {
		response.Error(c, 503, "Pod registry unavailable")
		return
	}
	stats, err := m.Stats(ctx)
	if err != nil {
		response.Error(c, 503, "Routing statistics unavailable")
		return
	}
	response.Success(c, gin.H{"config": cfg, "pods": pods, "stats": stats})
}
func (h *SettingHandler) SaveServerless(c *gin.Context) {
	if h.settingService.Serverless == nil {
		response.Error(c, 503, "Serverless manager unavailable")
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8<<20)
	var cfg serverless.Config
	if c.ShouldBindJSON(&cfg) != nil {
		response.BadRequest(c, "Invalid Serverless configuration")
		return
	}
	if err := serverless.Validate(cfg); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()
	if err := h.settingService.Serverless.Save(ctx, cfg); err != nil {
		response.Error(c, 503, "Could not save configuration; check cluster secret and Redis")
		return
	}
	response.Success(c, cfg)
}

func (h *SettingHandler) ProbeServerless(c *gin.Context) {
	m := h.settingService.Serverless
	if m == nil {
		response.Error(c, 503, "Serverless manager unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	cfg, err := m.Config(ctx)
	if err != nil {
		response.Error(c, 503, "Configuration unavailable")
		return
	}
	pods, err := m.Pods(ctx)
	if err != nil {
		response.Error(c, 503, "Pod registry unavailable")
		return
	}
	for _, p := range pods {
		if p.ID == c.Param("id") {
			approved := false
			for _, policy := range cfg.Pods {
				if policy.ID == p.ID && strings.TrimRight(policy.Endpoint, "/") == p.Endpoint {
					approved = true
				}
			}
			if !approved {
				response.BadRequest(c, "Approve this Pod endpoint before probing")
				return
			}
			response.Success(c, gin.H{"ready": m.Probe(ctx, p)})
			return
		}
	}
	response.Error(c, 404, "Pod not found")
}

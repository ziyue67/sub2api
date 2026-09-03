package admin

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// AccountProxyLaneRequest is the public admin payload for one account egress.
// Pointer fields on PUT make PATCH-like updates possible without adding a
// second endpoint; POST applies safe defaults (active, schedulable, weight 1).
type AccountProxyLaneRequest struct {
	Name          *string    `json:"name"`
	ProxyID       *int64     `json:"proxy_id"`
	Transport     *string    `json:"transport"`
	Concurrency   *int       `json:"concurrency"`
	Weight        *int       `json:"weight"`
	Priority      *int       `json:"priority"`
	Status        *string    `json:"status"`
	Schedulable   *bool      `json:"schedulable"`
	CooldownUntil *time.Time `json:"cooldown_until"`
}

type accountProxyLaneResponse struct {
	ID                 int64                      `json:"id"`
	AccountID          int64                      `json:"account_id"`
	ProxyID            *int64                     `json:"proxy_id,omitempty"`
	Name               string                     `json:"name"`
	Transport          string                     `json:"transport"`
	Concurrency        int                        `json:"concurrency"`
	Weight             int                        `json:"weight"`
	Priority           int                        `json:"priority"`
	Status             string                     `json:"status"`
	Schedulable        bool                       `json:"schedulable"`
	CooldownUntil      *time.Time                 `json:"cooldown_until,omitempty"`
	CreatedAt          time.Time                  `json:"created_at"`
	UpdatedAt          time.Time                  `json:"updated_at"`
	Proxy              *accountProxyLaneProxyView `json:"proxy,omitempty"`
	CurrentConcurrency int                        `json:"current_concurrency"`
}

// accountProxyLaneProxyView intentionally excludes proxy credentials.  Host
// and port are retained because they are useful when selecting a lane in the
// admin UI, but username/password never leave the server.
type accountProxyLaneProxyView struct {
	ID        int64      `json:"id"`
	Name      string     `json:"name"`
	Protocol  string     `json:"protocol"`
	Host      string     `json:"host"`
	Port      int        `json:"port"`
	Status    string     `json:"status"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

func accountProxyLaneResponseFromService(lane service.AccountProxyLane, currentConcurrency int) accountProxyLaneResponse {
	out := accountProxyLaneResponse{
		ID:                 lane.ID,
		AccountID:          lane.AccountID,
		ProxyID:            lane.ProxyID,
		Name:               lane.Name,
		Transport:          lane.Transport,
		Concurrency:        lane.Concurrency,
		Weight:             lane.Weight,
		Priority:           lane.Priority,
		Status:             lane.Status,
		Schedulable:        lane.Schedulable,
		CooldownUntil:      lane.CooldownUntil,
		CreatedAt:          lane.CreatedAt,
		UpdatedAt:          lane.UpdatedAt,
		CurrentConcurrency: currentConcurrency,
	}
	if lane.Proxy != nil {
		out.Proxy = &accountProxyLaneProxyView{
			ID:        lane.Proxy.ID,
			Name:      lane.Proxy.Name,
			Protocol:  lane.Proxy.Protocol,
			Host:      lane.Proxy.Host,
			Port:      lane.Proxy.Port,
			Status:    lane.Proxy.Status,
			ExpiresAt: lane.Proxy.ExpiresAt,
		}
	}
	return out
}

func parseAccountProxyLaneIDs(c *gin.Context) (int64, int64, bool) {
	accountID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || accountID <= 0 {
		response.ErrorFrom(c, infraerrors.BadRequest("INVALID_ACCOUNT_ID", "invalid account id"))
		return 0, 0, false
	}
	laneID := int64(0)
	if raw := strings.TrimSpace(c.Param("lane_id")); raw != "" {
		laneID, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || laneID <= 0 {
			response.ErrorFrom(c, infraerrors.BadRequest("INVALID_ACCOUNT_PROXY_LANE_ID", "invalid lane id"))
			return 0, 0, false
		}
	}
	return accountID, laneID, true
}

func accountProxyLaneAdminCapability(c *gin.Context, adminService service.AdminService) (service.AccountProxyLaneAdminService, bool) {
	capability, ok := adminService.(service.AccountProxyLaneAdminService)
	if !ok || capability == nil {
		response.ErrorFrom(c, service.ErrAccountProxyLanesUnavailable)
		return nil, false
	}
	return capability, true
}

// ListProxyLanes handles GET /api/v1/admin/accounts/:id/proxy-lanes.
func (h *AccountHandler) ListProxyLanes(c *gin.Context) {
	accountID, _, ok := parseAccountProxyLaneIDs(c)
	if !ok {
		return
	}
	capability, ok := accountProxyLaneAdminCapability(c, h.adminService)
	if !ok {
		return
	}
	lanes, err := capability.ListAccountProxyLanes(c.Request.Context(), accountID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	currentByLane := map[int64]int{}
	if h.concurrencyService != nil {
		laneIDs := make([]int64, 0, len(lanes))
		for _, lane := range lanes {
			if lane.ID > 0 {
				laneIDs = append(laneIDs, lane.ID)
			}
		}
		if counts, countErr := h.concurrencyService.GetLaneConcurrencyBatch(c.Request.Context(), laneIDs); countErr == nil {
			currentByLane = counts
		}
	}
	items := make([]accountProxyLaneResponse, 0, len(lanes))
	for _, lane := range lanes {
		items = append(items, accountProxyLaneResponseFromService(lane, currentByLane[lane.ID]))
	}
	response.Success(c, items)
}

// CreateProxyLane handles POST /api/v1/admin/accounts/:id/proxy-lanes.
func (h *AccountHandler) CreateProxyLane(c *gin.Context) {
	accountID, _, ok := parseAccountProxyLaneIDs(c)
	if !ok {
		return
	}
	capability, ok := accountProxyLaneAdminCapability(c, h.adminService)
	if !ok {
		return
	}
	var req AccountProxyLaneRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.ErrorFrom(c, infraerrors.BadRequest("INVALID_ACCOUNT_PROXY_LANE", "invalid lane payload: "+err.Error()))
		return
	}
	lane, err := laneFromCreateRequest(accountID, req)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	created, err := capability.CreateAccountProxyLane(c.Request.Context(), accountID, lane)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Created(c, accountProxyLaneResponseFromService(*created, 0))
}

// UpdateProxyLane handles PUT /api/v1/admin/accounts/:id/proxy-lanes/:lane_id.
func (h *AccountHandler) UpdateProxyLane(c *gin.Context) {
	accountID, laneID, ok := parseAccountProxyLaneIDs(c)
	if !ok || laneID <= 0 {
		return
	}
	capability, ok := accountProxyLaneAdminCapability(c, h.adminService)
	if !ok {
		return
	}
	var req AccountProxyLaneRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.ErrorFrom(c, infraerrors.BadRequest("INVALID_ACCOUNT_PROXY_LANE", "invalid lane payload: "+err.Error()))
		return
	}
	// Merge against the persisted lane so omitted PUT fields retain their
	// current value.  This also guarantees the account_id/lane_id pair is
	// checked by the service before the write.
	lanes, err := capability.ListAccountProxyLanes(c.Request.Context(), accountID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	var current *service.AccountProxyLane
	for i := range lanes {
		if lanes[i].ID == laneID {
			candidate := lanes[i]
			current = &candidate
			break
		}
	}
	if current == nil {
		response.ErrorFrom(c, service.ErrAccountProxyLaneNotFound)
		return
	}
	mergeLaneRequest(current, req)
	updated, err := capability.UpdateAccountProxyLane(c.Request.Context(), accountID, laneID, current)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, accountProxyLaneResponseFromService(*updated, 0))
}

// DeleteProxyLane handles DELETE /api/v1/admin/accounts/:id/proxy-lanes/:lane_id.
func (h *AccountHandler) DeleteProxyLane(c *gin.Context) {
	accountID, laneID, ok := parseAccountProxyLaneIDs(c)
	if !ok || laneID <= 0 {
		return
	}
	capability, ok := accountProxyLaneAdminCapability(c, h.adminService)
	if !ok {
		return
	}
	if err := capability.DeleteAccountProxyLane(c.Request.Context(), accountID, laneID); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func laneFromCreateRequest(accountID int64, req AccountProxyLaneRequest) (*service.AccountProxyLane, error) {
	if req.Name == nil || strings.TrimSpace(*req.Name) == "" {
		return nil, infraerrors.BadRequest("INVALID_ACCOUNT_PROXY_LANE", "name is required")
	}
	transport := service.AccountProxyLaneTransportProxy
	if req.Transport != nil && strings.TrimSpace(*req.Transport) != "" {
		transport = strings.TrimSpace(*req.Transport)
	}
	concurrency, weight, priority := 1, 1, 50
	if req.Concurrency != nil {
		concurrency = *req.Concurrency
	}
	if req.Weight != nil {
		weight = *req.Weight
	}
	if req.Priority != nil {
		priority = *req.Priority
	}
	status := service.AccountProxyLaneStatusActive
	if req.Status != nil && strings.TrimSpace(*req.Status) != "" {
		status = strings.TrimSpace(*req.Status)
	}
	schedulable := true
	if req.Schedulable != nil {
		schedulable = *req.Schedulable
	}
	return &service.AccountProxyLane{
		AccountID:     accountID,
		ProxyID:       req.ProxyID,
		Name:          *req.Name,
		Transport:     transport,
		Concurrency:   concurrency,
		Weight:        weight,
		Priority:      priority,
		Status:        status,
		Schedulable:   schedulable,
		CooldownUntil: req.CooldownUntil,
	}, nil
}

func mergeLaneRequest(lane *service.AccountProxyLane, req AccountProxyLaneRequest) {
	if lane == nil {
		return
	}
	if req.Name != nil {
		lane.Name = *req.Name
	}
	if req.ProxyID != nil {
		lane.ProxyID = req.ProxyID
	}
	if req.Transport != nil {
		lane.Transport = *req.Transport
	}
	if req.Concurrency != nil {
		lane.Concurrency = *req.Concurrency
	}
	if req.Weight != nil {
		lane.Weight = *req.Weight
	}
	if req.Priority != nil {
		lane.Priority = *req.Priority
	}
	if req.Status != nil {
		lane.Status = *req.Status
	}
	if req.Schedulable != nil {
		lane.Schedulable = *req.Schedulable
	}
	if req.CooldownUntil != nil {
		lane.CooldownUntil = req.CooldownUntil
	}
	// Switching to direct explicitly clears any previous proxy relation. A
	// pointer cannot distinguish JSON null from omission, so transport is the
	// authoritative signal for this invariant.
	if strings.EqualFold(strings.TrimSpace(lane.Transport), service.AccountProxyLaneTransportDirect) {
		lane.ProxyID = nil
	}
}

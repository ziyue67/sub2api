package admin

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// Only inspect reference fields, never credential/extra payloads. JSON decoding
// uses the same case-insensitive field matching as the endpoint request types.
type observerAccountReferences struct {
	AccountID          *int64                      `json:"account_id"`
	AccountIDs         []int64                     `json:"account_ids"`
	ProxyID            *int64                      `json:"proxy_id"`
	GroupIDs           *[]int64                    `json:"group_ids"`
	GroupAllowedModels map[int64][]string          `json:"group_allowed_models"`
	Accounts           []observerAccountReferences `json:"accounts"`
	Defaults           *observerAccountReferences  `json:"defaults"`
}

// AuthorizeObserver runs before any handler, idempotency lookup or mutation.
// Mixed authorized/unauthorized batches fail as a whole, including read batches.
func (h *AccountHandler) AuthorizeObserver(c *gin.Context) {
	ctx := c.Request.Context()
	if _, scoped := service.ObserverGroupIDs(ctx); !scoped {
		c.Next()
		return
	}
	if !middleware.ObserverAccountRouteAllowed(c.Request.Method, c.FullPath()) {
		response.ErrorFrom(c, service.ErrObserverScope)
		c.Abort()
		return
	}
	ids, err := parseAccountIDs(c)
	if err != nil {
		response.BadRequest(c, "Invalid account IDs")
		c.Abort()
		return
	}
	if raw := c.Param("id"); raw != "" && !strings.HasPrefix(c.FullPath(), "/api/v1/admin/scheduled-test-plans") {
		id, parseErr := strconv.ParseInt(raw, 10, 64)
		if parseErr != nil || id <= 0 {
			response.BadRequest(c, "Invalid account ID")
			c.Abort()
			return
		}
		ids = append(ids, id)
	}
	if c.Request.Body != nil && c.Request.Method != http.MethodGet {
		// Bound the preflight copy independently of the gateway's model input limit.
		const maxBody = 32 << 20
		body, readErr := io.ReadAll(io.LimitReader(c.Request.Body, maxBody+1))
		if readErr != nil || len(body) > maxBody {
			response.BadRequest(c, "Account management request exceeds 32 MiB or cannot be read")
			c.Abort()
			return
		}
		c.Request.Body = io.NopCloser(bytes.NewReader(body))
		if len(body) > 0 {
			var refs observerAccountReferences
			if json.Unmarshal(body, &refs) != nil {
				response.BadRequest(c, "Invalid account management request")
				c.Abort()
				return
			}
			var validate func(observerAccountReferences) bool
			validate = func(r observerAccountReferences) bool {
				if r.ProxyID != nil {
					return false
				}
				ids = append(ids, r.AccountIDs...)
				if r.AccountID != nil && *r.AccountID != 0 {
					ids = append(ids, *r.AccountID)
				}
				if r.GroupIDs != nil && service.ValidateObserverGroupBindings(ctx, *r.GroupIDs) != nil {
					return false
				}
				for groupID := range r.GroupAllowedModels {
					if !service.ObserverCanManageGroup(ctx, groupID) {
						return false
					}
				}
				for _, child := range r.Accounts {
					if child.GroupIDs == nil {
						return false
					}
					if !validate(child) {
						return false
					}
				}
				return r.Defaults == nil || validate(*r.Defaults)
			}
			if !validate(refs) {
				response.ErrorFrom(c, service.ErrObserverScope)
				c.Abort()
				return
			}
		}
	}
	if len(ids) > 0 {
		accounts, loadErr := h.adminService.GetAccountsByIDs(ctx, ids)
		if loadErr != nil {
			response.ErrorFrom(c, loadErr)
			c.Abort()
			return
		}
		allowed := make(map[int64]bool, len(accounts))
		for _, account := range accounts {
			if account != nil && service.ObserverCanManageAccount(ctx, account) {
				allowed[account.ID] = true
			}
		}
		for _, id := range ids {
			if !allowed[id] {
				response.ErrorFrom(c, service.ErrObserverScope)
				c.Abort()
				return
			}
		}
	}
	c.Next()
}

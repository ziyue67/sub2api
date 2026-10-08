//go:build unit

package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// supportTicketHandlerRepo stores tickets in memory, enough to drive the HTTP layer.
type supportTicketHandlerRepo struct {
	tickets  map[int64]*service.SupportTicket
	messages map[int64][]service.SupportTicketMessage
	lastAdd  service.SupportTicketMessageInput
}

func (r *supportTicketHandlerRepo) CreateTicket(_ context.Context, in service.SupportTicketCreateInput) (int64, error) {
	id := int64(len(r.tickets) + 1)
	r.tickets[id] = &service.SupportTicket{ID: id, UserID: in.UserID, Category: in.Category, Title: in.Title,
		Status: service.SupportTicketStatusPending, MessageCount: 1, AdminUnread: true}
	r.messages[id] = []service.SupportTicketMessage{{ID: id * 10, AuthorRole: service.SupportTicketRoleUser, Body: in.Body}}
	return id, nil
}
func (r *supportTicketHandlerRepo) ListTickets(_ context.Context, filter service.SupportTicketListFilter) ([]service.SupportTicket, int64, error) {
	items := []service.SupportTicket{}
	for _, ticket := range r.tickets {
		if filter.UserID == 0 || ticket.UserID == filter.UserID {
			items = append(items, *ticket)
		}
	}
	return items, int64(len(items)), nil
}
func (r *supportTicketHandlerRepo) GetTicket(_ context.Context, id, ownerID int64, withUser bool) (*service.SupportTicket, []service.SupportTicketMessage, error) {
	ticket, ok := r.tickets[id]
	if !ok || (ownerID > 0 && ticket.UserID != ownerID) {
		return nil, nil, service.ErrSupportTicketNotFound
	}
	copied := *ticket
	if withUser {
		copied.User = &service.SupportTicketUser{ID: ticket.UserID, Email: "user@example.com"}
	}
	return &copied, r.messages[id], nil
}
func (r *supportTicketHandlerRepo) MarkRead(context.Context, int64, string, int) error { return nil }
func (r *supportTicketHandlerRepo) AddMessage(_ context.Context, in service.SupportTicketMessageInput) error {
	r.lastAdd = in
	if _, _, err := r.GetTicket(context.Background(), in.TicketID, in.OwnerID, false); err != nil {
		return err
	}
	r.tickets[in.TicketID].Status = in.NextStatus
	return nil
}
func (r *supportTicketHandlerRepo) SetStatus(_ context.Context, in service.SupportTicketStatusInput) error {
	if _, _, err := r.GetTicket(context.Background(), in.TicketID, in.OwnerID, false); err != nil {
		return err
	}
	r.tickets[in.TicketID].Status = in.Status
	return nil
}
func (r *supportTicketHandlerRepo) DeleteTicket(_ context.Context, id int64) (bool, error) {
	_, ok := r.tickets[id]
	delete(r.tickets, id)
	return ok, nil
}
func (r *supportTicketHandlerRepo) CountUnread(context.Context, int64) (int64, error) { return 1, nil }
func (r *supportTicketHandlerRepo) CountOpen(context.Context, int64) (int64, error)   { return 2, nil }
func (r *supportTicketHandlerRepo) CountPending(context.Context) (int64, error)       { return 3, nil }

// supportTicketSettingRepo serves the two settings the ticket switch reads.
type supportTicketSettingRepo struct {
	service.SettingRepository
	values map[string]string
}

func (r *supportTicketSettingRepo) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	out := map[string]string{}
	for _, key := range keys {
		if value, ok := r.values[key]; ok {
			out[key] = value
		}
	}
	return out, nil
}

func newSupportTicketTestRouter(t *testing.T, enabled bool) (*gin.Engine, *supportTicketHandlerRepo) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	settings := &supportTicketSettingRepo{values: map[string]string{
		service.SettingKeySupportTicketEnabled: map[bool]string{true: "true", false: "false"}[enabled],
		service.SettingKeySupportTicketConfig:  `{"categories":["账户与充值","其他"],"max_open_per_user":5,"notice":"工作时间 9–21 点"}`,
	}}
	repo := &supportTicketHandlerRepo{tickets: map[int64]*service.SupportTicket{}, messages: map[int64][]service.SupportTicketMessage{}}
	h := NewSupportTicketHandler(service.NewSupportTicketService(repo, service.NewSettingService(settings, &config.Config{})))

	router := gin.New()
	router.Use(func(c *gin.Context) {
		// Tests pick the caller with headers: X-User (absent = anonymous) and X-Role.
		if user := c.GetHeader("X-User"); user != "" {
			id := int64(42)
			if user == "other" {
				id = 7
			}
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: id})
		}
		if role := c.GetHeader("X-Role"); role != "" {
			c.Set(string(middleware.ContextKeyUserRole), role)
		}
		c.Next()
	})
	user := router.Group("/api/v1/support-tickets")
	user.GET("", h.List)
	user.POST("", h.Create)
	user.GET("/summary", h.Summary)
	user.GET("/:id", h.Get)
	user.POST("/:id/messages", h.Reply)
	user.POST("/:id/close", h.Close)
	user.POST("/:id/reopen", h.Reopen)
	admin := router.Group("/api/v1/admin/support-tickets")
	admin.GET("", h.AdminList)
	admin.GET("/summary", h.AdminSummary)
	admin.GET("/:id", h.AdminGet)
	admin.POST("/:id/messages", h.AdminReply)
	admin.POST("/:id/status", h.AdminSetStatus)
	admin.DELETE("/:id", h.AdminDelete)
	return router, repo
}

type supportTicketEnvelope struct {
	Code    int             `json:"code"`
	Reason  string          `json:"reason"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func supportTicketCall(t *testing.T, router *gin.Engine, method, path, body, user string) (int, supportTicketEnvelope) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if user != "" {
		req.Header.Set("X-User", user)
	}
	if strings.Contains(path, "/admin/") {
		req.Header.Set("X-Role", service.RoleAdmin)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	var env supportTicketEnvelope
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env), rec.Body.String())
	return rec.Code, env
}

func TestSupportTicketHandlerUserFlow(t *testing.T) {
	router, repo := newSupportTicketTestRouter(t, true)

	status, env := supportTicketCall(t, router, http.MethodPost, "/api/v1/support-tickets",
		`{"category":"其他","title":"  401 报错 ","body":"the key stopped working"}`, "me")
	require.Equal(t, http.StatusOK, status, env.Message)
	var detail service.SupportTicketDetail
	require.NoError(t, json.Unmarshal(env.Data, &detail))
	require.Equal(t, "401 报错", detail.Ticket.Title)
	require.Equal(t, int64(42), detail.Ticket.UserID, "the owner comes from the session, not the body")
	require.Nil(t, detail.Ticket.User)

	status, env = supportTicketCall(t, router, http.MethodGet, "/api/v1/support-tickets?page=1&page_size=10", "", "me")
	require.Equal(t, http.StatusOK, status)
	var page struct {
		Items    []service.SupportTicket `json:"items"`
		Total    int64                   `json:"total"`
		Page     int                     `json:"page"`
		PageSize int                     `json:"page_size"`
		Pages    int                     `json:"pages"`
	}
	require.NoError(t, json.Unmarshal(env.Data, &page))
	require.Len(t, page.Items, 1)
	require.Equal(t, 10, page.PageSize)
	require.Equal(t, 1, page.Pages)

	status, env = supportTicketCall(t, router, http.MethodGet, "/api/v1/support-tickets/summary", "", "me")
	require.Equal(t, http.StatusOK, status)
	var summary service.SupportTicketUserSummary
	require.NoError(t, json.Unmarshal(env.Data, &summary))
	require.Equal(t, service.SupportTicketUserSummary{UnreadCount: 1, OpenCount: 2, MaxOpen: 5, Categories: []string{"账户与充值", "其他"}, Notice: "工作时间 9–21 点"}, summary)

	status, env = supportTicketCall(t, router, http.MethodGet, "/api/v1/support-tickets/1", "", "other")
	require.Equal(t, http.StatusNotFound, status)
	require.Equal(t, "SUPPORT_TICKET_NOT_FOUND", env.Reason, "someone else's ticket is not found, not forbidden")

	status, _ = supportTicketCall(t, router, http.MethodPost, "/api/v1/support-tickets/1/messages", `{"body":"more","status":"closed"}`, "me")
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, service.SupportTicketStatusPending, repo.lastAdd.NextStatus, "users cannot choose the status")
	require.Equal(t, int64(42), repo.lastAdd.OwnerID)

	status, _ = supportTicketCall(t, router, http.MethodPost, "/api/v1/support-tickets/1/close", "", "me")
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, service.SupportTicketStatusClosed, repo.tickets[1].Status)
	status, _ = supportTicketCall(t, router, http.MethodPost, "/api/v1/support-tickets/1/reopen", "", "me")
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, service.SupportTicketStatusPending, repo.tickets[1].Status)

	status, env = supportTicketCall(t, router, http.MethodPost, "/api/v1/support-tickets", `{"category":"退款","title":"t","body":"b"}`, "me")
	require.Equal(t, http.StatusBadRequest, status)
	require.Equal(t, "SUPPORT_TICKET_CATEGORY_INVALID", env.Reason)
	status, _ = supportTicketCall(t, router, http.MethodPost, "/api/v1/support-tickets", `{"title":`, "me")
	require.Equal(t, http.StatusBadRequest, status)
	status, _ = supportTicketCall(t, router, http.MethodGet, "/api/v1/support-tickets/abc", "", "me")
	require.Equal(t, http.StatusBadRequest, status)
	status, _ = supportTicketCall(t, router, http.MethodGet, "/api/v1/support-tickets/summary", "", "")
	require.Equal(t, http.StatusUnauthorized, status)
}

func TestSupportTicketHandlerAdminFlow(t *testing.T) {
	router, repo := newSupportTicketTestRouter(t, true)
	status, _ := supportTicketCall(t, router, http.MethodPost, "/api/v1/support-tickets", `{"category":"其他","title":"t","body":"b"}`, "me")
	require.Equal(t, http.StatusOK, status)

	status, env := supportTicketCall(t, router, http.MethodGet, "/api/v1/admin/support-tickets/1", "", "admin")
	require.Equal(t, http.StatusOK, status)
	var detail service.SupportTicketDetail
	require.NoError(t, json.Unmarshal(env.Data, &detail))
	require.Equal(t, "user@example.com", detail.Ticket.User.Email, "admins see the author")

	status, _ = supportTicketCall(t, router, http.MethodPost, "/api/v1/admin/support-tickets/1/messages", `{"body":"done","status":"processing"}`, "admin")
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, service.SupportTicketStatusProcessing, repo.lastAdd.NextStatus)
	require.Equal(t, service.SupportTicketRoleAdmin, repo.lastAdd.AuthorRole)
	status, env = supportTicketCall(t, router, http.MethodPost, "/api/v1/admin/support-tickets/1/messages", `{"body":"done","status":"pending"}`, "admin")
	require.Equal(t, http.StatusBadRequest, status)
	require.Equal(t, "SUPPORT_TICKET_STATUS_INVALID", env.Reason)

	status, _ = supportTicketCall(t, router, http.MethodPost, "/api/v1/admin/support-tickets/1/status", `{"status":"closed"}`, "admin")
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, service.SupportTicketStatusClosed, repo.tickets[1].Status)

	status, env = supportTicketCall(t, router, http.MethodGet, "/api/v1/admin/support-tickets/summary", "", "admin")
	require.Equal(t, http.StatusOK, status)
	require.JSONEq(t, `{"pending_count":3,"categories":["账户与充值","其他"]}`, string(env.Data))

	status, _ = supportTicketCall(t, router, http.MethodDelete, "/api/v1/admin/support-tickets/1", "", "admin")
	require.Equal(t, http.StatusOK, status)
	status, env = supportTicketCall(t, router, http.MethodDelete, "/api/v1/admin/support-tickets/1", "", "admin")
	require.Equal(t, http.StatusNotFound, status)
	require.Equal(t, "SUPPORT_TICKET_NOT_FOUND", env.Reason)
	status, env = supportTicketCall(t, router, http.MethodGet, "/api/v1/admin/support-tickets?status=deleted", "", "admin")
	require.Equal(t, http.StatusBadRequest, status)
	require.Equal(t, "SUPPORT_TICKET_STATUS_INVALID", env.Reason)
}

func TestSupportTicketHandlerDisabled(t *testing.T) {
	router, repo := newSupportTicketTestRouter(t, false)
	for _, call := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/v1/support-tickets/summary", ""},
		{http.MethodGet, "/api/v1/support-tickets", ""},
		{http.MethodPost, "/api/v1/support-tickets", `{"category":"其他","title":"t","body":"b"}`},
		{http.MethodGet, "/api/v1/admin/support-tickets/summary", ""},
		{http.MethodGet, "/api/v1/admin/support-tickets", ""},
		{http.MethodDelete, "/api/v1/admin/support-tickets/1", ""},
	} {
		status, env := supportTicketCall(t, router, call.method, call.path, call.body, "me")
		require.Equal(t, http.StatusForbidden, status, call.path)
		require.Equal(t, "SUPPORT_TICKET_DISABLED", env.Reason, call.path)
	}
	require.Empty(t, repo.tickets, "nothing is stored while the feature is off")
}

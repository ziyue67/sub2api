package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type monitorV1ReaderStub struct{}

func (*monitorV1ReaderStub) ListUserView(context.Context) ([]*service.UserMonitorView, error) {
	return []*service.UserMonitorView{{ID: 1, Name: "Allowed", GroupName: "Mine"}, {ID: 2, Name: "Forbidden", GroupName: "Other"}, {ID: 3, Name: "Unbound", GroupName: ""}}, nil
}
func (*monitorV1ReaderStub) GetUserDetail(_ context.Context, id int64) (*service.UserMonitorDetail, error) {
	if id == 1 {
		return &service.UserMonitorDetail{ID: 1, Name: "Allowed", GroupName: "Mine"}, nil
	}
	if id == 2 {
		return &service.UserMonitorDetail{ID: 2, Name: "Forbidden", GroupName: "Other"}, nil
	}
	return nil, service.ErrChannelMonitorNotFound
}

func TestV1MonitorScopesListAndDetail(t *testing.T) {
	access := &monitorV3AccessStub{groups: []service.Group{{ID: 1, Name: "Mine"}}}
	h := &ChannelMonitorUserHandler{monitorService: &monitorV1ReaderStub{}, access: access}
	for _, role := range []string{"user", "observer", "admin"} {
		t.Run(role, func(t *testing.T) {
			c, w := monitorV3Request("/api/v1/channel-monitors", role, "")
			h.List(c)
			require.Equal(t, http.StatusOK, w.Code)
			require.Contains(t, w.Body.String(), "Allowed")
			if role == "admin" {
				require.Contains(t, w.Body.String(), "Forbidden")
				require.Contains(t, w.Body.String(), "Unbound")
			} else {
				require.NotContains(t, w.Body.String(), "Forbidden")
				require.NotContains(t, w.Body.String(), "Unbound")
			}
			c, w = monitorV3Request("/api/v1/channel-monitors/2/status", role, "")
			c.Params = gin.Params{{Key: "id", Value: "2"}}
			h.GetStatus(c)
			if role == "admin" {
				require.Equal(t, 200, w.Code)
			} else {
				require.Equal(t, 404, w.Code)
				require.NotContains(t, w.Body.String(), "Forbidden")
			}
		})
	}
	c, w := monitorV3Request("/api/v1/channel-monitors/1/status", "user", "")
	c.Params = gin.Params{{Key: "id", Value: "1"}}
	h.GetStatus(c)
	require.Equal(t, 200, w.Code)
	access.err = errors.New("group lookup unavailable")
	c, w = monitorV3Request("/api/v1/channel-monitors", "user", "")
	h.List(c)
	require.Equal(t, 500, w.Code)
	require.NotContains(t, w.Body.String(), "Forbidden")
	access.err = nil
	access.groups = nil
	c, w = monitorV3Request("/api/v1/channel-monitors", "user", "")
	h.List(c)
	require.Equal(t, 200, w.Code)
	require.NotContains(t, w.Body.String(), "Allowed")
	c, w = func() (*gin.Context, *httptest.ResponseRecorder) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/api/v1/channel-monitors", nil)
		return c, w
	}()
	h.List(c)
	require.Equal(t, http.StatusUnauthorized, w.Code)
	require.NotContains(t, w.Body.String(), "Allowed")
}

//go:build unit

package routes

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	adminhandler "github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/pkg/adminaccess"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type issue603RegisteredOpsRepo struct {
	service.OpsRepository
	reads int
}

func (r *issue603RegisteredOpsRepo) InsertNetworkSample(context.Context, service.OpsNetworkPoint) error {
	return nil
}
func (r *issue603RegisteredOpsRepo) ReadNetworkSeries(_ context.Context, _, end time.Time) (*service.OpsNetworkSeries, error) {
	r.reads++
	return &service.OpsNetworkSeries{Points: []service.OpsNetworkPoint{{At: end.Add(-time.Minute), EgressStatus: "permission_denied", TCPStatus: "no_segments", QdiscStatus: "unsupported_unprivileged_collector"}}, BodyPoints: []service.OpsBodyBytesPoint{{At: end.Add(-time.Minute), Bytes: 17, Samples: 1}}}, nil
}

func TestIssue603RegisteredOpsNetworkRouteAndGlobalPermission(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, allowed := range []bool{true, false} {
		store := &issue603RegisteredOpsRepo{}
		svc := service.NewOpsService(store, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
		h := &handler.Handlers{Admin: &handler.AdminHandlers{Ops: adminhandler.NewOpsHandler(svc)}}
		r := gin.New()
		admin := r.Group("/api/v1/admin")
		policy := adminaccess.Policy{}
		if allowed {
			policy.GlobalPermissions = []string{"ops.read"}
		}
		admin.Use(func(c *gin.Context) {
			if !adminaccess.RouteAllowed(policy, c.Request.Method, c.FullPath()) {
				c.AbortWithStatus(403)
				return
			}
			c.Next()
		})
		registerOpsRoutes(admin, h)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/admin/ops/dashboard/network-series", nil))
		if allowed {
			require.Equal(t, 200, w.Code, "registered ops network route must reach its real handler instead of returning 404")
			require.Contains(t, w.Body.String(), `"egress_mbps":null`)
			require.Contains(t, w.Body.String(), `"egress_status":"permission_denied"`)
			require.Contains(t, w.Body.String(), `"qdisc_drops":null`)
			require.Equal(t, 1, store.reads)
		} else {
			require.Equal(t, 403, w.Code)
			require.Zero(t, store.reads, "denied global read must not reach metric storage")
		}
	}
}

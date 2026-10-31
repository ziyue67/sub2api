package admin

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type issue603LatencyRepo struct {
	service.UsageLogRepository
	service.OpsRepository
	filter service.LatencyReportFilter
}

func (r *issue603LatencyRepo) ReadLatencyReport(_ context.Context, f service.LatencyReportFilter) ([]service.LatencyGroup, error) {
	r.filter = f
	return []service.LatencyGroup{{BodyBucket: "unknown", Requests: 1}}, nil
}
func (r *issue603LatencyRepo) InsertNetworkSample(context.Context, service.OpsNetworkPoint) error {
	return nil
}
func (r *issue603LatencyRepo) ReadNetworkSeries(context.Context, time.Time, time.Time) (*service.OpsNetworkSeries, error) {
	return &service.OpsNetworkSeries{Points: []service.OpsNetworkPoint{{EgressStatus: "unavailable", TCPStatus: "no_segments", QdiscStatus: "unsupported_unprivileged_collector"}}}, nil
}

func TestIssue603RealUsageAndOpsHandlers(t *testing.T) {
	stub := &issue603LatencyRepo{}
	usage := NewUsageHandler(service.NewUsageService(stub, nil, nil, nil), nil, nil, nil)
	ops := NewOpsHandler(service.NewOpsService(stub, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil))
	r := gin.New()
	r.GET("/usage/latency-report", usage.LatencyReport)
	r.GET("/ops/latency-report", ops.LatencyReport)
	r.GET("/ops/network-series", ops.NetworkSeries)
	for _, path := range []string{"/usage/latency-report", "/ops/latency-report"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", path+"?user_id=42&model=synthetic", nil))
		require.Equal(t, 200, w.Code)
		require.Contains(t, w.Body.String(), `"end_to_end_ms":null`)
		require.Contains(t, w.Body.String(), `"end_to_end_status":"not_collected"`)
		require.Contains(t, w.Body.String(), `"end_to_end_source":"none"`)
		require.Equal(t, int64(42), stub.filter.UserID)
		require.Equal(t, "synthetic", stub.filter.Model)
		require.Equal(t, path == "/ops/latency-report", stub.filter.IncludeUnbilled)
		for _, bad := range []string{"user_id=-1", "start_date=bad", "start_date=2026-01-01&end_date=2026-03-01"} {
			w = httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest("GET", path+"?"+bad, nil))
			require.Equal(t, 400, w.Code, bad)
		}
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/ops/network-series", nil))
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), `"egress_mbps":null`)
	require.Contains(t, w.Body.String(), "unsupported_unprivileged_collector")
}

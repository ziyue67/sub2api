package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type connectionListStub struct {
	service.UsageLogRepository
	filter service.ConnectionTraceFilter
}

func (r *connectionListStub) ListConnectionTraces(_ context.Context, f service.ConnectionTraceFilter) (*service.ConnectionTraceList, error) {
	r.filter = f
	return &service.ConnectionTraceList{Items: []service.ConnectionTrace{}, Page: f.Page, PageSize: f.PageSize}, nil
}

func TestConnectionFiltersAndBounds(t *testing.T) {
	stub := &connectionListStub{}
	h := NewUsageHandler(service.NewUsageService(stub, nil, nil, nil), nil, nil, nil)
	r := gin.New()
	r.GET("/connections", h.Connections)
	for _, query := range []string{"page=-1", "page=501", "page_size=51", "user_id=-1", "source=evil", "state=success", "client_ip=invalid", "start_time=invalid", "start_time=2026-01-01T00:00:00Z&end_time=2026-03-01T00:00:00Z"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", "/connections?"+query, nil))
		require.Equal(t, http.StatusBadRequest, w.Code, query)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/connections?user_id=12&api_key_id=7&source=edge&host=relay.example.com&state=abnormal", nil))
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, int64(12), stub.filter.UserID)
	require.Equal(t, "edge", stub.filter.Source)
	require.Equal(t, "relay.example.com", stub.filter.Host)
	require.Equal(t, 20, stub.filter.PageSize)
}

func TestEdgeImportSanitizesAndCannotAssertUserIdentity(t *testing.T) {
	now := time.Now()
	v := edgeConnectionEvent{ID: uuid.NewString(), Source: "nginx", Time: now, Host: "relay.example.com", Scheme: "https", Method: "POST", Path: "/v1/responses?key=secret", ClientIP: "192.0.2.8", Status: 502, NetworkError: "upstream_unavailable"}
	got, ok := edgeConnectionRecord(v, now)
	require.True(t, ok)
	require.Equal(t, "/v1/responses", got.Path)
	require.Zero(t, got.UserID)
	require.Zero(t, got.APIKeyID)
	v.NetworkError = "raw secret-bearing error"
	_, ok = edgeConnectionRecord(v, now)
	require.False(t, ok)
	v.NetworkError = "tls_error"
	v.Host = ""
	v.Status = 0
	v.Path = ""
	v.Method = ""
	got, ok = edgeConnectionRecord(v, now)
	require.True(t, ok)
	require.Empty(t, got.Host)
	require.Empty(t, got.Path)
	// Missing host is allowed only before HTTP exists; a malformed host is
	// rejected even for TLS handshake errors, without attributing an identity.
	for _, host := range []string{"", "relay.example.com", "invalid/host"} {
		v.Host = host
		_, ok = edgeConnectionRecord(v, now)
		require.Equal(t, host != "invalid/host", ok, host)
	}
	v.NetworkError = "upstream_unavailable"
	v.Status = 502
	v.Path = "/v1/responses"
	v.Method = "POST"
	v.Host = ""
	_, ok = edgeConnectionRecord(v, now)
	require.False(t, ok, "ordinary HTTP events require a host")
	v.Host = "relay.example.com"
	v.Time = now.Add(-31 * 24 * time.Hour)
	_, ok = edgeConnectionRecord(v, now)
	require.False(t, ok)
}

package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestGatewayReadinessUsesScopedSnapshotAndRecovers(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mr := miniredis.RunT(t)
	mr.SetTime(time.Unix(1700000000, 0))
	redisClient := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1})
	t.Cleanup(func() { _ = redisClient.Close() })
	cfg := &config.Config{Runtime: config.RuntimeConfig{Role: config.RuntimeRoleGateway, SchedulerReadiness: config.SchedulerReadinessConfig{Enabled: true, Buckets: []string{"17:openai:single"}, MaxAgeSeconds: 60}}}
	cache := repository.NewSchedulerCache(redisClient)
	scheduler := service.NewSchedulerSnapshotService(cache, nil, nil, nil, cfg)
	defer scheduler.Stop()
	lifecycle := ProvideLifecycle(cfg, db, redisClient, scheduler)
	handler := lifecycle.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))
	probe := func(want int) {
		t.Helper()
		mock.ExpectPing()
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
		require.Equal(t, want, response.Code)
		if want == 503 {
			require.JSONEq(t, `{"status":"not_ready"}`, response.Body.String())
		}
	}
	probe(503) // Database and Redis ping already succeed; snapshots do not exist.
	bucket := service.SchedulerBucket{GroupID: 17, Platform: service.PlatformOpenAI, Mode: service.SchedulerModeSingle}
	ctx := context.Background()
	token, err := cache.CaptureBucketWriteToken(ctx, bucket)
	require.NoError(t, err)
	require.NoError(t, cache.SetSnapshot(ctx, bucket, token, []service.Account{{ID: 7}}))
	probe(200)
	mr.SetTime(time.Unix(1700000061, 0))
	probe(503)
	require.NoError(t, cache.SetSnapshot(ctx, bucket, token, []service.Account{{ID: 7}}))
	probe(200)
	mr.Del("sched:meta:7")
	probe(503)
	require.NoError(t, cache.SetAccount(ctx, &service.Account{ID: 7}))
	probe(200)
	require.NoError(t, cache.RetireBucket(ctx, bucket))
	probe(503)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/health", nil))
	require.Equal(t, 200, response.Code)
	lifecycle.BeginDrain()
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	require.Equal(t, 503, response.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

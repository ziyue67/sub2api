package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

// Exercise the actual provider and HTTP wrapper: changing only the background
// dependency timeout would still let the old HTTP deadline reject the probe.
func TestProvideLifecycleSlowDependency(t *testing.T) {
	for _, tc := range []struct {
		name    string
		seconds int
		want    int
	}{
		{name: "default rejects after one second", want: http.StatusServiceUnavailable},
		{name: "configured budget permits slow success", seconds: 2, want: http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			mock.ExpectPing().WillDelayFor(1200 * time.Millisecond)
			mr := miniredis.RunT(t)
			cache := redis.NewClient(&redis.Options{Addr: mr.Addr()})
			t.Cleanup(func() { _ = cache.Close() })
			cfg := &config.Config{Server: config.ServerConfig{ReadinessTimeoutSeconds: tc.seconds}}
			lifecycle := ProvideLifecycle(cfg, db, cache)
			response := httptest.NewRecorder()
			lifecycle.Wrap(http.NotFoundHandler()).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
			require.Equal(t, tc.want, response.Code)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestProvideLifecycleReadinessDefaults(t *testing.T) {
	for _, cfg := range []*config.Config{nil, {}, {Server: config.ServerConfig{ReadinessTimeoutSeconds: 0}}} {
		lifecycle := ProvideLifecycle(cfg, nil, nil)
		require.Equal(t, time.Second, lifecycle.probeTimeout)
		response := httptest.NewRecorder()
		lifecycle.Wrap(http.NotFoundHandler()).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
		require.Equal(t, http.StatusServiceUnavailable, response.Code, "an unavailable dependency must still fail closed")
		require.JSONEq(t, `{"status":"not_ready"}`, response.Body.String())
	}
}

func TestProvideLifecycleConfiguredBudgetStillRejectsDependencyFailures(t *testing.T) {
	for _, dependency := range []string{"postgres", "redis"} {
		t.Run(dependency, func(t *testing.T) {
			db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			ping := mock.ExpectPing()
			mr := miniredis.RunT(t)
			const privateError = "synthetic-password-must-stay-private"
			if dependency == "postgres" {
				ping.WillReturnError(errors.New(privateError))
			} else {
				mr.SetError(privateError)
			}
			cache := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1})
			t.Cleanup(func() { _ = cache.Close() })
			cfg := &config.Config{Server: config.ServerConfig{ReadinessTimeoutSeconds: 3}}
			lifecycle := ProvideLifecycle(cfg, db, cache)
			handler := lifecycle.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
			require.Equal(t, http.StatusServiceUnavailable, response.Code)
			require.JSONEq(t, `{"status":"not_ready"}`, response.Body.String())
			require.NotContains(t, response.Body.String(), privateError)
			response = httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/health", nil))
			require.Equal(t, http.StatusOK, response.Code)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestLifecycleInternalReadinessWaitIsBounded(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	var calls atomic.Int32
	lifecycle := newLifecycleWithTimeout(25*time.Millisecond, func(context.Context) error {
		calls.Add(1)
		<-release // model a driver that ignores cancellation
		return nil
	})
	done := make(chan error, 1)
	go func() { done <- lifecycle.checkWithinBudget(context.Background()) }()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.DeadlineExceeded)
	case <-time.After(time.Second):
		t.Fatal("internal readiness caller remained blocked past the configured budget")
	}
	require.ErrorIs(t, lifecycle.checkWithinBudget(context.Background()), context.DeadlineExceeded)
	require.Equal(t, int32(1), calls.Load(), "overlapping callers must share the stalled check")
}

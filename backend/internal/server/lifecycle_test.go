package server

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLifecycleReadinessAndSetupNeverReturnSPA(t *testing.T) {
	for _, tc := range []struct {
		name  string
		check func(context.Context) error
		want  int
	}{
		{"healthy", func(context.Context) error { return nil }, http.StatusOK},
		{"dependency failure", func(context.Context) error { return errors.New("private-db-password") }, http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lifecycle := NewLifecycle(tc.check)
			handler := lifecycle.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "SPA page") }))
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
			require.Equal(t, tc.want, response.Code)
			require.Equal(t, "application/json", response.Header().Get("Content-Type"))
			require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
			require.NotContains(t, response.Body.String(), "private-db-password")
			require.NotContains(t, response.Body.String(), "SPA")
			lifecycle.BeginDrain()
			response = httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
			require.Equal(t, http.StatusServiceUnavailable, response.Code)
		})
	}
	setup := SetupHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "SPA page") }))
	response := httptest.NewRecorder()
	setup.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	require.Equal(t, http.StatusServiceUnavailable, response.Code)
	require.Contains(t, response.Body.String(), "needs_setup")
}

func TestLifecycleReadinessUsesConfiguredDependencyTimeout(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	lifecycle := newLifecycleWithTimeout(50*time.Millisecond, func(context.Context) error {
		close(entered)
		<-release
		return nil
	})
	t.Cleanup(func() { close(release) })
	handler := lifecycle.Wrap(http.NotFoundHandler())
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
		close(done)
	}()
	<-entered
	select {
	case <-done:
		require.Equal(t, http.StatusServiceUnavailable, response.Code)
	case <-time.After(time.Second):
		t.Fatal("configured readiness probe budget was not enforced")
	}
}

func TestLifecycleDrainPreservesStreamAndRejectsNewRequests(t *testing.T) {
	lifecycle := NewLifecycle()
	release := make(chan struct{})
	var once sync.Once
	finish := func() { once.Do(func() { close(release) }) }
	server := httptest.NewServer(lifecycle.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: first\n\n")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("streaming ResponseWriter lost Flusher")
			return
		}
		flusher.Flush()
		<-release
		_, _ = io.WriteString(w, "data: done\n\n")
	})))
	defer server.Close()
	defer finish()
	response, err := server.Client().Get(server.URL + "/v1/responses")
	require.NoError(t, err)
	defer func() { _ = response.Body.Close() }()
	reader := bufio.NewReader(response.Body)
	line, err := reader.ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "data: first\n", line)
	lifecycle.BeginDrain()
	lifecycle.BeginDrain()
	for _, tc := range []struct {
		path   string
		status int
	}{{"/readyz", 503}, {"/v1/responses", 503}, {"/health", 200}} {
		next, err := server.Client().Get(server.URL + tc.path)
		require.NoError(t, err)
		require.Equal(t, tc.status, next.StatusCode)
		require.NoError(t, next.Body.Close())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, lifecycle.Wait(ctx), context.Canceled, "in-flight stream must still be tracked")
	finish()
	rest, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.Contains(t, string(rest), "data: done")
	ctx, cancel = context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, lifecycle.Wait(ctx))
}

func TestLifecycleTracksHijackedHandlers(t *testing.T) {
	lifecycle := NewLifecycle()
	entered := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(lifecycle.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			t.Error("ResponseWriter lost Hijacker")
			return
		}
		connection, rw, err := hijacker.Hijack()
		if err != nil {
			return
		}
		defer func() { _ = connection.Close() }()
		_, _ = fmt.Fprint(rw, "HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok")
		_ = rw.Flush()
		close(entered)
		<-release
	})))
	defer server.Close()
	response, err := server.Client().Get(server.URL)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	<-entered
	lifecycle.BeginDrain()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, lifecycle.Wait(ctx), context.DeadlineExceeded)
	close(release)
	ctx, cancel = context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, lifecycle.Wait(ctx))
}

func TestLifecycleProbeChecksRespectCancellationAndMethod(t *testing.T) {
	lifecycle := NewLifecycle(func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() })
	handler := lifecycle.Wrap(http.NotFoundHandler())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil).WithContext(ctx))
	require.Equal(t, 503, response.Code)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/readyz", strings.NewReader("ignored")))
	require.Equal(t, 405, response.Code)
}

func TestLifecycleBoundsAProbeThatIgnoresCancellation(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	var calls atomic.Int32
	lifecycle := NewLifecycle(func(context.Context) error {
		if calls.Add(1) == 1 {
			close(entered)
		}
		<-release
		return nil
	})
	handler := lifecycle.Wrap(http.NotFoundHandler())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil).WithContext(ctx))
		close(done)
	}()
	<-entered
	cancel()
	select {
	case <-done:
		require.Equal(t, http.StatusServiceUnavailable, response.Code)
	case <-time.After(time.Second):
		t.Fatal("HTTP probe must stop waiting on a stuck client")
	}
	next := httptest.NewRecorder()
	handler.ServeHTTP(next, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	require.Equal(t, http.StatusServiceUnavailable, next.Code)
	require.Equal(t, int32(1), calls.Load(), "another probe must not spawn an additional stuck check")
	unblock()
}

func TestLifecycleOverlappingProbesShareWorkAndKeepCallerDeadlines(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	finish := func() { once.Do(func() { close(release) }) }
	defer finish()
	var calls atomic.Int32
	lifecycle := NewLifecycle(func(context.Context) error {
		if calls.Add(1) == 1 {
			close(entered)
		}
		<-release
		return nil
	})
	first := make(chan error, 1)
	go func() { first <- lifecycle.checkWithinBudget(context.Background()) }()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, lifecycle.checkWithinBudget(ctx), context.DeadlineExceeded, "an overlapping probe should wait for shared work rather than fail as busy")
	finish()
	require.NoError(t, <-first, "another caller's timeout must not cancel the shared dependency check")
	require.Equal(t, int32(1), calls.Load())
}

package repository

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type pr612SettingRepo struct {
	service.SettingRepository
	mu    sync.Mutex
	value string
}

func pr612NativeTransport(t *testing.T, upstream service.HTTPUpstream) *httpUpstreamService {
	t.Helper()
	if routed, ok := upstream.(*astraRoutingUpstream); ok {
		upstream = routed.delegate
	}
	native, ok := upstream.(*httpUpstreamService)
	require.True(t, ok)
	return native
}

type pr612StaleSettingRepo struct {
	pr612SettingRepo
	started chan struct{}
	release chan struct{}
	blocked atomic.Bool
	readErr error
}

func (r *pr612StaleSettingRepo) GetValue(ctx context.Context, key string) (string, error) {
	value, err := r.pr612SettingRepo.GetValue(ctx, key)
	if key == service.SettingKeyOpenAIResponseHeaderTimeoutSettings && r.blocked.CompareAndSwap(false, true) {
		close(r.started) // The old enabled snapshot is fixed before the save.
		select {
		case <-r.release:
		case <-ctx.Done():
			return "", ctx.Err()
		}
		if r.readErr != nil {
			return "", r.readErr
		}
	}
	return value, err
}

func TestPR612StaleReaderDisableControlsNextHTTPRequest(t *testing.T) {
	for _, failedRead := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "DB_error"}[failedRead], func(t *testing.T) {
			repo := &pr612StaleSettingRepo{pr612SettingRepo: pr612SettingRepo{value: `{"enabled":true,"seconds":1}`},
				started: make(chan struct{}), release: make(chan struct{})}
			if failedRead {
				repo.readErr = errors.New("old snapshot read failed")
			}
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(repo.release) }) }
			defer release()
			ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
			defer cancel()
			cfg := &config.Config{}
			cfg.Security.URLAllowlist.AllowPrivateHosts = true
			cfg.Security.URLAllowlist.AllowInsecureHTTP = true
			cfg.Gateway.OpenAIResponseHeaderTimeout = 1
			cfg.Gateway.ResponseHeaderTimeout = 600
			settings := service.NewSettingService(repo, cfg)
			readerDone := make(chan bool, 1)
			go func() {
				_, enabled := settings.GetOpenAIResponseHeaderTimeoutCached(ctx)
				readerDone <- enabled
			}()
			select {
			case <-repo.started:
			case <-ctx.Done():
				t.Fatal("old reader did not reach the snapshot barrier")
			}
			saveDone := make(chan error, 1)
			go func() {
				saveDone <- settings.SetOpenAIResponseHeaderTimeoutSettings(ctx, &service.OpenAIResponseHeaderTimeoutSettings{Enabled: false, Seconds: 1})
			}()
			select {
			case err := <-saveDone:
				require.NoError(t, err, "disable save must complete before releasing the old reader")
			case <-ctx.Done():
				t.Fatal("disable save blocked behind the old reader")
			}
			release()
			select {
			case <-readerDone:
			case <-ctx.Done():
				t.Fatal("old reader did not complete")
			}
			var submissions atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				submissions.Add(1)
				select {
				case <-time.After(1200 * time.Millisecond):
					_, _ = io.WriteString(w, "completion after old deadline")
				case <-r.Context().Done():
				}
			}))
			defer server.Close()
			upstream := newHTTPUpstream(cfg, settings)
			native := pr612NativeTransport(t, upstream)
			// This is the actual next network request after the stale reader exits.
			req, err := http.NewRequestWithContext(service.WithHTTPUpstreamProfile(ctx, service.HTTPUpstreamProfileOpenAI),
				http.MethodPost, server.URL+"/slow", strings.NewReader("synthetic"))
			require.NoError(t, err)
			resp, err := upstream.Do(req, "", 1, 1)
			require.NoError(t, err, "disabled next-request transport must have timeout 0")
			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())
			require.Equal(t, "completion after old deadline", string(body))
			require.Zero(t, native.applyProfilePoolSettings(poolSettings{}, service.HTTPUpstreamProfileOpenAI).responseHeaderTimeout)
			require.Equal(t, int32(1), submissions.Load())
			requireNoUpstreamInFlight(t, native)
		})
	}
}

func TestPR612NilSettingsProviderUsesStaticTimeout(t *testing.T) {
	for _, seconds := range []int{0, 45} {
		cfg := &config.Config{}
		cfg.Gateway.OpenAIResponseHeaderTimeout = seconds
		cfg.Gateway.ResponseHeaderTimeout = 600
		s, ok := ProvideHTTPUpstream(cfg, nil).(*httpUpstreamService)
		require.True(t, ok)
		require.Equal(t, time.Duration(seconds)*time.Second, s.applyProfilePoolSettings(poolSettings{}, service.HTTPUpstreamProfileOpenAI).responseHeaderTimeout)
	}
}

func (r *pr612SettingRepo) GetValue(context.Context, string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.value == "" {
		return "", service.ErrSettingNotFound
	}
	return r.value, nil
}

func (r *pr612SettingRepo) Set(_ context.Context, _, value string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.value = value
	return nil
}

func TestPR612RuntimeDisableRebuildsPoolAndPreservesActiveStream(t *testing.T) {
	finishStream := make(chan struct{})
	var finishOnce sync.Once
	finish := func() { finishOnce.Do(func() { close(finishStream) }) }
	defer finish()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/stream" {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: begin\n\n")
			_ = http.NewResponseController(w).Flush()
			select {
			case <-finishStream:
				_, _ = io.WriteString(w, "data: done\n\n")
			case <-r.Context().Done():
			}
			return
		}
		// Exceeds the old one-second timer: an actual next request must use 0.
		select {
		case <-time.After(1200 * time.Millisecond):
			_, _ = io.WriteString(w, "slow completion")
		case <-r.Context().Done():
		}
	}))
	defer upstream.Close()
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.AllowPrivateHosts = true
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	cfg.Gateway.OpenAIResponseHeaderTimeout = 1
	cfg.Gateway.ResponseHeaderTimeout = 600
	settings := service.NewSettingService(&pr612SettingRepo{}, cfg)
	upstreamHTTP := newHTTPUpstream(cfg, settings)
	s := pr612NativeTransport(t, upstreamHTTP)
	ctx, cancel := context.WithTimeout(t.Context(), 6*time.Second)
	defer cancel()
	ctx = service.WithHTTPUpstreamProfile(ctx, service.HTTPUpstreamProfileOpenAI)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, upstream.URL+"/stream", strings.NewReader("synthetic"))
	require.NoError(t, err)
	stream, err := upstreamHTTP.Do(req, "", 1, 1)
	require.NoError(t, err)
	defer func() { require.NoError(t, stream.Body.Close()) }()
	prefix := make([]byte, len("data: begin\n\n"))
	_, err = io.ReadFull(stream.Body, prefix)
	require.NoError(t, err)
	oldPool := s.applyProfilePoolSettings(poolSettings{}, service.HTTPUpstreamProfileOpenAI)
	require.NoError(t, settings.SetOpenAIResponseHeaderTimeoutSettings(ctx, &service.OpenAIResponseHeaderTimeoutSettings{Enabled: false, Seconds: 1}))
	newPool := s.applyProfilePoolSettings(poolSettings{}, service.HTTPUpstreamProfileOpenAI)
	require.Zero(t, newPool.responseHeaderTimeout, "disabled never inherits generic 600s or static 1s")
	require.NotEqual(t, buildPoolKey(oldPool, ""), buildPoolKey(newPool, ""))
	req, err = http.NewRequestWithContext(ctx, http.MethodPost, upstream.URL+"/slow", strings.NewReader("synthetic"))
	require.NoError(t, err)
	resp, err := upstreamHTTP.Do(req, "", 1, 1)
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, "slow completion", string(body))
	// The old stream has remained open longer than its original header timer.
	finish()
	remainder, err := io.ReadAll(stream.Body)
	require.NoError(t, err)
	require.Equal(t, "data: done\n\n", string(remainder), "saving must leave the old active stream intact")
	require.NoError(t, stream.Body.Close())
	requireNoUpstreamInFlight(t, s)
	// Explicit static 0 also bypasses the generic fallback without a DB provider.
	cfg.Gateway.OpenAIResponseHeaderTimeout = 0
	static := pr612NativeTransport(t, NewHTTPUpstream(cfg))
	require.Zero(t, static.applyProfilePoolSettings(poolSettings{}, service.HTTPUpstreamProfileOpenAI).responseHeaderTimeout)
}

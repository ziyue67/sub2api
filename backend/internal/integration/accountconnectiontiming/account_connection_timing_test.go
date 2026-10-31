//go:build unit

package accountconnectiontiming

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/requesttiming"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type timingAccounts struct {
	service.AccountRepository
	account *service.Account
}

func (r timingAccounts) GetByID(context.Context, int64) (*service.Account, error) {
	// Match accountRepository.GetByID: each request gets its own domain object.
	// Account has lazy mapping/header caches, so returning the same mutable
	// pointer would introduce a fixture-only race unrelated to the collector.
	account := *r.account
	account.Credentials = maps.Clone(r.account.Credentials)
	account.Extra = maps.Clone(r.account.Extra)
	return &account, nil
}
func (r timingAccounts) UpdateExtra(context.Context, int64, map[string]any) error { return nil }

// Rewriting is strictly fixture-only; the real repository transport still owns
// Client.Do, HTTP tracing, decompression, body close and in-flight accounting.
type timingFixtureUpstream struct {
	delegate service.HTTPUpstream
	target   *url.URL
	written  chan struct{}
}

func (u timingFixtureUpstream) Do(req *http.Request, proxy string, account int64, concurrency int) (*http.Response, error) {
	forward := req.Clone(req.Context())
	target := *u.target
	target.Path = req.URL.Path
	forward.URL, forward.Host = &target, ""
	if u.written != nil {
		var once sync.Once
		forward = forward.WithContext(httptrace.WithClientTrace(forward.Context(), &httptrace.ClientTrace{
			WroteRequest: func(info httptrace.WroteRequestInfo) {
				if info.Err == nil {
					once.Do(func() { close(u.written) })
				}
			},
		}))
	}
	return u.delegate.Do(forward, proxy, account, concurrency)
}
func (u timingFixtureUpstream) DoWithTLS(req *http.Request, proxy string, account int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxy, account, concurrency)
}

func timingRouter(account *service.Account, target *url.URL, written ...chan struct{}) *gin.Engine {
	cfg := &config.Config{}
	upstream := timingFixtureUpstream{delegate: repository.NewHTTPUpstream(cfg), target: target}
	if len(written) != 0 {
		upstream.written = written[0]
	}
	svc := service.NewAccountTestService(timingAccounts{account: account}, nil, nil, nil, nil, upstream, cfg, nil)
	h := admin.NewAccountHandler(nil, nil, nil, nil, nil, nil, nil, nil, svc, nil, nil, nil, nil, nil)
	router := gin.New()
	router.POST("/api/v1/admin/accounts/:id/test", h.Test)
	return router
}

func timingFlush(w http.ResponseWriter) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		panic("synthetic server does not support flushing")
	}
	flusher.Flush()
}

func timingWait(t *testing.T, channel <-chan struct{}) {
	t.Helper()
	select {
	case <-channel:
	case <-time.After(10 * time.Second):
		t.Fatal("synthetic test gate timed out")
	}
}

func TestAccountConnectionTimingHandlerHTTP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, scenario := range []string{"apikey-responses", "oauth-responses", "apikey-chat", "upstream-error", "incomplete", "metadata-only", "non-stream"} {
		t.Run(scenario, func(t *testing.T) {
			entered, headers, metadata, content, contentSeen, terminal := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
			requestWritten := make(chan struct{})
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer func() { _ = r.Body.Close() }()
				var payload map[string]any
				if json.NewDecoder(r.Body).Decode(&payload) != nil {
					return
				}
				close(entered)
				select {
				case <-headers:
				case <-r.Context().Done():
					return
				}
				if scenario == "upstream-error" {
					w.WriteHeader(502)
					_, _ = io.WriteString(w, `{"error":"synthetic rejection"}`)
					return
				}
				if scenario == "non-stream" {
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"answers":{"connection_test":true}}`)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, ": heartbeat\n\ndata: {\"type\":\"response.created\"}\n\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"   \"}\n\n")
				timingFlush(w)
				close(metadata)
				select {
				case <-content:
				case <-r.Context().Done():
					return
				}
				if scenario == "metadata-only" {
					_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\"}\n\n")
					return
				}
				if scenario == "apikey-chat" {
					_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\ndata: {\"choices\":[{\"delta\":{\"content\":\"hello synthetic\"}}]}\n\n")
				} else {
					_, _ = io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello synthetic\"}\n\n")
				}
				timingFlush(w)
				select {
				case <-terminal:
				case <-r.Context().Done():
					return
				}
				if scenario == "incomplete" {
					return
				}
				if scenario == "apikey-chat" {
					_, _ = io.WriteString(w, "data: [DONE]\n\n")
				} else {
					_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\"}\n\n")
				}
			}))
			defer upstream.Close()
			target, err := url.Parse(upstream.URL)
			require.NoError(t, err)
			account := &service.Account{ID: 42, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Concurrency: 1, Credentials: map[string]any{"api_key": "synthetic-only", "base_url": "https://fixture.invalid"}, Extra: map[string]any{openai_compat.ExtraKeyResponsesSupported: true}}
			if scenario == "oauth-responses" {
				account.Type = service.AccountTypeOAuth
				account.Credentials = map[string]any{"access_token": "synthetic-only"}
			}
			if scenario == "apikey-chat" {
				account.Extra = map[string]any{openai_compat.ExtraKeyResponsesSupported: false}
			}
			if scenario == "non-stream" {
				account.Platform = service.PlatformTypeSafe
			}
			downstream := httptest.NewServer(timingRouter(account, target, requestWritten))
			defer downstream.Close()
			client := &http.Client{Timeout: 15 * time.Second}
			response, err := client.Post(downstream.URL+"/api/v1/admin/accounts/42/test", "application/json", strings.NewReader(`{"model_id":"synthetic-model"}`))
			require.NoError(t, err)
			defer func() { _ = response.Body.Close() }()
			timingWait(t, entered)
			// Server receipt alone does not synchronize net/http's asynchronous
			// WroteRequest callback. Gate delayed headers on the actual callback.
			timingWait(t, requestWritten)
			close(headers)
			stream := scenario != "upstream-error" && scenario != "non-stream"
			if stream {
				timingWait(t, metadata)
				close(content)
			}
			var raw strings.Builder
			var timings []requesttiming.AccountTestTiming
			scanner := bufio.NewScanner(response.Body)
			for scanner.Scan() {
				line := scanner.Text()
				_, _ = raw.WriteString(line + "\n")
				if !strings.HasPrefix(line, "data: ") {
					continue
				}
				var event service.TestEvent
				require.NoError(t, json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event))
				if event.Timing != nil {
					timings = append(timings, *event.Timing)
				}
				if event.Type == "content" && event.Text == "hello synthetic" {
					close(contentSeen)
					close(terminal)
				}
			}
			require.NoError(t, scanner.Err())
			require.Len(t, timings, 2)
			last := timings[1]
			require.Equal(t, "running", timings[0].Outcome)
			require.Nil(t, timings[0].FirstContent.DurationMS)
			require.NotNil(t, last.TotalMS)
			require.NotNil(t, last.FinishedAt)
			require.NotNil(t, last.ResponseHeaders.DurationMS, "response_headers=%+v", last.ResponseHeaders)
			require.NotNil(t, last.LocalProcessing.DurationMS)
			if stream && scenario != "metadata-only" {
				require.NotNil(t, last.FirstContent.DurationMS)
				require.NotNil(t, last.StreamOutput.DurationMS)
				require.GreaterOrEqual(t, *last.FirstContent.EndMS, *last.ResponseHeaders.EndMS)
				require.LessOrEqual(t, *last.StreamOutput.EndMS, *last.TotalMS)
			} else {
				require.Nil(t, last.FirstContent.DurationMS)
			}
			expected := "success"
			if scenario == "upstream-error" || scenario == "incomplete" {
				expected = "error"
			}
			require.Equal(t, expected, last.Outcome)
			encoded, err := json.Marshal(last)
			require.NoError(t, err)
			for _, private := range []string{"fixture.invalid", "synthetic-only", "127.0.0.1", "route", "proxy", "credentials"} {
				require.NotContains(t, string(encoded), private)
			}
			if dir := os.Getenv("ACCOUNT_TIMING_FIXTURES_DIR"); dir != "" {
				require.NoError(t, os.MkdirAll(dir, 0700))
				require.NoError(t, os.WriteFile(filepath.Join(dir, scenario+".sse"), []byte(raw.String()), 0600))
			}
			t.Logf("scenario=%s mode=%s outcome=%s total_ms=%.3f content_observed=%t", scenario, last.Mode, last.Outcome, *last.TotalMS, last.FirstContent.DurationMS != nil)
		})
	}
}

// Manually trigger a deadline without scheduling a short sleep. The real
// net/http transport and context propagation still perform cancellation.
type timingDeadlineContext struct {
	context.Context
	done chan struct{}
}

func (c timingDeadlineContext) Done() <-chan struct{} { return c.done }
func (c timingDeadlineContext) Err() error {
	select {
	case <-c.done:
		return context.DeadlineExceeded
	default:
		return nil
	}
}

func TestAccountConnectionTimingCancellationAndTimeout(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, phase := range []string{"headers", "body"} {
		for _, outcome := range []string{"canceled", "timeout"} {
			t.Run(phase+"/"+outcome, func(t *testing.T) {
				entered, stopped := make(chan struct{}), make(chan struct{})
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					defer close(stopped)
					_, _ = io.Copy(io.Discard, r.Body)
					_ = r.Body.Close()
					if phase == "body" {
						w.Header().Set("Content-Type", "text/event-stream")
						_, _ = io.WriteString(w, ": heartbeat\n\ndata: {\"type\":\"response.created\"}\n\n")
						timingFlush(w)
					}
					close(entered)
					select {
					case <-r.Context().Done():
					case <-time.After(10 * time.Second):
					}
				}))
				defer upstream.Close()
				target, err := url.Parse(upstream.URL)
				require.NoError(t, err)
				account := &service.Account{ID: 42, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Concurrency: 1, Credentials: map[string]any{"api_key": "synthetic-only", "base_url": "https://fixture.invalid"}, Extra: map[string]any{openai_compat.ExtraKeyResponsesSupported: true}}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				requestCtx := ctx
				trigger := cancel
				if outcome == "timeout" {
					deadline := timingDeadlineContext{Context: context.Background(), done: make(chan struct{})}
					requestCtx = deadline
					var once sync.Once
					trigger = func() { once.Do(func() { close(deadline.done) }) }
				}
				defer trigger()
				recorder := httptest.NewRecorder()
				request := httptest.NewRequest("POST", "/api/v1/admin/accounts/42/test", strings.NewReader(`{"model_id":"synthetic-model"}`)).WithContext(requestCtx)
				done := make(chan struct{})
				router := timingRouter(account, target)
				go func() { defer close(done); router.ServeHTTP(recorder, request) }()
				timingWait(t, entered)
				trigger()
				timingWait(t, done)
				timingWait(t, stopped)
				var final *requesttiming.AccountTestTiming
				for _, line := range strings.Split(recorder.Body.String(), "\n") {
					if !strings.HasPrefix(line, "data: ") {
						continue
					}
					var event service.TestEvent
					require.NoError(t, json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event))
					if event.Timing != nil && event.Timing.FinishedAt != nil {
						final = event.Timing
					}
				}
				require.NotNil(t, final)
				require.Equal(t, outcome, final.Outcome)
				require.Nil(t, final.FirstContent.DurationMS)
				require.NotContains(t, recorder.Body.String(), `"success":true`)
				// Record actual handler events from this synthetic sink. Real
				// disconnected-client delivery is tested separately below.
				if dir := os.Getenv("ACCOUNT_TIMING_FIXTURES_DIR"); dir != "" {
					require.NoError(t, os.MkdirAll(dir, 0700))
					require.NoError(t, os.WriteFile(filepath.Join(dir, outcome+"-"+phase+".sse"), []byte(recorder.Body.String()), 0600))
				}
			})
		}
	}
}

func TestAccountConnectionTimingClientDisconnect(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, phase := range []string{"headers", "body"} {
		t.Run(phase, func(t *testing.T) {
			entered, stopped, handlerDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(stopped)
				_, _ = io.Copy(io.Discard, r.Body)
				_ = r.Body.Close()
				if phase == "body" {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello synthetic\"}\n\n")
					timingFlush(w)
				}
				close(entered)
				select {
				case <-r.Context().Done():
				case <-time.After(10 * time.Second):
				}
			}))
			defer upstream.Close()
			target, err := url.Parse(upstream.URL)
			require.NoError(t, err)
			account := &service.Account{ID: 42, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Concurrency: 1, Credentials: map[string]any{"api_key": "synthetic-only", "base_url": "https://fixture.invalid"}, Extra: map[string]any{openai_compat.ExtraKeyResponsesSupported: true}}
			router := timingRouter(account, target)
			downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { defer close(handlerDone); router.ServeHTTP(w, r) }))
			defer downstream.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			request, err := http.NewRequestWithContext(ctx, "POST", downstream.URL+"/api/v1/admin/accounts/42/test", strings.NewReader(`{"model_id":"synthetic-model"}`))
			require.NoError(t, err)
			response, err := (&http.Client{Timeout: 15 * time.Second}).Do(request)
			require.NoError(t, err)
			timingWait(t, entered)
			cancel()
			_ = response.Body.Close()
			timingWait(t, handlerDone)
			timingWait(t, stopped)
		})
	}
}

func TestAccountConnectionTimingConcurrentRequests(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Model string `json:"model"`
		}
		if json.NewDecoder(r.Body).Decode(&payload) != nil {
			w.WriteHeader(400)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		data, _ := json.Marshal(map[string]any{"type": "response.output_text.delta", "delta": payload.Model})
		_, _ = fmt.Fprintf(w, "data: %s\n\ndata: {\"type\":\"response.completed\"}\n\n", data)
	}))
	defer upstream.Close()
	target, err := url.Parse(upstream.URL)
	require.NoError(t, err)
	account := &service.Account{ID: 42, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Concurrency: 8, Credentials: map[string]any{"api_key": "synthetic-only", "base_url": "https://fixture.invalid"}, Extra: map[string]any{openai_compat.ExtraKeyResponsesSupported: true}}
	downstream := httptest.NewServer(timingRouter(account, target))
	defer downstream.Close()
	results := make(chan error, 8)
	for i := range 8 {
		go func() {
			model := fmt.Sprintf("synthetic-%d", i)
			response, err := (&http.Client{Timeout: 15 * time.Second}).Post(downstream.URL+"/api/v1/admin/accounts/42/test", "application/json", strings.NewReader(fmt.Sprintf(`{"model_id":%q}`, model)))
			if err != nil {
				results <- err
				return
			}
			defer func() { _ = response.Body.Close() }()
			body, err := io.ReadAll(response.Body)
			if err != nil {
				results <- err
				return
			}
			var text strings.Builder
			var final *requesttiming.AccountTestTiming
			for _, line := range strings.Split(string(body), "\n") {
				if !strings.HasPrefix(line, "data: ") {
					continue
				}
				var event service.TestEvent
				if err = json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event); err != nil {
					results <- err
					return
				}
				if event.Type == "content" {
					_, _ = text.WriteString(event.Text)
				}
				if event.Timing != nil && event.Timing.FinishedAt != nil {
					final = event.Timing
				}
			}
			if text.String() != model || final == nil || final.Outcome != "success" || final.FirstContent.DurationMS == nil {
				results <- fmt.Errorf("request %s lost its own content/timing", model)
				return
			}
			results <- nil
		}()
	}
	for range 8 {
		require.NoError(t, <-results)
	}
}

package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestReadGuardResultReturnsWithoutEOF(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = io.WriteString(w, `{"type":"result","payload":{"status":"active"}}`)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-r.Context().Done()
	}))
	defer server.Close()

	svc := &AccountTokenGuardService{httpClient: server.Client()}
	cfg := defaultAccountTokenGuardConfig()
	cfg.ProbeEndpoint = server.URL
	cfg.ProbeTimeoutSeconds = 5
	account := &Account{ID: 1, Name: "test@example.com", Type: AccountTypeOAuth, Platform: PlatformOpenAI,
		Credentials: map[string]any{"access_token": "access-token-canary"}}

	started := time.Now()
	result := svc.probe(context.Background(), cfg, account)
	if result.State != AccountTokenGuardProbeOK {
		t.Fatalf("state = %q, detail = %s", result.State, result.Detail)
	}
	if time.Since(started) > time.Second {
		t.Fatalf("probe waited for EOF instead of returning on result")
	}
	if strings.Contains(result.Detail, "access-token-canary") || strings.Contains(result.Detail, server.URL) {
		t.Fatalf("diagnostic leaked sensitive data: %s", result.Detail)
	}
}

func TestProbeClassifiesHTTP401AsTransient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, "password=secret-response-body")
	}))
	defer server.Close()

	svc := &AccountTokenGuardService{httpClient: server.Client()}
	cfg := defaultAccountTokenGuardConfig()
	cfg.ProbeEndpoint = server.URL
	account := &Account{ID: 1, Name: "test@example.com", Type: AccountTypeOAuth, Platform: PlatformOpenAI,
		Credentials: map[string]any{"access_token": "token"}}

	result := svc.probe(context.Background(), cfg, account)
	if result.State != AccountTokenGuardProbeTransient {
		t.Fatalf("state = %q, want transient", result.State)
	}
	if result.Diagnostic.Code != "upstream_http_error" || result.Diagnostic.HTTPStatus != http.StatusUnauthorized {
		t.Fatalf("diagnostic = %+v", result.Diagnostic)
	}
	if strings.Contains(result.Detail, "secret-response-body") || strings.Contains(result.Detail, "password") {
		t.Fatalf("diagnostic leaked response body: %s", result.Detail)
	}
}

func TestProbeOnlyExplicitProtocolCodeTriggersRelogin(t *testing.T) {
	tests := []struct {
		name  string
		body  string
		state string
	}{
		{"explicit auth", `{"type":"result","payload":{"status":"failed","error":{"code":"invalid_token","message":"do-not-log"}}}`, AccountTokenGuardProbeAuth},
		{"provider unauthorized text", `{"type":"result","payload":{"status":"failed","error":{"code":"provider_unauthorized","message":"token expired"}}}`, AccountTokenGuardProbeTransient},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, tt.body)
			}))
			defer server.Close()
			svc := &AccountTokenGuardService{httpClient: server.Client()}
			cfg := defaultAccountTokenGuardConfig()
			cfg.ProbeEndpoint = server.URL
			account := &Account{ID: 1, Name: "test@example.com", Type: AccountTypeOAuth, Platform: PlatformOpenAI,
				Credentials: map[string]any{"access_token": "token"}}
			result := svc.probe(context.Background(), cfg, account)
			if result.State != tt.state {
				t.Fatalf("state = %q, want %q; detail=%s", result.State, tt.state, result.Detail)
			}
			if strings.Contains(result.Detail, "do-not-log") || strings.Contains(result.Detail, "token expired") {
				t.Fatalf("diagnostic leaked error message: %s", result.Detail)
			}
		})
	}
}

func TestReadGuardResultDistinguishesIdleMalformedAndOversize(t *testing.T) {
	t.Run("idle", func(t *testing.T) {
		body := &blockingGuardBody{}
		_, _, err := readGuardResult(context.Background(), body, 100, 20*time.Millisecond)
		if err == nil || !strings.Contains(err.Error(), "idle timeout") {
			t.Fatalf("err = %v, want idle timeout", err)
		}
	})
	t.Run("malformed", func(t *testing.T) {
		_, _, err := readGuardResult(context.Background(), io.NopCloser(strings.NewReader(`{"type":"result"`)), 100, time.Second)
		if err == nil || !strings.Contains(err.Error(), "malformed") {
			t.Fatalf("err = %v, want malformed", err)
		}
	})
	t.Run("oversize", func(t *testing.T) {
		_, _, err := readGuardResult(context.Background(), io.NopCloser(strings.NewReader(`{"message":"`+strings.Repeat("x", 200)+`"}`)), 32, time.Second)
		if err == nil || !strings.Contains(err.Error(), "too large") {
			t.Fatalf("err = %v, want too large", err)
		}
	})
}

func TestRunCycleIgnoresCallerCancellationAfterAcceptance(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(60 * time.Millisecond)
		_, _ = io.WriteString(w, `{"status":"active"}`)
	}))
	defer server.Close()

	repo := &guardMemoryRepo{}
	accounts := &guardMemoryAccounts{items: []Account{{ID: 1, Name: "test@example.com", Status: StatusActive, Type: AccountTypeOAuth, Platform: PlatformOpenAI,
		Credentials: map[string]any{"access_token": "token"}}}}
	svc := NewAccountTokenGuardService(nil, repo, accounts, nil, nil)
	cfg := defaultAccountTokenGuardConfig()
	cfg.ProbeEndpoint = server.URL
	cfg.ProbeConcurrency = 1
	cfg.MaxProbePerCycle = 1
	cfg.ProbeTimeoutSeconds = 5
	svc.config.Store(cfg)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	stats, err := svc.RunCycle(ctx, false)
	if err != nil {
		t.Fatalf("RunCycle error after caller cancellation: %v", err)
	}
	if stats.Healthy != 1 || repo.upserts == 0 {
		t.Fatalf("stats=%+v upserts=%d", stats, repo.upserts)
	}
}

func TestStartRunDeduplicatesAndCanCancel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()

	repo := &guardMemoryRepo{}
	accounts := &guardMemoryAccounts{items: []Account{{ID: 1, Name: "test@example.com", Status: StatusActive, Type: AccountTypeOAuth, Platform: PlatformOpenAI,
		Credentials: map[string]any{"access_token": "token"}}}}
	svc := NewAccountTokenGuardService(nil, repo, accounts, nil, nil)
	cfg := defaultAccountTokenGuardConfig()
	cfg.ProbeEndpoint = server.URL
	cfg.ProbeConcurrency = 1
	cfg.MaxProbePerCycle = 1
	cfg.ProbeTimeoutSeconds = 5
	svc.config.Store(cfg)

	first, err := svc.StartRun(true)
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.StartRun(true)
	if !errors.Is(err, ErrAccountTokenGuardRunning) || second == nil || second.ID != first.ID {
		t.Fatalf("duplicate start = job=%+v err=%v", second, err)
	}
	if _, err := svc.CancelRun(first.ID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		job, ok := svc.Job(first.ID)
		if ok && job.Status == AccountTokenGuardJobCanceled {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	job, _ := svc.Job(first.ID)
	t.Fatalf("job did not cancel: %+v", job)
}

func TestRunCycleReportsPersistenceErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"status":"active"}`)
	}))
	defer server.Close()

	repo := &guardMemoryRepo{upsertErr: errors.New("store unavailable")}
	accounts := &guardMemoryAccounts{items: []Account{{ID: 1, Name: "test@example.com", Status: StatusActive, Type: AccountTypeOAuth, Platform: PlatformOpenAI,
		Credentials: map[string]any{"access_token": "token"}}}}
	svc := NewAccountTokenGuardService(nil, repo, accounts, nil, nil)
	cfg := defaultAccountTokenGuardConfig()
	cfg.ProbeEndpoint = server.URL
	cfg.ProbeConcurrency = 1
	cfg.MaxProbePerCycle = 1
	cfg.ProbeTimeoutSeconds = 5
	svc.config.Store(cfg)

	stats, err := svc.RunCycle(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if stats.PersistenceErrors == 0 || stats.Failed == 0 {
		t.Fatalf("stats did not expose persistence failure: %+v", stats)
	}
}

type blockingGuardBody struct {
	mu   sync.Mutex
	once sync.Once
	done chan struct{}
}

func (b *blockingGuardBody) Read([]byte) (int, error) {
	b.mu.Lock()
	if b.done == nil {
		b.done = make(chan struct{})
	}
	done := b.done
	b.mu.Unlock()
	<-done
	return 0, io.EOF
}

func (b *blockingGuardBody) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.done == nil {
		b.done = make(chan struct{})
	}
	b.once.Do(func() { close(b.done) })
	return nil
}

type guardMemoryRepo struct {
	mu        sync.Mutex
	states    []AccountTokenGuardState
	upserts   int
	upsertErr error
}

func (r *guardMemoryRepo) UpsertState(_ context.Context, state AccountTokenGuardState) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.upserts++
	if r.upsertErr != nil {
		return r.upsertErr
	}
	for i, previous := range r.states {
		if previous.AccountID != state.AccountID {
			continue
		}
		if state.LastFixAt == nil {
			state.LastFixAt = previous.LastFixAt
			state.LastFixAction = previous.LastFixAction
			state.LastFixResult = previous.LastFixResult
		}
		r.states[i] = state
		return nil
	}
	r.states = append(r.states, state)
	return nil
}
func (r *guardMemoryRepo) DeleteStatesExcept(context.Context, []int64) error { return nil }
func (r *guardMemoryRepo) RecordEvent(context.Context, AccountTokenGuardEvent) error {
	return nil
}
func (r *guardMemoryRepo) ListEvents(context.Context, int, int) ([]AccountTokenGuardEvent, error) {
	return nil, nil
}
func (r *guardMemoryRepo) ListStates(context.Context) ([]AccountTokenGuardState, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]AccountTokenGuardState(nil), r.states...), nil
}
func (r *guardMemoryRepo) PruneEvents(context.Context, time.Time) error { return nil }

type guardMemoryAccounts struct{ items []Account }

func (a *guardMemoryAccounts) GetByID(context.Context, int64) (*Account, error) {
	return nil, errors.New("not implemented")
}
func (a *guardMemoryAccounts) ListAllWithFilters(context.Context, string, string, string, string, int64, string) ([]Account, error) {
	return a.items, nil
}
func (a *guardMemoryAccounts) ClearError(context.Context, int64) error           { return nil }
func (a *guardMemoryAccounts) SetSchedulable(context.Context, int64, bool) error { return nil }

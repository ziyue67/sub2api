package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/redis/go-redis/v9"
)

// Lifecycle counts handlers, including hijacked WebSocket handlers, without
// wrapping ResponseWriter and losing its streaming or hijacking interfaces.
type Lifecycle struct {
	onDrain      []func()
	mu           sync.Mutex
	draining     bool
	active       int
	idle         chan struct{}
	checks       []func(context.Context) error
	probeMu      sync.Mutex
	probe        *readinessProbe
	probeTimeout time.Duration
}

func NewLifecycle(checks ...func(context.Context) error) *Lifecycle {
	return newLifecycleWithTimeout(time.Second, checks...)
}

// Both HTTP probes and internal readiness callers share this dependency budget.
// It is immutable after construction and does not affect liveness or draining.
func newLifecycleWithTimeout(timeout time.Duration, checks ...func(context.Context) error) *Lifecycle {
	if timeout <= 0 {
		timeout = time.Second
	}
	return &Lifecycle{idle: make(chan struct{}), checks: checks, probeTimeout: timeout}
}

func ProvideLifecycle(cfg *config.Config, db *sql.DB, cache *redis.Client) *Lifecycle {
	timeout := time.Second
	if cfg != nil && cfg.Server.ReadinessTimeoutSeconds > 0 {
		timeout = time.Duration(cfg.Server.ReadinessTimeoutSeconds) * time.Second
	}
	return newLifecycleWithTimeout(timeout, func(ctx context.Context) error {
		if db == nil || cache == nil {
			return errors.New("readiness dependencies unavailable")
		}
		if err := db.PingContext(ctx); err != nil {
			return err
		}
		return cache.Ping(ctx).Err()
	})
}

// BeginDrain immediately withdraws readiness and rejects new business requests.
// Existing handlers retain their contexts until completion or the process's
// configured shutdown deadline. Repeated signals are harmless.
func (l *Lifecycle) BeginDrain() {
	l.mu.Lock()
	if l.draining {
		l.mu.Unlock()
		return
	}
	l.draining = true
	if l.active == 0 {
		close(l.idle)
	}
	callbacks := append([]func(){}, l.onDrain...)
	l.mu.Unlock()
	for _, stop := range callbacks {
		stop()
	}
}

func (l *Lifecycle) Wait(ctx context.Context) error {
	select {
	case <-l.idle:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (l *Lifecycle) isDraining() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.draining
}

func (l *Lifecycle) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/readyz" {
			l.serveReadiness(w, r)
			return
		}
		// Liveness must not depend on Redis/PostgreSQL or change during draining.
		if r.URL.Path == "/health" {
			next.ServeHTTP(w, r)
			return
		}
		l.mu.Lock()
		if l.draining {
			l.mu.Unlock()
			w.Header().Set("Retry-After", "1")
			writeLifecycleStatus(w, http.StatusServiceUnavailable, "draining")
			return
		}
		l.active++
		l.mu.Unlock()
		defer func() {
			l.mu.Lock()
			defer l.mu.Unlock()
			l.active--
			if l.draining && l.active == 0 {
				close(l.idle)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (l *Lifecycle) serveReadiness(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeLifecycleStatus(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if l.isDraining() {
		writeLifecycleStatus(w, http.StatusServiceUnavailable, "draining")
		return
	}
	if err := l.checkWithinBudget(r.Context()); err != nil {
		writeLifecycleStatus(w, http.StatusServiceUnavailable, "not_ready")
		return
	}
	if l.isDraining() {
		writeLifecycleStatus(w, http.StatusServiceUnavailable, "draining")
		return
	}
	writeLifecycleStatus(w, http.StatusOK, "ready")
}

func writeLifecycleStatus(w http.ResponseWriter, code int, status string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": status})
}

// SetupHandler prevents the embedded SPA from making an unconfigured Pod ready.
func SetupHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/readyz" {
			writeLifecycleStatus(w, http.StatusServiceUnavailable, "needs_setup")
			return
		}
		if r.URL.Path == "/health" {
			writeLifecycleStatus(w, http.StatusOK, "ok")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Some dependency clients use their own socket deadlines rather than ctx.
// Share one bounded check across overlapping probes. A caller's cancellation
// must neither create another stuck goroutine nor cancel its peers' check.
type readinessProbe struct {
	done chan struct{}
	err  error
}

func (l *Lifecycle) checkWithinBudget(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, l.probeTimeout)
	defer cancel()
	l.probeMu.Lock()
	probe := l.probe
	if probe == nil {
		probe = &readinessProbe{done: make(chan struct{})}
		l.probe = probe
		go func() {
			probeCtx, cancel := context.WithTimeout(context.Background(), l.probeTimeout)
			defer cancel()
			err := l.checkDependencies(probeCtx)
			l.probeMu.Lock()
			probe.err = err
			l.probe = nil
			close(probe.done)
			l.probeMu.Unlock()
		}()
	}
	l.probeMu.Unlock()
	select {
	case <-probe.done:
		return probe.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (l *Lifecycle) checkDependencies(ctx context.Context) (err error) {
	defer func() {
		if recover() != nil {
			err = errors.New("readiness check failed")
		}
	}()
	for _, check := range l.checks {
		if err = ctx.Err(); err != nil {
			return err
		}
		if err = check(ctx); err != nil {
			return err
		}
	}
	return ctx.Err()
}

package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestIssue578ReviewQueryDownstreamCannotRestampWindows(t *testing.T) {
	for _, path := range []string{"auto reset", "post reset"} {
		for _, missing := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/missing=%t", path, missing), func(t *testing.T) {
				a := issue578Account()
				r := &issue578Repo{accounts: map[int64]*Account{a.ID: a}}
				entered, release := make(chan struct{}), make(chan struct{})
				var once sync.Once
				unblock := func() { once.Do(func() { close(release) }) }
				t.Cleanup(unblock)
				q := issue578Quota(t, r, func(w http.ResponseWriter, request *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					if request.URL.Path == "/backend-api/wham/usage" {
						body := strings.ReplaceAll(issue578UsageBody, `"used_percent":100`, `"used_percent":89`)
						if missing {
							body = strings.ReplaceAll(body, `"used_percent":89,`, "")
						}
						_, _ = w.Write([]byte(body))
						return
					}
					close(entered)
					<-release
					_, _ = w.Write([]byte(`{"available_count":0}`))
				})
				type result struct {
					usage *OpenAIQuotaUsage
					err   error
				}
				done := make(chan result, 1)
				go func() { u, err := q.QueryUsage(context.Background(), a.ID); done <- result{u, err} }()
				select {
				case <-entered:
				case <-time.After(time.Second):
					t.Fatal("query did not reach details seam")
				}
				s := &OpenAIGatewayService{accountRepo: r, codexSnapshotThrottle: newAccountWriteThrottle(30 * time.Millisecond)}
				t.Cleanup(s.CloseOpenAIWSPool)
				s.UpdateCodexUsageSnapshotFromHeaders(context.Background(), a.ID, excelBPSQuotaHeaders("100", "20"))
				issue578WaitUsage(t, r, a.ID, 100)
				unblock()
				got := <-done
				require.NoError(t, got.err)
				if path == "auto reset" {
					auto := &OpenAIQuotaAutoResetService{accountRepo: r, quota: q}
					require.NoError(t, auto.persistFreshUsage(context.Background(), a.ID, got.usage, time.Now()))
				} else {
					require.NoError(t, q.CachePostResetSnapshot(context.Background(), a.ID, got.usage))
				}
				current, _ := r.GetByID(context.Background(), a.ID)
				require.Equal(t, 100.0, current.Extra["codex_5h_used_percent"], "downstream must retain original observation and presence, never restamp the DTO")
			})
		}
	}
}

type issue578LifecycleRepo struct {
	AccountRepository
	calls atomic.Int32
	write func(context.Context) error
}

func (r *issue578LifecycleRepo) UpdateExtra(ctx context.Context, _ int64, _ map[string]any) error {
	r.calls.Add(1)
	return r.write(ctx)
}

func TestIssue578ReviewWorkerPermanentErrorAndShutdown(t *testing.T) {
	for _, mode := range []string{"deleted", "shutdown waiting", "shutdown in flight"} {
		t.Run(mode, func(t *testing.T) {
			entered, cancelled := make(chan struct{}), make(chan struct{})
			var once sync.Once
			r := &issue578LifecycleRepo{write: func(ctx context.Context) error {
				once.Do(func() { close(entered) })
				if mode == "deleted" {
					return ErrAccountNotFound
				}
				if mode == "shutdown in flight" {
					<-ctx.Done()
					close(cancelled)
					return ctx.Err()
				}
				return errors.New("transient synthetic failure; must not log opaque upstream text")
			}}
			s := &OpenAIGatewayService{accountRepo: r, codexSnapshotThrottle: newAccountWriteThrottle(40 * time.Millisecond)}
			s.UpdateCodexUsageSnapshotFromHeaders(context.Background(), 300, excelBPSQuotaHeaders("100", "20"))
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("writer did not start")
			}
			if mode != "deleted" {
				closed := make(chan struct{})
				go func() { s.CloseOpenAIWSPool(); close(closed) }()
				select {
				case <-closed:
				case <-time.After(time.Second):
					t.Fatal("shutdown did not cancel snapshot worker")
				}
				if mode == "shutdown in flight" {
					select {
					case <-cancelled:
					case <-time.After(time.Second):
						t.Fatal("in-flight context was not cancelled")
					}
				}
			}
			time.Sleep(130 * time.Millisecond)
			require.EqualValues(t, 1, r.calls.Load(), "deleted/closed services must not retry forever")
			s.codexSnapshotThrottle.mu.Lock()
			pending := len(s.codexSnapshotThrottle.pending)
			s.codexSnapshotThrottle.mu.Unlock()
			require.Zero(t, pending, "terminal workers must release pending state")
			s.CloseOpenAIWSPool()
		})
	}
}

func TestIssue578ReviewActualAutoResetAndPostResetWorkflow(t *testing.T) {
	for _, path := range []string{"auto reset", "post reset"} {
		for _, stage := range []string{"usage rejected", "details delayed", "missing usage"} {
			t.Run(path+"/"+stage, func(t *testing.T) {
				a := issue578Account()
				a.Extra[OpenAIAutoResetCreditEnabledExtraKey] = true
				a.Extra[OpenAIAutoResetCredit5hThresholdExtraKey] = 0.95
				r := &issue578Repo{accounts: map[int64]*Account{a.ID: a}}
				entered, release := make(chan struct{}), make(chan struct{})
				var once sync.Once
				unblock := func() { once.Do(func() { close(release) }) }
				t.Cleanup(unblock)
				var consume atomic.Int32
				q := issue578Quota(t, r, func(w http.ResponseWriter, request *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					if request.Method != http.MethodGet {
						consume.Add(1)
						t.Error("workflow must not consume a reset card in this fixture")
						w.WriteHeader(500)
						return
					}
					if request.URL.Path == "/backend-api/wham/usage" {
						if stage == "usage rejected" {
							close(entered)
							<-release
						}
						body := strings.ReplaceAll(issue578UsageBody, `"used_percent":100`, `"used_percent":89`)
						if stage == "missing usage" {
							body = strings.ReplaceAll(body, `"used_percent":89,`, "")
						}
						_, _ = w.Write([]byte(body))
						return
					}
					if stage != "usage rejected" {
						close(entered)
						<-release
					}
					_, _ = w.Write([]byte(`{"available_count":0}`))
				})
				done := make(chan error, 1)
				if path == "auto reset" {
					auto := &OpenAIQuotaAutoResetService{accountRepo: r, quota: q}
					go func() { done <- auto.evaluateAccount(context.Background(), a.ID) }()
				} else {
					go func() {
						result := RunOpenAIQuotaResetPostProcess(context.Background(), a.ID, q, autoResetTestRecoverer{}, r.GetByID)
						if result.WarningCode != "" {
							done <- fmt.Errorf("unexpected warning %s", result.WarningCode)
							return
						}
						done <- nil
					}()
				}
				select {
				case <-entered:
				case <-time.After(time.Second):
					t.Fatal("workflow did not reach local query seam")
				}
				if stage != "missing usage" {
					s := &OpenAIGatewayService{accountRepo: r, codexSnapshotThrottle: newAccountWriteThrottle(30 * time.Millisecond)}
					t.Cleanup(s.CloseOpenAIWSPool)
					s.UpdateCodexUsageSnapshotFromHeaders(context.Background(), a.ID, excelBPSQuotaHeaders("100", "20"))
					issue578WaitUsage(t, r, a.ID, 100)
				}
				unblock()
				select {
				case err := <-done:
					require.NoError(t, err)
				case <-time.After(time.Second):
					t.Fatal("workflow did not finish")
				}
				current, _ := r.GetByID(context.Background(), a.ID)
				want := 100.0
				if stage == "missing usage" {
					want = 89
				}
				require.Equal(t, want, current.Extra["codex_5h_used_percent"])
				require.Zero(t, consume.Load())
			})
		}
	}
}

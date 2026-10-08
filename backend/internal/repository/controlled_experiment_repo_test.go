package repository

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// Run against an isolated PostgreSQL database, never the application's tables.
// A unique schema also separates concurrent test processes on that database.
func TestControlledExperimentPostgresBudgetAndRecovery(t *testing.T) {
	dsn := os.Getenv("SUB2API_EXPERIMENT_TEST_DSN")
	if dsn == "" {
		t.Skip("isolated PostgreSQL DSN not supplied")
	}
	u, err := url.Parse(dsn)
	require.NoError(t, err)
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	ctx := context.Background()
	require.NoError(t, db.PingContext(ctx))
	schema := fmt.Sprintf("controlled_experiment_test_%d", time.Now().UnixNano())
	_, err = db.ExecContext(ctx, "CREATE SCHEMA "+schema)
	require.NoError(t, err)
	t.Cleanup(func() { _, err := db.ExecContext(ctx, "DROP SCHEMA "+schema+" CASCADE"); require.NoError(t, err) })
	query := u.Query()
	query.Set("search_path", schema)
	u.RawQuery = query.Encode()
	isolated, err := sql.Open("postgres", u.String())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, isolated.Close()) })
	migration, err := os.ReadFile(filepath.Join("..", "..", "migrations", "268_controlled_experiments.sql"))
	require.NoError(t, err)
	_, err = isolated.ExecContext(ctx, string(migration))
	require.NoError(t, err)
	r := NewControlledExperimentRepository(isolated)
	run, err := r.Create(ctx, &service.ControlledExperiment{Name: "budget", MaxCalls: 3, Spec: service.ControlledExperimentSpec{Model: "frozen", Routes: []service.ControlledRoute{{AccountID: 1, Channel: "native_http"}}}})
	require.NoError(t, err)
	run.Spec.Model = "changed locally"
	saved, err := r.Get(ctx, run.ID)
	require.NoError(t, err)
	require.Equal(t, "frozen", saved.Spec.Model)
	var wg sync.WaitGroup
	var mu sync.Mutex
	claimed := 0
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := r.Claim(ctx, run.ID)
			require.NoError(t, err)
			if ok {
				mu.Lock()
				claimed++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	require.Equal(t, 1, claimed)
	other, err := r.Create(ctx, &service.ControlledExperiment{Name: "other", MaxCalls: 5, Spec: saved.Spec})
	require.NoError(t, err)
	ok, err := r.Claim(ctx, other.ID)
	require.ErrorIs(t, err, service.ErrExperimentBusy)
	require.False(t, ok)
	var successful []*service.ControlledAttempt
	for i := 0; i < 15; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a := &service.ControlledAttempt{RunID: run.ID, RouteIndex: 0, Phase: "task", TaskID: "fixed", Turn: 1}
			err := r.Reserve(ctx, a)
			if err != nil {
				require.ErrorIs(t, err, service.ErrExperimentBudget)
				return
			}
			mu.Lock()
			successful = append(successful, a)
			mu.Unlock()
		}()
	}
	wg.Wait()
	require.Len(t, successful, 3)
	saved, err = r.Get(ctx, run.ID)
	require.NoError(t, err)
	require.Equal(t, 3, saved.ReservedCalls)
	attempts, err := r.Attempts(ctx, run.ID)
	require.NoError(t, err)
	require.Len(t, attempts, 3)
	for i, a := range attempts {
		require.Equal(t, i+1, a.Sequence)
		require.Equal(t, "reserved", a.Status)
	}
	_, err = isolated.ExecContext(ctx, "UPDATE controlled_experiments SET lease_until=NOW()-INTERVAL '1 minute' WHERE id=$1", run.ID)
	require.NoError(t, err)
	require.NoError(t, r.RecoverExpired(ctx))
	saved, err = r.Get(ctx, run.ID)
	require.NoError(t, err)
	require.Equal(t, "interrupted", saved.Status)
	require.Equal(t, 3, saved.ReservedCalls)
	attempts, err = r.Attempts(ctx, run.ID)
	require.NoError(t, err)
	for _, a := range attempts {
		require.Equal(t, "unknown", a.Status)
		require.True(t, a.Diagnostic.CostIncomplete)
	}
	ok, err = r.Claim(ctx, run.ID)
	require.NoError(t, err)
	require.False(t, ok, "an interrupted run must never be replayed")
	ok, err = r.Claim(ctx, other.ID)
	require.NoError(t, err)
	require.True(t, ok)
	a := &service.ControlledAttempt{RunID: other.ID, RouteIndex: 0, Phase: "task", TaskID: "fixed", Turn: 1}
	require.NoError(t, r.Reserve(ctx, a))
	ok, err = r.RequestStop(ctx, other.ID)
	require.NoError(t, err)
	require.True(t, ok)
	a.Status = "completed"
	a.Answer = "finished current call"
	require.NoError(t, r.Resolve(ctx, a))
	require.ErrorIs(t, r.Reserve(ctx, &service.ControlledAttempt{RunID: other.ID}), service.ErrExperimentStopped)
	require.NoError(t, r.Finish(ctx, other.ID, "cancelled", "administrator_stop"))
	attempts, err = r.Attempts(ctx, other.ID)
	require.NoError(t, err)
	require.Equal(t, "completed", attempts[0].Status)
	require.Equal(t, "finished current call", attempts[0].Answer)
	require.ErrorIs(t, r.Resolve(ctx, a), service.ErrExperimentStopped, "resolved records cannot be rewritten")
}

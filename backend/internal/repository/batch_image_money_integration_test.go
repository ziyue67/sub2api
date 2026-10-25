//go:build integration

package repository

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// Real NUMERIC columns and simultaneous jobs, rather than a float-only mock.
func TestBatchImageMoneyConcurrentJobsConserveBalance(t *testing.T) {
	ctx := context.Background()
	db, client := newBillingAuditDatabase(t)
	user := mustCreateUser(t, client, &service.User{Email: uuid.NewString() + "@example.com", PasswordHash: "hash", Balance: 100})
	repo := NewUsageBillingRepository(nil, db)
	amounts := []float64{0.07619951, 0.07621186}
	jobs := make([]string, len(amounts))
	var wg sync.WaitGroup
	errs := make(chan error, len(amounts))
	for i, amount := range amounts {
		jobs[i] = uuid.NewString()
		wg.Add(1)
		go func(id string, hold float64) {
			defer wg.Done()
			_, err := repo.ReserveBatchImageBalance(ctx, &service.BatchImageBalanceHoldCommand{
				RequestID: service.BatchImageHoldRequestID(id), APIKeyID: 17, UserID: user.ID, BatchID: id, HoldAmount: hold, MonetaryVersion: 2,
			})
			errs <- err
		}(jobs[i], amount)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	// Capture one job, refund the other, and replay both operations.
	for attempt := 0; attempt < 2; attempt++ {
		_, err := repo.CaptureBatchImageBalance(ctx, &service.BatchImageBalanceHoldCommand{
			RequestID: service.BatchImageCaptureRequestID(jobs[0]), APIKeyID: 17, UserID: user.ID, BatchID: jobs[0], HoldAmount: amounts[0], ActualAmount: 0.05, MonetaryVersion: 2,
		})
		require.NoError(t, err)
		_, err = repo.ReleaseBatchImageBalance(ctx, &service.BatchImageBalanceHoldCommand{
			RequestID: service.BatchImageReleaseRequestID(jobs[0]), APIKeyID: 17, UserID: user.ID, BatchID: jobs[0], HoldAmount: amounts[0], MonetaryVersion: 2,
		})
		require.ErrorIs(t, err, service.ErrUsageBillingRequestConflict, "capture must not be refunded out of another job's frozen funds")
		_, err = repo.ReleaseBatchImageBalance(ctx, &service.BatchImageBalanceHoldCommand{
			RequestID: service.BatchImageReleaseRequestID(jobs[1]), APIKeyID: 17, UserID: user.ID, BatchID: jobs[1], HoldAmount: amounts[1], MonetaryVersion: 2,
		})
		require.NoError(t, err)
	}
	var balance, frozen string
	require.NoError(t, db.QueryRowContext(ctx, "SELECT balance::text,frozen_balance::text FROM users WHERE id=$1", user.ID).Scan(&balance, &frozen))
	require.Equal(t, "99.95000000", balance)
	require.Equal(t, "0.00000000", frozen)
	// A legacy ambiguous hold must not consume money belonging to another job.
	_, err := repo.ReleaseBatchImageBalance(ctx, &service.BatchImageBalanceHoldCommand{
		RequestID: fmt.Sprintf("legacy:%s", jobs[0]), APIKeyID: 17, UserID: user.ID, BatchID: jobs[0], HoldAmount: 0.0761995125,
	})
	require.ErrorIs(t, err, service.ErrBatchImageLegacyMoneyPrecision)
}

func TestBatchImageOwnedMoneyCaptureFailureRollsBackAndFreeJobsStayFree(t *testing.T) {
	ctx := context.Background()
	db, client := newBillingAuditDatabase(t)
	user := mustCreateUser(t, client, &service.User{Email: uuid.NewString() + "@example.com", PasswordHash: "hash", Balance: 100})
	repo := NewUsageBillingRepository(nil, db)
	id := uuid.NewString()
	_, err := repo.ReserveBatchImageBalance(ctx, &service.BatchImageBalanceHoldCommand{RequestID: service.BatchImageHoldRequestID(id),
		APIKeyID: 17, UserID: user.ID, BatchID: id, HoldAmount: 1, MonetaryVersion: 2})
	require.NoError(t, err)
	name := "hold_fail_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = db.ExecContext(ctx, fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$
	BEGIN IF NEW.batch_id='%s' THEN RAISE EXCEPTION 'injected receipt failure'; END IF; RETURN NEW; END; $$;
	CREATE TRIGGER %s BEFORE UPDATE ON batch_image_money_holds FOR EACH ROW EXECUTE FUNCTION %s()`, name, id, name, name))
	require.NoError(t, err)
	cleanup := func() {
		_, _ = db.ExecContext(ctx, fmt.Sprintf("DROP TRIGGER IF EXISTS %s ON batch_image_money_holds; DROP FUNCTION IF EXISTS %s()", name, name))
	}
	t.Cleanup(cleanup)
	capture := &service.BatchImageBalanceHoldCommand{RequestID: service.BatchImageCaptureRequestID(id), APIKeyID: 17,
		UserID: user.ID, BatchID: id, HoldAmount: 1, ActualAmount: 0.5, MonetaryVersion: 2}
	_, err = repo.CaptureBatchImageBalance(ctx, capture)
	require.Error(t, err)
	var balance, frozen string
	require.NoError(t, db.QueryRowContext(ctx, "SELECT balance::text,frozen_balance::text FROM users WHERE id=$1", user.ID).Scan(&balance, &frozen))
	require.Equal(t, "99.00000000", balance)
	require.Equal(t, "1.00000000", frozen)
	cleanup()
	_, err = repo.CaptureBatchImageBalance(ctx, capture)
	require.NoError(t, err)
	free := uuid.NewString()
	_, err = repo.CaptureBatchImageBalance(ctx, &service.BatchImageBalanceHoldCommand{RequestID: service.BatchImageCaptureRequestID(free),
		APIKeyID: 17, UserID: user.ID, BatchID: free, MonetaryVersion: 2})
	require.NoError(t, err)
	require.NoError(t, db.QueryRowContext(ctx, "SELECT balance::text,frozen_balance::text FROM users WHERE id=$1", user.ID).Scan(&balance, &frozen))
	require.Equal(t, "99.50000000", balance)
	require.Equal(t, "0.00000000", frozen)
}

func TestBatchImageSameTaskCaptureReleaseCompeteWithoutTakingOtherHold(t *testing.T) {
	ctx := context.Background()
	db, client := newBillingAuditDatabase(t)
	user := mustCreateUser(t, client, &service.User{Email: uuid.NewString() + "@example.com", PasswordHash: "hash", Balance: 100})
	repo := NewUsageBillingRepository(nil, db)
	batch, other := uuid.NewString(), uuid.NewString()
	for id, amount := range map[string]float64{batch: 1, other: 2} {
		_, err := repo.ReserveBatchImageBalance(ctx, &service.BatchImageBalanceHoldCommand{RequestID: service.BatchImageHoldRequestID(id),
			BatchID: id, UserID: user.ID, APIKeyID: 17, HoldAmount: amount, MonetaryVersion: 2})
		require.NoError(t, err)
	}
	start := make(chan struct{})
	errs := make(chan error, 2)
	go func() {
		<-start
		_, err := repo.CaptureBatchImageBalance(ctx, &service.BatchImageBalanceHoldCommand{RequestID: service.BatchImageCaptureRequestID(batch),
			BatchID: batch, UserID: user.ID, APIKeyID: 17, HoldAmount: 1, ActualAmount: 0.4, MonetaryVersion: 2})
		errs <- err
	}()
	go func() {
		<-start
		_, err := repo.ReleaseBatchImageBalance(ctx, &service.BatchImageBalanceHoldCommand{RequestID: service.BatchImageReleaseRequestID(batch),
			BatchID: batch, UserID: user.ID, APIKeyID: 17, HoldAmount: 1, MonetaryVersion: 2})
		errs <- err
	}()
	close(start)
	success, conflict := 0, 0
	for i := 0; i < 2; i++ {
		if err := <-errs; err != nil {
			require.ErrorIs(t, err, service.ErrUsageBillingRequestConflict)
			conflict++
		} else {
			success++
		}
	}
	require.Equal(t, 1, success)
	require.Equal(t, 1, conflict)
	var state, balance, frozen string
	var receipts int
	require.NoError(t, db.QueryRowContext(ctx, "SELECT state FROM batch_image_money_holds WHERE batch_id=$1 AND api_key_id=17", batch).Scan(&state))
	require.NoError(t, db.QueryRowContext(ctx, "SELECT balance::text,frozen_balance::text FROM users WHERE id=$1", user.ID).Scan(&balance, &frozen))
	require.NoError(t, db.QueryRowContext(ctx, "SELECT count(*) FROM usage_billing_dedup WHERE api_key_id=17 AND request_id IN ($1,$2)", service.BatchImageCaptureRequestID(batch), service.BatchImageReleaseRequestID(batch)).Scan(&receipts))
	require.Equal(t, 1, receipts)
	require.Equal(t, "2.00000000", frozen, "the other task's hold remains intact")
	if state == "captured" {
		require.Equal(t, "97.60000000", balance)
	} else {
		require.Equal(t, "released", state)
		require.Equal(t, "98.00000000", balance)
	}
}

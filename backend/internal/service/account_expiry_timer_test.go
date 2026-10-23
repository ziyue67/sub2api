//go:build unit

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/accounttimer"
)

type expiryTimerRepo struct {
	AccountRepository
	timers accounttimer.Repository
	expiry chan struct{}
}

func (r *expiryTimerRepo) SchedulableTimers() accounttimer.Repository { return r.timers }
func (r *expiryTimerRepo) AutoPauseExpiredAccounts(context.Context, time.Time) (int64, error) {
	select {
	case r.expiry <- struct{}{}:
	default:
	}
	return 0, nil
}

type expiryTimerWorker struct {
	accounttimer.Repository
	ran chan struct{}
}

func (w *expiryTimerWorker) RunDue(ctx context.Context) error {
	if _, ok := ctx.Deadline(); !ok {
		panic("worker context must be bounded")
	}
	select {
	case w.ran <- struct{}{}:
	default:
	}
	return errors.New("isolated fixture worker failure")
}
func TestAccountExpiryTimerSharesLifecycleAndDoesNotBlockExpiryOnError(t *testing.T) {
	worker := &expiryTimerWorker{ran: make(chan struct{}, 2)}
	repo := &expiryTimerRepo{timers: worker, expiry: make(chan struct{}, 2)}
	svc := NewAccountExpiryService(repo, time.Hour)
	svc.Start()
	defer svc.Stop()
	select {
	case <-worker.ran:
	case <-time.After(time.Second):
		t.Fatal("timer worker not run on startup")
	}
	select {
	case <-repo.expiry:
	case <-time.After(time.Second):
		t.Fatal("timer failure blocked expiry")
	}
	stopped := make(chan struct{})
	go func() { svc.Stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("Stop did not join worker")
	}
}
func TestAccountExpiryTimerOptionalProvider(t *testing.T) {
	s := &adminServiceImpl{accountRepo: &expiryTimerRepo{}}
	if s.SchedulableTimers() != nil {
		t.Fatal("unexpected provider")
	}
	worker := &expiryTimerWorker{ran: make(chan struct{}, 1)}
	s.accountRepo = &expiryTimerRepo{timers: worker}
	if s.SchedulableTimers() != worker {
		t.Fatal("timer capability not forwarded")
	}
}

package service

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"
)

var (
	ErrLocalSchedulerSnapshotUnavailable = errors.New("local scheduler snapshot unavailable")
	ErrLocalSchedulerSnapshotExpired     = errors.New("local scheduler snapshot expired")
	ErrLocalSchedulerSnapshotRevision    = errors.New("local scheduler snapshot revision must advance")
)

// LocalSchedulerSnapshotReader is the regional request-path contract. A
// configured reader is authoritative: callers must not fall back to the
// primary database when it reports a miss, expiry or malformed snapshot.
type LocalSchedulerSnapshotReader interface {
	GetLocalSnapshot(context.Context, SchedulerBucket) ([]*Account, bool, error)
}

// LocalSchedulerDetailReader serves complete credentials and group policy from
// the same authenticated stream as candidates. A candidate-only reader must
// fail closed when a caller needs details, never fall back to the primary.
type LocalSchedulerDetailReader interface {
	GetLocalAccounts(context.Context, []int64) (map[int64]*Account, error)
	GetLocalGroup(context.Context, int64) (*Group, error)
}

type localSchedulerSnapshot struct {
	revision  uint64
	expiresAt time.Time
	accounts  []byte
}

// LocalSchedulerSnapshotStore keeps immutable scheduler views in process
// memory. Regional sync code publishes complete views atomically; readers see
// either the previous complete view or the new complete view, never a partial
// bucket. A zero expiry is rejected to prevent accidentally immortal views.
type LocalSchedulerSnapshotStore struct {
	mu      sync.RWMutex
	now     func() time.Time
	buckets map[SchedulerBucket]localSchedulerSnapshot
}

func NewLocalSchedulerSnapshotStore(now func() time.Time) *LocalSchedulerSnapshotStore {
	if now == nil {
		now = time.Now
	}
	return &LocalSchedulerSnapshotStore{now: now, buckets: make(map[SchedulerBucket]localSchedulerSnapshot)}
}

// Publish replaces a complete bucket view. Revision is monotonic per bucket;
// callers may retry the same revision only after proving the payload is
// identical in the sync layer. This store rejects duplicates to avoid hiding a
// conflicting revision. It does not authenticate or decode network payloads.
func (s *LocalSchedulerSnapshotStore) Publish(bucket SchedulerBucket, revision uint64, expiresAt time.Time, accounts []*Account) error {
	if s == nil || bucket.GroupID < 0 || bucket.Platform == "" || (bucket.Mode != SchedulerModeSingle && bucket.Mode != SchedulerModeMixed && bucket.Mode != SchedulerModeForced) || revision == 0 || expiresAt.IsZero() {
		return ErrLocalSchedulerSnapshotUnavailable
	}
	now := s.now()
	if !expiresAt.After(now) {
		return ErrLocalSchedulerSnapshotExpired
	}
	seen := make(map[int64]bool, len(accounts))
	for _, account := range accounts {
		if account == nil || account.ID <= 0 || seen[account.ID] {
			return ErrLocalSchedulerSnapshotUnavailable
		}
		seen[account.ID] = true
	}
	encoded, err := json.Marshal(accounts)
	if err != nil {
		return ErrLocalSchedulerSnapshotUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if previous, ok := s.buckets[bucket]; ok && revision <= previous.revision {
		return ErrLocalSchedulerSnapshotRevision
	}
	if !expiresAt.After(s.now()) {
		return ErrLocalSchedulerSnapshotExpired
	}
	s.buckets[bucket] = localSchedulerSnapshot{revision: revision, expiresAt: expiresAt, accounts: encoded}
	return nil
}

func (s *LocalSchedulerSnapshotStore) GetLocalSnapshot(ctx context.Context, bucket SchedulerBucket) ([]*Account, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if s == nil {
		return nil, false, ErrLocalSchedulerSnapshotUnavailable
	}
	now := s.now()
	s.mu.RLock()
	view, ok := s.buckets[bucket]
	s.mu.RUnlock()
	if !ok || view.expiresAt.IsZero() {
		return nil, false, ErrLocalSchedulerSnapshotUnavailable
	}
	if !now.Before(view.expiresAt) {
		return nil, false, ErrLocalSchedulerSnapshotExpired
	}
	var accounts []*Account
	if err := json.Unmarshal(view.accounts, &accounts); err != nil {
		return nil, false, ErrLocalSchedulerSnapshotUnavailable
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if !s.now().Before(view.expiresAt) {
		return nil, false, ErrLocalSchedulerSnapshotExpired
	}
	return accounts, true, nil
}

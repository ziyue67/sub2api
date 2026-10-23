package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cyberevidence"
	"github.com/stretchr/testify/require"
)

type cyberEvidenceRecordStore struct {
	mu    sync.Mutex
	saves int
}

func (s *cyberEvidenceRecordStore) LoadConfig(context.Context) (cyberevidence.Config, error) {
	return cyberevidence.DefaultConfig(), nil
}
func (s *cyberEvidenceRecordStore) SaveConfig(context.Context, cyberevidence.Config) error {
	return nil
}
func (s *cyberEvidenceRecordStore) Save(context.Context, int64, cyberevidence.Prepared, int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.saves++
	return nil
}
func (s *cyberEvidenceRecordStore) Status(context.Context, int64, string) error { return nil }
func (s *cyberEvidenceRecordStore) Get(context.Context, int64) (cyberevidence.Record, error) {
	return cyberevidence.Record{}, cyberevidence.ErrNotFound
}
func (s *cyberEvidenceRecordStore) Delete(context.Context, int64) error { return nil }
func (s *cyberEvidenceRecordStore) Cleanup(context.Context, int) (int64, error) {
	return 0, nil
}
func (s *cyberEvidenceRecordStore) snapshotSaves() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saves
}

// cyberEvidenceCreateLogRepo assigns the ID that the production repository
// returns, which is the evidence foreign key.
type cyberEvidenceCreateLogRepo struct {
	contentModerationTestRepo
}

func (r *cyberEvidenceCreateLogRepo) CreateLog(ctx context.Context, log *ContentModerationLog) error {
	if log != nil {
		log.ID = 42
	}
	return r.contentModerationTestRepo.CreateLog(ctx, log)
}

// cyberEvidenceCreateLogFailureRepo simulates the parent audit row never being
// persisted, so no evidence may be enqueued.
type cyberEvidenceCreateLogFailureRepo struct {
	contentModerationTestRepo
	mu    sync.Mutex
	calls int
}

func (r *cyberEvidenceCreateLogFailureRepo) CreateLog(context.Context, *ContentModerationLog) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	return errors.New("synthetic create log failure")
}
func (r *cyberEvidenceCreateLogFailureRepo) snapshotCreateCalls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

func TestRecordCyberPolicyEventEnqueuesEvidenceOnlyAfterLogPersist(t *testing.T) {
	ctx := context.Background()
	settings := func() *contentModerationTestSettingRepo {
		return &contentModerationTestSettingRepo{values: map[string]string{SettingKeyRiskControlEnabled: "true"}}
	}

	store := &cyberEvidenceRecordStore{}
	evidence := cyberevidence.New(store, reauthTestEncryptor{}, true)
	t.Cleanup(evidence.Close)
	repo := &cyberEvidenceCreateLogRepo{}
	svc := NewContentModerationService(settings(), repo, nil, nil, nil, nil, nil, nil).WithCyberEvidence(evidence)
	prepared := evidence.PrepareWithSession([]byte(`{"messages":[{"role":"user","content":"synthetic visible input"}]}`), "synthetic upstream reason", "sess_synthetic")
	require.Equal(t, "pending", prepared.Status)
	svc.RecordCyberPolicyEvent(ctx, CyberPolicyRecordInput{
		LogOnly: true, Evidence: prepared, UserID: 42, UserEmail: "synthetic@example.test", Model: "gpt-5",
	})
	require.Eventually(t, func() bool { return store.snapshotSaves() == 1 }, 2*time.Second, 10*time.Millisecond)
	logs := repo.snapshotLogs()
	require.Len(t, logs, 1)
	require.Equal(t, "pending", logs[0].EvidenceStatus)

	failingStore := &cyberEvidenceRecordStore{}
	failingEvidence := cyberevidence.New(failingStore, reauthTestEncryptor{}, true)
	t.Cleanup(failingEvidence.Close)
	failingRepo := &cyberEvidenceCreateLogFailureRepo{}
	failingSvc := NewContentModerationService(settings(), failingRepo, nil, nil, nil, nil, nil, nil).WithCyberEvidence(failingEvidence)
	failedPrepared := failingEvidence.Prepare([]byte(`{"input":"synthetic visible input"}`), "synthetic upstream reason")
	defer failedPrepared.Release()
	failingSvc.RecordCyberPolicyEvent(ctx, CyberPolicyRecordInput{
		LogOnly: true, Evidence: failedPrepared, UserID: 42, UserEmail: "synthetic@example.test", Model: "gpt-5",
	})
	require.Equal(t, 1, failingRepo.snapshotCreateCalls())
	require.Zero(t, failingStore.snapshotSaves(), "failed parent log must not enqueue evidence")
	require.Empty(t, failingRepo.snapshotLogs())
}

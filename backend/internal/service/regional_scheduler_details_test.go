//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestRegionalSchedulerDetailsNoPrimaryFallback(t *testing.T) {
	ctx := context.Background()
	replica := regionalTestReplica(t, filepath.Join(t.TempDir(), "state"), true)
	doc := RegionalSchedulerDocument{Version: 1, Groups: []*Group{{ID: 10, Status: StatusActive, Platform: PlatformOpenAI, Hydrated: true}}, Buckets: []RegionalSchedulerBucket{{Bucket: SchedulerBucket{GroupID: 10, Platform: PlatformOpenAI, Mode: SchedulerModeSingle}, Accounts: []*Account{{ID: 1, GroupIDs: []int64{10}, Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true, Credentials: map[string]any{"api_key": "test-secret"}}}}}}
	publish := func() {
		t.Helper()
		payload, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		if err := replica.PublishNext(ctx, "test", time.Minute, payload); err != nil {
			t.Fatal(err)
		}
	}
	publish()
	reader, err := NewRegionalSchedulerReader(replica)
	if err != nil {
		t.Fatal(err)
	}
	// Nil primary dependencies turn any accidental fallback into a test failure.
	svc := NewSchedulerSnapshotService(nil, nil, nil, nil, nil)
	svc.SetLocalSnapshotReader(reader)
	account, err := svc.GetAccount(ctx, 1)
	if err != nil || account == nil || account.Credentials["api_key"] != "test-secret" {
		t.Fatalf("account hydration failed: %v", err)
	}
	account.Credentials["api_key"] = "changed"
	batch, err := svc.GetAccounts(ctx, []int64{1, 1, 2, -1})
	if err != nil || len(batch) != 1 || batch[1].Credentials["api_key"] != "test-secret" {
		t.Fatalf("batch hydration failed: %v", err)
	}
	for _, get := range []func(context.Context, int64) (*Group, error){svc.GetGroupByID, svc.GetGroupByIDLite} {
		g, err := get(ctx, 10)
		if err != nil || g == nil || !g.Hydrated {
			t.Fatalf("group hydration failed: %v", err)
		}
		g.Status = "disabled"
		if _, err := get(ctx, 11); err == nil {
			t.Fatal("missing group accepted")
		}
	}
	doc.Buckets[0].Accounts = []*Account{}
	doc.Groups = nil
	publish()
	account, err = svc.GetAccount(ctx, 1)
	if err != nil || account != nil {
		t.Fatalf("removed account resurrected: %v", err)
	}
	if _, err := svc.GetGroupByID(ctx, 10); err == nil {
		t.Fatal("removed group resurrected")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := svc.GetAccounts(canceled, []int64{1}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestRegionalSchedulerCandidateOnlyFailsClosedForDetails(t *testing.T) {
	svc := NewSchedulerSnapshotService(nil, nil, nil, nil, nil)
	svc.SetLocalSnapshotReader(NewLocalSchedulerSnapshotStore(nil))
	ctx := context.Background()
	if _, err := svc.GetAccount(ctx, 1); !errors.Is(err, ErrLocalSchedulerSnapshotUnavailable) {
		t.Fatal(err)
	}
	if _, err := svc.GetAccounts(ctx, []int64{1}); !errors.Is(err, ErrLocalSchedulerSnapshotUnavailable) {
		t.Fatal(err)
	}
	if _, err := svc.GetGroupByID(ctx, 1); !errors.Is(err, ErrLocalSchedulerSnapshotUnavailable) {
		t.Fatal(err)
	}
	if _, err := svc.GetGroupByIDLite(ctx, 1); !errors.Is(err, ErrLocalSchedulerSnapshotUnavailable) {
		t.Fatal(err)
	}
}

func TestRegionalSchedulerRejectsConflictingDetails(t *testing.T) {
	a := &Account{ID: 1, Platform: PlatformOpenAI, Credentials: map[string]any{"api_key": "a"}}
	b := &Account{ID: 1, Platform: PlatformOpenAI, Credentials: map[string]any{"api_key": "b"}}
	doc := RegionalSchedulerDocument{Version: 1, Buckets: []RegionalSchedulerBucket{
		{Bucket: SchedulerBucket{Platform: PlatformOpenAI, Mode: SchedulerModeSingle}, Accounts: []*Account{a}},
		{Bucket: SchedulerBucket{Platform: PlatformOpenAI, Mode: SchedulerModeForced}, Accounts: []*Account{b}},
	}}
	payload, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := RegionalSchedulerValidator([]int64{1})(payload); err == nil {
		t.Fatal("conflicting credentials accepted")
	}
}

func TestRegionalSchedulerPreSendRechecksDoNotUsePrimaryOrAdmissionCache(t *testing.T) {
	ctx := context.Background()
	replica := regionalTestReplica(t, filepath.Join(t.TempDir(), "state"), true)
	selected := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Credentials: map[string]any{"api_key": "fixture"}}
	if err := replica.PublishNext(ctx, "test", time.Minute, regionalDoc(t, selected)); err != nil {
		t.Fatal(err)
	}
	reader, err := NewRegionalSchedulerReader(replica)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := NewSchedulerSnapshotService(nil, nil, nil, nil, nil)
	snapshot.SetLocalSnapshotReader(reader)
	primary := &turnAdmissionRepo{err: errors.New("must never access primary")}
	svc := &OpenAIGatewayService{schedulerSnapshot: snapshot, accountRepo: primary, requireLatestTurnAdmission: true}
	if _, err := svc.latestOpenAITurnAccountForGroup(ctx, selected, 0, true); err != nil {
		t.Fatal(err)
	}
	if primary.reads != 0 {
		t.Fatal("regional admission accessed primary")
	}
	// An older process cache must not keep a removed account admissible.
	svc.openAITurnAdmissionCache.store(selected, nil)
	if err := replica.PublishNext(ctx, "test", time.Minute, regionalDoc(t)); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.latestOpenAITurnAccountForGroup(ctx, selected, 0, true); !IsOpenAITurnAdmissionError(err) {
		t.Fatalf("revocation ignored: %v", err)
	}
	if primary.reads != 0 {
		t.Fatal("regional admission fell back after revocation")
	}
}

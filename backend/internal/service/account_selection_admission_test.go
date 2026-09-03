package service

import "testing"

func TestAdmissionMaxConcurrencyArgsPreservesExplicitUnlimited(t *testing.T) {
	if got := admissionMaxConcurrencyArgs(nil); got != nil {
		t.Fatalf("nil acquire result should omit explicit metadata, got %#v", got)
	}
	if got := admissionMaxConcurrencyArgs(&AcquireResult{MaxConcurrency: 0}); got != nil {
		t.Fatalf("legacy result with zero max must omit explicit metadata, got %#v", got)
	}
	got := admissionMaxConcurrencyArgs(&AcquireResult{MaxConcurrency: 0, MaxConcurrencySet: true})
	if len(got) != 1 || got[0] != 0 {
		t.Fatalf("explicit unlimited max was not preserved: %#v", got)
	}
	got = admissionMaxConcurrencyArgs(&AcquireResult{MaxConcurrency: 7, MaxConcurrencySet: true})
	if len(got) != 1 || got[0] != 7 {
		t.Fatalf("explicit bounded max was not preserved: %#v", got)
	}
}

func TestSelectionAdmissionMaxConcurrencyPrefersExplicitMetadata(t *testing.T) {
	account := &Account{Concurrency: 11}
	waitPlan := &AccountWaitPlan{MaxConcurrency: 5}

	if got, set := selectionAdmissionMaxConcurrency(account, waitPlan); !set || got != 5 {
		t.Fatalf("wait plan max = %d/%v, want 5/true", got, set)
	}
	if got, set := selectionAdmissionMaxConcurrency(account, waitPlan, 0); !set || got != 0 {
		t.Fatalf("explicit unlimited max = %d/%v, want 0/true", got, set)
	}
	if got, set := selectionAdmissionMaxConcurrency(account, nil); !set || got != 11 {
		t.Fatalf("account max = %d/%v, want 11/true", got, set)
	}
	if got, set := selectionAdmissionMaxConcurrency(nil, nil); set || got != 0 {
		t.Fatalf("nil account max = %d/%v, want 0/false", got, set)
	}
}

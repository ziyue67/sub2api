package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// proxyLaneHydrationRepoStub implements only the authoritative lookup used by
// ensureSelectedAccountProxyLaneHydrated.  Embedding AccountRepository keeps
// the test focused on this narrow contract without a large hand-written mock.
type proxyLaneHydrationRepoStub struct {
	AccountRepository
	account *Account
	err     error
	calls   int
}

func (r *proxyLaneHydrationRepoStub) GetByID(context.Context, int64) (*Account, error) {
	r.calls++
	return r.account, r.err
}

func TestEnsureSelectedAccountProxyLaneHydratedRejectsCompactProxyWithoutRepository(t *testing.T) {
	proxyID := int64(301)
	lane := &AccountProxyLane{
		ID: 3001, AccountID: 30, ProxyID: &proxyID,
		Transport: AccountProxyLaneTransportProxy,
		Status:    AccountProxyLaneStatusActive, Schedulable: true,
	}
	selected := &Account{ID: 30, SelectedProxyLane: lane}
	hydrated := &Account{ID: 30}

	got, err := ensureSelectedAccountProxyLaneHydrated(context.Background(), selected, hydrated, nil)
	if got != nil {
		t.Fatalf("compact proxy lane must not be returned without hydration: %#v", got)
	}
	if !errors.Is(err, ErrNoSchedulableAccountProxyLane) {
		t.Fatalf("expected lane sentinel, got %v", err)
	}
}

func TestEnsureSelectedAccountProxyLaneHydratedUsesAuthoritativeRepository(t *testing.T) {
	proxyID := int64(302)
	lane := &AccountProxyLane{
		ID: 3002, AccountID: 30, ProxyID: &proxyID,
		Transport: AccountProxyLaneTransportProxy,
		Status:    AccountProxyLaneStatusActive, Schedulable: true,
	}
	selected := &Account{ID: 30, SelectedProxyLane: lane}
	proxy := &Proxy{ID: proxyID, Protocol: "http", Host: "lane.example", Port: 8302, Status: StatusActive}
	fresh := &Account{ID: 30, ProxyLanes: []AccountProxyLane{{
		ID: lane.ID, AccountID: 30, ProxyID: &proxyID, Proxy: proxy,
		Transport: AccountProxyLaneTransportProxy,
		Status:    AccountProxyLaneStatusActive, Schedulable: true,
		Weight: 1, Concurrency: 2,
	}}}
	repo := &proxyLaneHydrationRepoStub{account: fresh}

	got, err := ensureSelectedAccountProxyLaneHydrated(context.Background(), selected, &Account{ID: 30}, repo)
	require.NoError(t, err)
	require.Same(t, fresh, got)
	require.Equal(t, 1, repo.calls)
	require.NotNil(t, got.SelectedProxyLane)
	require.Same(t, proxy, got.SelectedProxyLane.Proxy)
	require.Equal(t, proxy.URL(), AccountProxyURL(got))
}

func TestEnsureSelectedAccountProxyLaneHydratedRejectsStaleAuthoritativeLane(t *testing.T) {
	proxyID := int64(303)
	lane := &AccountProxyLane{
		ID: 3003, AccountID: 30, ProxyID: &proxyID,
		Transport: AccountProxyLaneTransportProxy,
		Status:    AccountProxyLaneStatusActive, Schedulable: true,
	}
	selected := &Account{ID: 30, SelectedProxyLane: lane}
	cases := []struct {
		name  string
		fresh *Account
	}{
		{name: "deleted", fresh: &Account{ID: 30}},
		{name: "paused", fresh: &Account{ID: 30, ProxyLanes: []AccountProxyLane{{
			ID: lane.ID, AccountID: 30, ProxyID: &proxyID,
			Transport: AccountProxyLaneTransportProxy,
			Status:    AccountProxyLaneStatusPaused, Schedulable: true,
		}}}},
		{name: "reassigned", fresh: func() *Account {
			otherID := int64(304)
			other := &Proxy{ID: otherID, Protocol: "http", Host: "other.example", Port: 8304, Status: StatusActive}
			return &Account{ID: 30, ProxyLanes: []AccountProxyLane{{
				ID: lane.ID, AccountID: 30, ProxyID: &otherID, Proxy: other,
				Transport: AccountProxyLaneTransportProxy,
				Status:    AccountProxyLaneStatusActive, Schedulable: true,
			}}}
		}()},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &proxyLaneHydrationRepoStub{account: tc.fresh}
			got, err := ensureSelectedAccountProxyLaneHydrated(context.Background(), selected, &Account{ID: 30}, repo)
			if got != nil {
				t.Fatalf("stale lane %s was accepted: %#v", tc.name, got)
			}
			if !errors.Is(err, ErrNoSchedulableAccountProxyLane) {
				t.Fatalf("expected lane sentinel for %s, got %v", tc.name, err)
			}
		})
	}
}

func TestEnsureSelectedAccountProxyLaneHydratedAuthoritativeDoesNotTrustFullCache(t *testing.T) {
	oldProxyID, newProxyID := int64(306), int64(307)
	oldProxy := &Proxy{ID: oldProxyID, Protocol: "http", Host: "old-cache.example", Port: 8306, Status: StatusActive}
	newProxy := &Proxy{ID: newProxyID, Protocol: "http", Host: "new-db.example", Port: 8307, Status: StatusActive}
	selectedLane := &AccountProxyLane{
		ID: 3006, AccountID: 30, ProxyID: &oldProxyID,
		Transport: AccountProxyLaneTransportProxy,
		Status:    AccountProxyLaneStatusActive, Schedulable: true,
	}
	selected := &Account{ID: 30, SelectedProxyLane: selectedLane}

	// Redis/scheduler contains a complete-looking lane and proxy, but the DB
	// changed the binding after that snapshot was published.  The terminal
	// helper must read the repository first rather than accepting old-cache data.
	cacheAccount := &Account{ID: 30, ProxyLanes: []AccountProxyLane{{
		ID: selectedLane.ID, AccountID: 30, ProxyID: &oldProxyID, Proxy: oldProxy,
		Transport: AccountProxyLaneTransportProxy,
		Status:    AccountProxyLaneStatusActive, Schedulable: true,
		Weight: 1, Concurrency: 2,
	}}}
	cases := []struct {
		name string
		db   *Account
	}{
		{name: "deleted", db: &Account{ID: 30}},
		{name: "paused", db: &Account{ID: 30, ProxyLanes: []AccountProxyLane{{
			ID: selectedLane.ID, AccountID: 30, ProxyID: &oldProxyID,
			Transport: AccountProxyLaneTransportProxy,
			Status:    AccountProxyLaneStatusPaused, Schedulable: true,
		}}}},
		{name: "rebound", db: &Account{ID: 30, ProxyLanes: []AccountProxyLane{{
			ID: selectedLane.ID, AccountID: 30, ProxyID: &newProxyID, Proxy: newProxy,
			Transport: AccountProxyLaneTransportProxy,
			Status:    AccountProxyLaneStatusActive, Schedulable: true,
		}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &proxyLaneHydrationRepoStub{account: tc.db}
			got, err := ensureSelectedAccountProxyLaneHydratedAuthoritative(
				context.Background(), selected, cacheAccount, repo,
			)
			require.ErrorIs(t, err, ErrNoSchedulableAccountProxyLane)
			require.Nil(t, got)
			require.Equal(t, 1, repo.calls)
		})
	}

	// A matching DB row wins over the full cache as well; this proves the
	// authoritative path is not merely a compact-payload fallback.
	repo := &proxyLaneHydrationRepoStub{account: &Account{ID: 30, ProxyLanes: []AccountProxyLane{{
		ID: selectedLane.ID, AccountID: 30, ProxyID: &newProxyID, Proxy: newProxy,
		Transport: AccountProxyLaneTransportProxy,
		Status:    AccountProxyLaneStatusActive, Schedulable: true,
		Weight: 1, Concurrency: 4,
	}}}}
	selected.SelectedProxyLane = &AccountProxyLane{
		ID: selectedLane.ID, AccountID: 30, ProxyID: &newProxyID,
		Transport: AccountProxyLaneTransportProxy,
		Status:    AccountProxyLaneStatusActive, Schedulable: true,
	}
	got, err := ensureSelectedAccountProxyLaneHydratedAuthoritative(
		context.Background(), selected, cacheAccount, repo,
	)
	require.NoError(t, err)
	require.Same(t, repo.account, got)
	require.Equal(t, 1, repo.calls)
	require.Same(t, newProxy, got.SelectedProxyLane.Proxy)
}

func TestEnsureSelectedAccountProxyLaneHydratedAcceptsDirectLane(t *testing.T) {
	lane := &AccountProxyLane{
		ID: 3004, AccountID: 30,
		Transport: AccountProxyLaneTransportDirect,
		Status:    AccountProxyLaneStatusActive, Schedulable: true,
	}
	selected := &Account{ID: 30, SelectedProxyLane: lane}
	fresh := &Account{ID: 30, ProxyLanes: []AccountProxyLane{{
		ID: lane.ID, AccountID: 30,
		Transport: AccountProxyLaneTransportDirect,
		Status:    AccountProxyLaneStatusActive, Schedulable: true,
		Weight: 1, Concurrency: 1,
	}}}
	repo := &proxyLaneHydrationRepoStub{account: fresh}

	got, err := ensureSelectedAccountProxyLaneHydrated(context.Background(), selected, &Account{ID: 30}, repo)
	require.NoError(t, err)
	require.Same(t, fresh, got)
	require.NotNil(t, got.SelectedProxyLane)
	require.Empty(t, AccountProxyURL(got))
}

func TestEnsureSelectedAccountProxyLaneHydratedRejectsDirectProxyRelation(t *testing.T) {
	proxyID := int64(305)
	lane := &AccountProxyLane{
		ID: 3005, AccountID: 30,
		Transport: AccountProxyLaneTransportDirect,
		Status:    AccountProxyLaneStatusActive, Schedulable: true,
	}
	selected := &Account{ID: 30, SelectedProxyLane: lane}
	fresh := &Account{ID: 30, ProxyLanes: []AccountProxyLane{{
		ID: lane.ID, AccountID: 30, ProxyID: &proxyID,
		Transport: AccountProxyLaneTransportDirect,
		Status:    AccountProxyLaneStatusActive, Schedulable: true,
	}}}
	repo := &proxyLaneHydrationRepoStub{account: fresh}

	got, err := ensureSelectedAccountProxyLaneHydrated(context.Background(), selected, &Account{ID: 30}, repo)
	if got != nil {
		t.Fatalf("direct lane with proxy relation was accepted: %#v", got)
	}
	if !errors.Is(err, ErrNoSchedulableAccountProxyLane) {
		t.Fatalf("expected lane sentinel, got %v", err)
	}
}

func TestApplySelectedProxyLaneHydratesProxyFromAccountSnapshot(t *testing.T) {
	legacyID, laneID := int64(10), int64(20)
	legacyProxy := &Proxy{ID: legacyID, Protocol: "http", Host: "legacy.example", Port: 8080}
	laneProxy := &Proxy{ID: laneID, Protocol: "http", Host: "lane.example", Port: 8081, Status: StatusActive}
	original := &Account{
		ID:      7,
		ProxyID: &legacyID,
		Proxy:   legacyProxy,
		SelectedProxyLane: &AccountProxyLane{
			ID:        101,
			AccountID: 7,
			ProxyID:   &laneID,
			Transport: AccountProxyLaneTransportProxy,
		},
	}
	hydrated := &Account{
		ID:         7,
		ProxyID:    &legacyID,
		Proxy:      legacyProxy,
		ProxyLanes: []AccountProxyLane{{ID: 101, AccountID: 7, ProxyID: &laneID, Transport: AccountProxyLaneTransportProxy, Proxy: laneProxy, Status: AccountProxyLaneStatusActive, Schedulable: true, Weight: 1, Concurrency: 1}},
	}
	got := applyLaneToHydratedAccount(original, hydrated)
	if got != hydrated {
		t.Fatal("expected hydration helper to reuse hydrated account")
	}
	if got.ProxyID == nil || *got.ProxyID != laneID {
		t.Fatalf("selected lane proxy id not restored: %#v", got.ProxyID)
	}
	if got.Proxy != laneProxy {
		t.Fatalf("selected lane proxy object not restored: %#v", got.Proxy)
	}
	if got.SelectedProxyLane == nil || got.SelectedProxyLane.Proxy != laneProxy {
		t.Fatalf("selected lane metadata lost hydrated proxy: %#v", got.SelectedProxyLane)
	}
	if AccountProxyURL(got) != laneProxy.URL() {
		t.Fatalf("unexpected lane URL: %q", AccountProxyURL(got))
	}
}

func TestApplySelectedProxyLaneDoesNotJoinByProxyIDWhenLaneIDDiffers(t *testing.T) {
	proxyID := int64(20)
	proxy := &Proxy{ID: proxyID, Protocol: "http", Host: "lane-a.example", Port: 8081, Status: StatusActive}
	account := &Account{
		ID: 7,
		// Keep a legacy proxy on the account as older snapshots do.  The
		// mismatched lane must not borrow it merely because its proxy ID agrees.
		ProxyID: &proxyID,
		Proxy:   proxy,
		ProxyLanes: []AccountProxyLane{{
			ID: 101, AccountID: 7, ProxyID: &proxyID, Proxy: proxy,
			Transport: AccountProxyLaneTransportProxy, Status: AccountProxyLaneStatusActive,
			Schedulable: true, Weight: 1, Concurrency: 1,
		}},
	}
	// This compact lane has the same proxy ID as lane 101 but a different
	// identity.  It must not borrow lane 101's hydrated proxy object: lane ID,
	// not proxy ID, is the authoritative relation during hydration.
	requested := &AccountProxyLane{
		ID: 999, AccountID: 7, ProxyID: &proxyID,
		Transport: AccountProxyLaneTransportProxy, Status: AccountProxyLaneStatusActive,
		Schedulable: true, Weight: 1, Concurrency: 1,
	}
	account.ApplySelectedProxyLane(requested)
	if account.SelectedProxyLane == nil || account.SelectedProxyLane.ID != 999 {
		t.Fatalf("selected lane identity changed: %#v", account.SelectedProxyLane)
	}
	if account.Proxy != nil || account.ProxyID == nil || *account.ProxyID != proxyID {
		t.Fatalf("proxy from a different lane was borrowed: proxy_id=%v proxy=%#v", account.ProxyID, account.Proxy)
	}
}

func TestApplyLaneToHydratedAccountRejectsDeletedLaneWithoutLegacyProxyFallback(t *testing.T) {
	legacyID, selectedProxyID := int64(10), int64(20)
	legacyProxy := &Proxy{ID: legacyID, Protocol: "http", Host: "legacy.example", Port: 8080}
	original := &Account{
		ID:      7,
		ProxyID: &selectedProxyID,
		Proxy:   &Proxy{ID: selectedProxyID, Protocol: "http", Host: "old-lane.example", Port: 8081},
		SelectedProxyLane: &AccountProxyLane{
			ID: 101, AccountID: 7, ProxyID: &selectedProxyID,
			Transport: AccountProxyLaneTransportProxy,
		},
	}
	hydrated := &Account{ID: 7, ProxyID: &legacyID, Proxy: legacyProxy}
	got := applyLaneToHydratedAccount(original, hydrated)
	if got == nil {
		t.Fatal("expected hydrated account")
	}
	if got.SelectedProxyLane != nil {
		t.Fatalf("deleted lane was resurrected: %#v", got.SelectedProxyLane)
	}
	if got.ProxyID != nil || got.Proxy != nil || AccountProxyURL(got) != "" {
		t.Fatalf("stale lane fell back to legacy proxy: proxy_id=%v proxy=%#v url=%q", got.ProxyID, got.Proxy, AccountProxyURL(got))
	}
}

func TestApplyLaneToHydratedAccountRejectsPausedLane(t *testing.T) {
	proxyID := int64(20)
	proxy := &Proxy{ID: proxyID, Protocol: "http", Host: "lane.example", Port: 8081, Status: StatusActive}
	original := &Account{ID: 7, SelectedProxyLane: &AccountProxyLane{
		ID: 101, AccountID: 7, ProxyID: &proxyID, Transport: AccountProxyLaneTransportProxy,
	}}
	hydrated := &Account{ID: 7, ProxyLanes: []AccountProxyLane{{
		ID: 101, AccountID: 7, ProxyID: &proxyID, Proxy: proxy,
		Transport: AccountProxyLaneTransportProxy, Status: AccountProxyLaneStatusPaused,
		Schedulable: true, Weight: 1, Concurrency: 1,
	}}}
	got := applyLaneToHydratedAccount(original, hydrated)
	if got.SelectedProxyLane != nil || got.ProxyID != nil || got.Proxy != nil {
		t.Fatalf("paused lane should not be projected: %#v proxy_id=%v proxy=%#v", got.SelectedProxyLane, got.ProxyID, got.Proxy)
	}
}

func TestApplyLaneToHydratedAccountRejectsReassignedProxy(t *testing.T) {
	oldProxyID, newProxyID := int64(20), int64(21)
	hydratedProxy := &Proxy{ID: newProxyID, Protocol: "http", Host: "new.example", Port: 8081, Status: StatusActive}
	original := &Account{ID: 7, SelectedProxyLane: &AccountProxyLane{
		ID: 101, AccountID: 7, ProxyID: &oldProxyID, Transport: AccountProxyLaneTransportProxy,
	}}
	hydrated := &Account{ID: 7, ProxyID: &newProxyID, Proxy: hydratedProxy, ProxyLanes: []AccountProxyLane{{
		ID: 101, AccountID: 7, ProxyID: &newProxyID, Proxy: hydratedProxy,
		Transport: AccountProxyLaneTransportProxy, Status: AccountProxyLaneStatusActive,
		Schedulable: true, Weight: 1, Concurrency: 1,
	}}}
	got := applyLaneToHydratedAccount(original, hydrated)
	if got.SelectedProxyLane != nil || got.ProxyID != nil || got.Proxy != nil {
		t.Fatalf("reassigned proxy should invalidate lane: %#v proxy_id=%v proxy=%#v", got.SelectedProxyLane, got.ProxyID, got.Proxy)
	}
}

func TestGatewayWaitPlanReplacesStaleSelectedLane(t *testing.T) {
	oldProxyID, healthyProxyID := int64(40), int64(41)
	paused := AccountProxyLane{
		ID: 401, AccountID: 7, ProxyID: &oldProxyID,
		Transport: AccountProxyLaneTransportProxy, Status: AccountProxyLaneStatusPaused,
		Schedulable: true, Weight: 1, Concurrency: 2,
	}
	healthy := AccountProxyLane{
		ID: 402, AccountID: 7, ProxyID: &healthyProxyID,
		Transport: AccountProxyLaneTransportProxy, Status: AccountProxyLaneStatusActive,
		Schedulable: true, Weight: 1, Concurrency: 3,
	}
	account := &Account{
		ID: 7, Concurrency: 9,
		ProxyLanes:        []AccountProxyLane{paused, healthy},
		SelectedProxyLane: &paused,
	}

	plan, waitable := (&GatewayService{}).gatewayWaitPlanForAccount(
		context.Background(), account, time.Second, 5,
	)
	require.True(t, waitable)
	if plan == nil {
		t.Fatal("expected a wait plan for the healthy replacement lane")
	}
	if account.SelectedProxyLane == nil || account.SelectedProxyLane.ID != healthy.ID {
		t.Fatalf("stale selected lane was retained: %#v", account.SelectedProxyLane)
	}
	if account.Concurrency != 9 {
		t.Fatalf("legacy aggregate concurrency was overwritten by lane cap: got=%d", account.Concurrency)
	}
	if plan.AggregateMaxConcurrency != 9 || !plan.AggregateMaxConcurrencySet {
		t.Fatalf("wait plan lost parent aggregate: %#v", plan)
	}
}

func TestGatewayWaitPlanRejectsWhenAllSelectedLanesAreStale(t *testing.T) {
	proxyID := int64(50)
	paused := AccountProxyLane{
		ID: 501, AccountID: 8, ProxyID: &proxyID,
		Transport: AccountProxyLaneTransportProxy, Status: AccountProxyLaneStatusPaused,
		Schedulable: true, Weight: 1, Concurrency: 1,
	}
	account := &Account{ID: 8, Concurrency: 2, ProxyLanes: []AccountProxyLane{paused}, SelectedProxyLane: &paused}
	plan, waitable := (&GatewayService{}).gatewayWaitPlanForAccount(context.Background(), account, time.Second, 1)
	if waitable || plan != nil {
		t.Fatalf("paused-only account must not produce a wait plan: plan=%#v waitable=%v", plan, waitable)
	}
}

func TestConcurrencyServiceAcquireAccountSlotWithNilCacheFailsOpen(t *testing.T) {
	service := NewConcurrencyService(nil)
	result, err := service.AcquireAccountSlot(context.Background(), 77, 4)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.Acquired)
	require.True(t, result.MaxConcurrencySet)
	require.Equal(t, 4, result.MaxConcurrency)
	require.NotPanics(t, func() {
		if result.ReleaseFunc != nil {
			result.ReleaseFunc()
		}
	})
}

func TestApplyLaneToHydratedAccountAcceptsDirectLaneAndClearsLegacyProxy(t *testing.T) {
	legacyID := int64(10)
	original := &Account{ID: 7, ProxyID: &legacyID, Proxy: &Proxy{ID: legacyID, Protocol: "http", Host: "legacy.example", Port: 8080}, SelectedProxyLane: &AccountProxyLane{
		ID: 102, AccountID: 7, Transport: AccountProxyLaneTransportDirect,
	}}
	hydrated := &Account{ID: 7, ProxyID: &legacyID, Proxy: original.Proxy, ProxyLanes: []AccountProxyLane{{
		ID: 102, AccountID: 7, Transport: AccountProxyLaneTransportDirect,
		Status: AccountProxyLaneStatusActive, Schedulable: true, Weight: 1, Concurrency: 1,
	}}}
	got := applyLaneToHydratedAccount(original, hydrated)
	if got.SelectedProxyLane == nil || got.SelectedProxyLane.ID != 102 {
		t.Fatalf("direct lane was not preserved: %#v", got.SelectedProxyLane)
	}
	if got.ProxyID != nil || got.Proxy != nil || AccountProxyURL(got) != "" {
		t.Fatalf("direct lane inherited legacy proxy: proxy_id=%v proxy=%#v url=%q", got.ProxyID, got.Proxy, AccountProxyURL(got))
	}
}

func TestAccountProxyLaneContextAndDirectURL(t *testing.T) {
	proxyID := int64(1)
	proxy := &Proxy{ID: proxyID, Protocol: "http", Host: "proxy.example", Port: 8080}
	account := &Account{ID: 9, ProxyID: &proxyID, Proxy: proxy}
	if got := AccountProxyURL(account); got != proxy.URL() {
		t.Fatalf("legacy proxy URL changed: got=%q want=%q", got, proxy.URL())
	}
	direct := AccountProxyLane{ID: 99, AccountID: 9, Transport: AccountProxyLaneTransportDirect}
	account.ApplySelectedProxyLane(&direct)
	if got := AccountProxyURL(account); got != "" {
		t.Fatalf("direct lane must not inherit legacy proxy: %q", got)
	}
	ctx := WithSelectedAccountProxyLane(context.Background(), account)
	if got := AccountProxyLaneIDFromContext(ctx); got != direct.ID {
		t.Fatalf("lane context mismatch: got=%d want=%d", got, direct.ID)
	}
}

func TestApplySelectedProxyLaneNormalizesTransportBeforeProjection(t *testing.T) {
	legacyID := int64(77)
	legacy := &Proxy{ID: legacyID, Protocol: "http", Host: "legacy.example", Port: 8080, Status: StatusActive}
	account := &Account{ID: 9, ProxyID: &legacyID, Proxy: legacy}

	// A mixed-case direct value is valid after normalization and must not
	// inherit the legacy account proxy in either the projected fields or URL
	// resolver.
	direct := AccountProxyLane{ID: 901, AccountID: account.ID, Transport: "DIRECT", Status: AccountProxyLaneStatusActive, Schedulable: true, Weight: 1, Concurrency: 1}
	account.ApplySelectedProxyLane(&direct)
	if account.SelectedProxyLane == nil || account.SelectedProxyLane.Transport != AccountProxyLaneTransportDirect {
		t.Fatalf("transport was not normalized: %#v", account.SelectedProxyLane)
	}
	if account.ProxyID != nil || account.Proxy != nil || AccountProxyURL(account) != "" {
		t.Fatalf("direct lane inherited legacy proxy: proxy_id=%v proxy=%#v url=%q", account.ProxyID, account.Proxy, AccountProxyURL(account))
	}

	// Blank transport follows the database default (proxy) and should preserve
	// the lane's own relation rather than being treated as an unrelated value.
	laneProxyID := int64(88)
	laneProxy := &Proxy{ID: laneProxyID, Protocol: "http", Host: "lane.example", Port: 8081, Status: StatusActive}
	account.ProxyLanes = []AccountProxyLane{{ID: 902, AccountID: account.ID, ProxyID: &laneProxyID, Transport: AccountProxyLaneTransportProxy, Proxy: laneProxy, Status: AccountProxyLaneStatusActive, Schedulable: true, Weight: 1, Concurrency: 1}}
	proxyLane := AccountProxyLane{ID: 902, AccountID: account.ID, ProxyID: &laneProxyID, Transport: "", Status: AccountProxyLaneStatusActive, Schedulable: true, Weight: 1, Concurrency: 1}
	account.ApplySelectedProxyLane(&proxyLane)
	if account.SelectedProxyLane == nil || account.SelectedProxyLane.Transport != AccountProxyLaneTransportProxy || account.Proxy != laneProxy {
		t.Fatalf("blank transport did not use proxy default: %#v proxy=%#v", account.SelectedProxyLane, account.Proxy)
	}
}

func TestAccountProxyLaneValidateTransportAndProxy(t *testing.T) {
	proxyID := int64(10)
	valid := AccountProxyLane{
		AccountID:   1,
		ProxyID:     &proxyID,
		Name:        "  edge-a ",
		Transport:   AccountProxyLaneTransportProxy,
		Concurrency: 3,
		Weight:      2,
		Priority:    50,
		Status:      AccountProxyLaneStatusActive,
		Schedulable: true,
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid lane rejected: %v", err)
	}

	direct := valid
	direct.Transport = AccountProxyLaneTransportDirect
	direct.ProxyID = nil
	if err := direct.Validate(); err != nil {
		t.Fatalf("valid direct lane rejected: %v", err)
	}

	badDirect := direct
	badDirect.ProxyID = &proxyID
	if err := badDirect.Validate(); err == nil {
		t.Fatal("direct lane with proxy_id should be rejected")
	}

	badProxy := valid
	badProxy.ProxyID = nil
	if err := badProxy.Validate(); err == nil {
		t.Fatal("proxy lane without proxy_id should be rejected")
	}
}

func TestValidateAccountProxyLanesRejectsDuplicates(t *testing.T) {
	p1, p2 := int64(10), int64(11)
	base := func(name string, proxyID *int64) AccountProxyLane {
		return AccountProxyLane{
			ProxyID:     proxyID,
			Name:        name,
			Transport:   AccountProxyLaneTransportProxy,
			Concurrency: 1,
			Weight:      1,
			Status:      AccountProxyLaneStatusActive,
			Schedulable: true,
		}
	}
	if err := ValidateAccountProxyLanes(1, []AccountProxyLane{base("a", &p1), base("A", &p2)}); err == nil {
		t.Fatal("duplicate names should be rejected case-insensitively")
	}
	if err := ValidateAccountProxyLanes(1, []AccountProxyLane{base("a", &p1), base("b", &p1)}); err == nil {
		t.Fatal("duplicate proxy IDs should be rejected")
	}
}

func TestFilterSchedulableAccountProxyLanes(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	future := now.Add(time.Hour)
	proxyID := int64(1)
	lanes := []AccountProxyLane{
		{Name: "cooldown", ProxyID: &proxyID, Transport: "proxy", Status: "active", Schedulable: true, Weight: 1, Priority: 1, CooldownUntil: &future},
		{Name: "paused", ProxyID: &proxyID, Transport: "proxy", Status: "paused", Schedulable: true, Weight: 1, Priority: 1},
		{Name: "low-priority", ProxyID: &proxyID, Transport: "proxy", Status: "active", Schedulable: true, Weight: 1, Priority: 20},
		{Name: "best", ProxyID: &proxyID, Transport: "proxy", Status: "active", Schedulable: true, Weight: 1, Priority: 2},
	}
	got := FilterSchedulableAccountProxyLanes(lanes, now)
	if len(got) != 2 || got[0].Name != "best" || got[1].Name != "low-priority" {
		t.Fatalf("unexpected filtered lanes: %#v", got)
	}
}

func TestSelectAccountProxyLaneStickyAndWeighted(t *testing.T) {
	p1, p2 := int64(1), int64(2)
	lanes := []AccountProxyLane{
		{Name: "one", ProxyID: &p1, Transport: "proxy", Status: "active", Schedulable: true, Weight: 1, Priority: 10},
		{Name: "two", ProxyID: &p2, Transport: "proxy", Status: "active", Schedulable: true, Weight: 3, Priority: 10},
	}
	now := time.Unix(1_700_000_000, 0)
	a, err := SelectAccountProxyLaneForSession(lanes, "session-42", now)
	if err != nil {
		t.Fatalf("selection failed: %v", err)
	}
	b, err := SelectAccountProxyLaneForSession(lanes, "session-42", now)
	if err != nil || a.Name != b.Name {
		t.Fatalf("selection is not sticky: a=%q b=%q err=%v", a.Name, b.Name, err)
	}

	// Different priorities must prefer the lower number regardless of weight.
	prioritized := append([]AccountProxyLane(nil), lanes...)
	prioritized[0].Priority = 1
	prioritized[0].Weight = 1
	prioritized[1].Priority = 2
	prioritized[1].Weight = 100
	chosen, err := SelectAccountProxyLaneForSession(prioritized, "any", now)
	if err != nil || chosen.Name != "one" {
		t.Fatalf("priority selection failed: %#v err=%v", chosen, err)
	}
}

func TestSelectAccountProxyLaneStableWhenSourceOrderChanges(t *testing.T) {
	p1, p2 := int64(11), int64(12)
	lanes := []AccountProxyLane{
		{ID: 1202, Name: "two", ProxyID: &p2, Transport: "proxy", Status: "active", Schedulable: true, Weight: 3, Priority: 4},
		{ID: 1201, Name: "one", ProxyID: &p1, Transport: "proxy", Status: "active", Schedulable: true, Weight: 1, Priority: 4},
	}
	now := time.Unix(1_700_000_000, 0)
	first, err := SelectAccountProxyLaneForSession(lanes, "order-stability", now)
	if err != nil {
		t.Fatalf("selection failed: %v", err)
	}
	reversed := append([]AccountProxyLane(nil), lanes...)
	reversed[0], reversed[1] = reversed[1], reversed[0]
	second, err := SelectAccountProxyLaneForSession(reversed, "order-stability", now)
	if err != nil {
		t.Fatalf("selection after reorder failed: %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("source order changed sticky lane: first=%d second=%d", first.ID, second.ID)
	}
}

func TestSelectAccountProxyLaneNoEligible(t *testing.T) {
	p := int64(1)
	future := time.Now().Add(time.Hour)
	_, err := SelectAccountProxyLaneForSession([]AccountProxyLane{{
		Name: "cooldown", ProxyID: &p, Transport: "proxy", Status: "active", Schedulable: true, Weight: 1, Priority: 1, CooldownUntil: &future,
	}}, "s", time.Now())
	if !errors.Is(err, ErrNoSchedulableAccountProxyLane) {
		t.Fatalf("expected ErrNoSchedulableAccountProxyLane, got %v", err)
	}
}

//go:build unit

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// geminiLaneTestAccount returns a minimal schedulable Gemini API-key account.
// The lane is intentionally compact (ProxyID only) so the test exercises the
// same shape emitted by scheduler snapshots.
func geminiLaneTestAccount(id, laneID, proxyID int64, proxy *Proxy) Account {
	lane := AccountProxyLane{
		ID: laneID, AccountID: id, ProxyID: &proxyID,
		Transport: AccountProxyLaneTransportProxy,
		Status:    AccountProxyLaneStatusActive, Schedulable: true,
		Weight: 1, Concurrency: 2,
		Proxy: proxy,
	}
	return Account{
		ID: id, Platform: PlatformGemini, Type: AccountTypeAPIKey,
		Status: StatusActive, Schedulable: true, Priority: 1,
		Concurrency: 2,
		Credentials: map[string]any{"api_key": "gemini-test-key"},
		ProxyLanes:  []AccountProxyLane{lane},
	}
}

func TestGeminiProjectLaneSelectionHydratesCompactProxyFromRepository(t *testing.T) {
	proxyID := int64(7101)
	laneID := int64(71001)
	proxy := &Proxy{ID: proxyID, Protocol: "http", Host: "lane.example", Port: 8710, Status: StatusActive}

	compact := geminiLaneTestAccount(71, laneID, proxyID, nil)
	// Keep an unrelated legacy proxy on the compact object. If lane hydration
	// regresses to Account.Proxy this assertion catches the wrong egress.
	legacyID := int64(7199)
	compact.ProxyID = &legacyID
	compact.Proxy = &Proxy{ID: legacyID, Protocol: "http", Host: "legacy.example", Port: 8799, Status: StatusActive}

	authoritative := geminiLaneTestAccount(71, laneID, proxyID, proxy)
	repo := &proxyLaneHydrationRepoStub{account: &authoritative}
	svc := &GeminiMessagesCompatService{accountRepo: repo}

	got, err := svc.projectGeminiLaneSelection(context.Background(), &compact, "gemini-session")
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, 1, repo.calls, "compact lane should trigger one authoritative lookup")
	require.NotNil(t, got.SelectedProxyLane)
	require.Equal(t, laneID, got.SelectedProxyLane.ID)
	require.Same(t, proxy, got.SelectedProxyLane.Proxy)
	require.Equal(t, proxy.URL(), AccountProxyURL(got))
	require.Equal(t, proxyID, *got.ProxyID)
}

func TestGeminiProjectLaneSelectionFailsClosedWithoutAuthoritativeHydration(t *testing.T) {
	proxyID := int64(7201)
	laneID := int64(72001)
	compact := geminiLaneTestAccount(72, laneID, proxyID, nil)
	legacyID := int64(7299)
	compact.ProxyID = &legacyID
	compact.Proxy = &Proxy{ID: legacyID, Protocol: "http", Host: "legacy.example", Port: 8799, Status: StatusActive}

	svc := &GeminiMessagesCompatService{}
	got, err := svc.projectGeminiLaneSelection(context.Background(), &compact, "gemini-session")
	require.Nil(t, got)
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrNoSchedulableAccountProxyLane), "unexpected error: %v", err)
}

func TestGeminiProjectLaneSelectionRejectsPausedOrDeletedLane(t *testing.T) {
	proxyID := int64(7301)
	laneID := int64(73001)
	proxy := &Proxy{ID: proxyID, Protocol: "http", Host: "lane.example", Port: 8730, Status: StatusActive}

	cases := []struct {
		name          string
		authoritative *Account
	}{
		{
			name:          "deleted",
			authoritative: &Account{ID: 73},
		},
		{
			name: "paused",
			authoritative: &Account{ID: 73, ProxyLanes: []AccountProxyLane{{
				ID: laneID, AccountID: 73, ProxyID: &proxyID, Proxy: proxy,
				Transport: AccountProxyLaneTransportProxy,
				Status:    AccountProxyLaneStatusPaused, Schedulable: true,
				Weight: 1, Concurrency: 2,
			}}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			compact := geminiLaneTestAccount(73, laneID, proxyID, nil)
			legacyID := int64(7399)
			compact.ProxyID = &legacyID
			compact.Proxy = &Proxy{ID: legacyID, Protocol: "http", Host: "legacy.example", Port: 8799, Status: StatusActive}
			repo := &proxyLaneHydrationRepoStub{account: tc.authoritative}
			svc := &GeminiMessagesCompatService{accountRepo: repo}

			got, err := svc.projectGeminiLaneSelection(context.Background(), &compact, "gemini-session")
			require.Nil(t, got)
			require.Error(t, err)
			require.True(t, errors.Is(err, ErrNoSchedulableAccountProxyLane), "unexpected error: %v", err)
		})
	}
}

func TestGeminiProjectLaneSelectionDirectLaneClearsLegacyProxy(t *testing.T) {
	legacyID := int64(7401)
	laneID := int64(74001)
	legacy := &Proxy{ID: legacyID, Protocol: "http", Host: "legacy.example", Port: 8740, Status: StatusActive}
	compact := Account{
		ID: 74, Platform: PlatformGemini, Type: AccountTypeAPIKey,
		Status: StatusActive, Schedulable: true, Concurrency: 2,
		Credentials: map[string]any{"api_key": "gemini-test-key"},
		ProxyID:     &legacyID, Proxy: legacy,
		ProxyLanes: []AccountProxyLane{{
			ID: laneID, AccountID: 74,
			Transport: AccountProxyLaneTransportDirect,
			Status:    AccountProxyLaneStatusActive, Schedulable: true,
			Weight: 1, Concurrency: 2,
		}},
	}
	authoritative := compact
	repo := &proxyLaneHydrationRepoStub{account: &authoritative}
	svc := &GeminiMessagesCompatService{accountRepo: repo}

	got, err := svc.projectGeminiLaneSelection(context.Background(), &compact, "gemini-session")
	require.NoError(t, err)
	require.NotNil(t, got)
	require.NotNil(t, got.SelectedProxyLane)
	require.Equal(t, laneID, got.SelectedProxyLane.ID)
	require.Equal(t, AccountProxyLaneTransportDirect, got.SelectedProxyLane.Transport)
	require.Nil(t, got.ProxyID)
	require.Nil(t, got.Proxy)
	require.Empty(t, AccountProxyURL(got))
}

func TestGeminiProjectLaneSelectionPreservesLegacyAccountBehavior(t *testing.T) {
	proxyID := int64(7501)
	proxy := &Proxy{ID: proxyID, Protocol: "http", Host: "legacy.example", Port: 8750, Status: StatusActive}
	account := &Account{
		ID: 75, Platform: PlatformGemini, Type: AccountTypeAPIKey,
		Status: StatusActive, Schedulable: true, Concurrency: 2,
		Credentials: map[string]any{"api_key": "gemini-test-key"},
		ProxyID:     &proxyID, Proxy: proxy,
	}
	svc := &GeminiMessagesCompatService{}

	got, err := svc.projectGeminiLaneSelection(context.Background(), account, "gemini-session")
	require.NoError(t, err)
	require.Same(t, account, got)
	require.Equal(t, proxy.URL(), AccountProxyURL(got))
}

func TestGeminiSelectAccountForModelProjectsSelectedLaneBeforeStickyBinding(t *testing.T) {
	proxyID := int64(7601)
	laneID := int64(76001)
	proxy := &Proxy{ID: proxyID, Protocol: "http", Host: "lane.example", Port: 8760, Status: StatusActive}
	compact := geminiLaneTestAccount(76, laneID, proxyID, nil)
	authoritative := geminiLaneTestAccount(76, laneID, proxyID, proxy)
	repo := &proxyLaneHydrationRepoStub{
		AccountRepository: &mockAccountRepoForGemini{},
		account:           &authoritative,
	}
	repo.AccountRepository.(*mockAccountRepoForGemini).accounts = []Account{compact}
	repo.AccountRepository.(*mockAccountRepoForGemini).accountsByID = map[int64]*Account{compact.ID: &compact}
	cache := &mockGatewayCacheForGemini{}
	svc := &GeminiMessagesCompatService{accountRepo: repo, cache: cache}

	got, err := svc.SelectAccountForModelWithExclusions(context.Background(), nil, "session-76", "gemini-2.5-flash", nil)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, proxy.URL(), AccountProxyURL(got))
	require.Equal(t, int64(76), cache.sessionBindings["gemini:session-76"])
}

func TestGeminiSelectAccountForModelWithoutStickyCacheDoesNotPanic(t *testing.T) {
	proxyID := int64(7651)
	laneID := int64(76501)
	proxy := &Proxy{ID: proxyID, Protocol: "http", Host: "lane.example", Port: 8765, Status: StatusActive}
	compact := geminiLaneTestAccount(765, laneID, proxyID, nil)
	authoritative := geminiLaneTestAccount(765, laneID, proxyID, proxy)
	baseRepo := &mockAccountRepoForGemini{
		accounts:     []Account{compact},
		accountsByID: map[int64]*Account{compact.ID: &compact},
	}
	repo := &proxyLaneHydrationRepoStub{AccountRepository: baseRepo, account: &authoritative}
	svc := &GeminiMessagesCompatService{accountRepo: repo}

	got, err := svc.SelectAccountForModelWithExclusions(context.Background(), nil, "session-without-cache", "gemini-2.5-flash", nil)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, proxy.URL(), AccountProxyURL(got))
}

func TestGeminiSelectAccountForAIStudioEndpointsProjectsLane(t *testing.T) {
	proxyID := int64(7701)
	laneID := int64(77001)
	proxy := &Proxy{ID: proxyID, Protocol: "http", Host: "lane.example", Port: 8770, Status: StatusActive}
	compact := geminiLaneTestAccount(77, laneID, proxyID, nil)
	authoritative := geminiLaneTestAccount(77, laneID, proxyID, proxy)
	baseRepo := &mockAccountRepoForGemini{
		accounts:     []Account{compact},
		accountsByID: map[int64]*Account{compact.ID: &compact},
	}
	repo := &proxyLaneHydrationRepoStub{AccountRepository: baseRepo, account: &authoritative}
	svc := &GeminiMessagesCompatService{accountRepo: repo}

	got, err := svc.SelectAccountForAIStudioEndpoints(context.Background(), nil)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, proxy.URL(), AccountProxyURL(got))
	require.Equal(t, laneID, got.SelectedProxyLane.ID)
}

package service

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type openAIRPMTestCache struct {
	counts map[int64]int
	ids    []int64
	err    error
}

func TestOpenAIRPMInternalAttemptsConsumeAdmissionOnce(t *testing.T) {
	account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{"base_rpm": 2}}
	cache := &openAIRPMTestCache{counts: map[int64]int{}}
	calls := 0
	s := &OpenAIGatewayService{rpmCache: cache, httpUpstream: &codexTicketFuncUpstream{do: func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: http.StatusBadRequest, Header: http.Header{}, Body: http.NoBody}, nil
	}}}
	allowed, state, err := s.TryAcquireOpenAIOAuthRPM(context.Background(), account)
	require.NoError(t, err)
	require.True(t, allowed)
	ctx := WithOpenAIRPMReservation(context.Background(), account, state)
	for range 2 {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://example.invalid/responses", nil)
		require.NoError(t, err)
		resp, err := s.doOpenAIUpstream(req, "", account)
		require.NoError(t, err)
		require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://example.invalid/responses", nil)
	require.NoError(t, err)
	_, err = s.doOpenAIUpstream(req, "", account)
	require.ErrorIs(t, err, ErrOpenAIRPMExhausted)
	require.Equal(t, 2, calls, "a rejected retry must never reach the upstream")
	require.Equal(t, 2, cache.counts[account.ID], "first send must not double-charge the handler's admission")
	require.ErrorIs(t, s.handleOpenAIUpstreamTransportError(ctx, nil, account, err, false), ErrOpenAIRPMExhausted)
}

func TestOpenAIRPMExpiredReservationCannotChargeOldMinute(t *testing.T) {
	account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{"base_rpm": 1}}
	s := &OpenAIGatewayService{rpmCache: &openAIRPMTestCache{counts: map[int64]int{1: 1}}}
	ctx := WithOpenAIRPMReservation(context.Background(), account, AccountRPMState{Enabled: true, ResetAt: time.Now().Add(-time.Second)})
	require.ErrorIs(t, s.acquireOpenAIRPMForSend(ctx, account), ErrOpenAIRPMExhausted)
}

func TestOpenAIRPMSchedulerSpreadsLoadAndPreservesLastHeadroom(t *testing.T) {
	for _, mode := range []string{"legacy", "legacy_batch", "advanced"} {
		t.Run(mode, func(t *testing.T) {
			resetOpenAIAdvancedSchedulerSettingCacheForTest()
			defer resetOpenAIAdvancedSchedulerSettingCacheForTest()
			accounts := []Account{
				{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Concurrency: 10, Extra: map[string]any{"base_rpm": 10}},
				{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Concurrency: 10, Extra: map[string]any{"base_rpm": 10}},
			}
			cache := &openAIRPMTestCache{counts: map[int64]int{1: 8, 2: 1}}
			cfg := &config.Config{}
			cfg.Gateway.Scheduling.LoadBatchEnabled = mode == "legacy_batch"
			cfg.Gateway.OpenAIWS.LBTopK = 1
			s := &OpenAIGatewayService{
				cfg: cfg, rpmCache: cache, accountRepo: schedulerTestOpenAIAccountRepo{accounts: accounts},
				cache:              &schedulerTestGatewayCache{sessionBindings: map[string]int64{"openai:rpm_session": 1}},
				concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{}),
			}
			if mode == "advanced" {
				s.rateLimitService = newOpenAIAdvancedSchedulerRateLimitService("true")
			}
			selectAccount := func() (*AccountSelectionResult, error) {
				selection, _, err := s.SelectAccountWithScheduler(context.Background(), nil, "", "rpm_session", "gpt-5.1", nil, OpenAIUpstreamTransportAny, false)
				if selection != nil && selection.ReleaseFunc != nil {
					selection.ReleaseFunc()
				}
				return selection, err
			}
			selection, err := selectAccount()
			require.NoError(t, err)
			require.Equal(t, int64(2), selection.Account.ID, "near-full sticky account must yield to cooler capacity")
			cache.counts[2] = 10
			selection, err = selectAccount()
			require.NoError(t, err)
			require.Equal(t, int64(1), selection.Account.ID, "80%% is a scheduling preference, not the hard cap")
			cache.counts[1] = 10
			_, err = selectAccount()
			require.ErrorIs(t, err, ErrOpenAIRPMExhausted)
		})
	}
}

func (c *openAIRPMTestCache) GetRPM(_ context.Context, id int64) (int, error) {
	return c.counts[id], c.err
}

func (c *openAIRPMTestCache) GetRPMBatch(_ context.Context, ids []int64) (map[int64]int, error) {
	c.ids = append([]int64(nil), ids...)
	return c.counts, c.err
}

func (c *openAIRPMTestCache) IncrementRPM(_ context.Context, id int64) (int, error) {
	panic("OpenAI must use atomic admission, never unconditional increment")
}

func (c *openAIRPMTestCache) TryAcquireRPM(_ context.Context, id int64, limit int) (bool, int, time.Time, error) {
	if c.err != nil {
		return false, 0, time.Time{}, c.err
	}
	allowed := c.counts[id] < limit
	if allowed {
		c.counts[id]++
	}
	return allowed, c.counts[id], time.Now().Truncate(time.Minute).Add(time.Minute), nil
}

func TestOpenAIRPMStrictScopeAndFailure(t *testing.T) {
	ctx := context.Background()
	for _, account := range []*Account{
		nil,
		{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Extra: map[string]any{"base_rpm": 1}},
		{Platform: PlatformOpenAI, Type: AccountTypeSetupToken, Extra: map[string]any{"base_rpm": 1}},
		{Platform: PlatformAnthropic, Type: AccountTypeOAuth, Extra: map[string]any{"base_rpm": 1}},
		{Platform: PlatformOpenAI, Type: AccountTypeOAuth},
	} {
		s := &OpenAIGatewayService{}
		allowed, _, err := s.TryAcquireOpenAIOAuthRPM(ctx, account)
		if !allowed || err != nil {
			t.Fatalf("out-of-scope account must bypass: %v", err)
		}
	}
	account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{"base_rpm": 1, "rpm_strategy": "sticky_exempt"}}
	s := &OpenAIGatewayService{}
	if _, _, err := s.TryAcquireOpenAIOAuthRPM(ctx, account); !errors.Is(err, ErrOpenAIRPMUnavailable) {
		t.Fatalf("missing cache must fail closed: %v", err)
	}
	cache := &openAIRPMTestCache{counts: map[int64]int{}}
	s.rpmCache = cache
	if allowed, _, err := s.TryAcquireOpenAIOAuthRPM(ctx, account); !allowed || err != nil {
		t.Fatalf("first request: %v", err)
	}
	if allowed, state, err := s.TryAcquireOpenAIOAuthRPM(ctx, account); allowed || !errors.Is(err, ErrOpenAIRPMExhausted) || state.Current != 1 {
		t.Fatalf("sticky must not exceed strict limit: allowed=%v state=%+v err=%v", allowed, state, err)
	}
	cache.err = errors.New("redis unavailable")
	if _, _, err := s.TryAcquireOpenAIOAuthRPM(ctx, account); !errors.Is(err, ErrOpenAIRPMUnavailable) {
		t.Fatalf("cache error must fail closed: %v", err)
	}
}

func TestOpenAIRPMHeadroomAndSharedParent(t *testing.T) {
	parentID := int64(1)
	accounts := []Account{
		{ID: parentID, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{"base_rpm": 10}},
		{ID: 2, ParentAccountID: &parentID, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{"base_rpm": 10}},
	}
	cache := &openAIRPMTestCache{counts: map[int64]int{parentID: 8}}
	s := &OpenAIGatewayService{rpmCache: cache}
	ctx, err := s.withOpenAIRPMPrefetch(context.Background(), accounts)
	state, found := accountRPMStateFromContext(ctx, &accounts[1])
	if err != nil || len(cache.ids) != 1 || cache.ids[0] != parentID || !found || state.Current != 8 {
		t.Fatalf("shadow must use parent counter once: ids=%v state=%v err=%v", cache.ids, state, err)
	}
	if allowed, _, err := s.OpenAIRPMSchedulable(ctx, &accounts[0], true); allowed || err != nil {
		t.Fatalf("movable sticky should yield at 80%%: %v", err)
	}
	if allowed, _, err := s.OpenAIRPMSchedulable(ctx, &accounts[0], false); !allowed || err != nil {
		t.Fatalf("hard cap must retain capacity above 80%%: %v", err)
	}
	if load := openAIRPMEffectiveLoad(ctx, &accounts[0], 20); load != 80 {
		t.Fatalf("RPM pressure must influence load, got %v", load)
	}
	if allowed, _, err := s.TryAcquireOpenAIOAuthRPM(ctx, &accounts[1]); !allowed || err != nil || cache.counts[parentID] != 9 {
		t.Fatalf("shadow acquisition must charge parent: %v", err)
	}
	if allowed, _, err := s.TryAcquireOpenAIOAuthRPM(ctx, &accounts[0]); !allowed || err != nil {
		t.Fatalf("parent should acquire last shared slot: %v", err)
	}
	if allowed, _, err := s.TryAcquireOpenAIOAuthRPM(ctx, &accounts[1]); allowed || !errors.Is(err, ErrOpenAIRPMExhausted) {
		t.Fatalf("stale scheduler snapshot must not bypass final reservation: %v", err)
	}
}

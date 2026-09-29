package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestOpenAIBPSSchedulerFallsBackBeforeWaiting(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		bpsLoad              int
		bpsAcquired          bool
		nativeAcquired       bool
		nativeExcluded       bool
		wantAccount          int64
		wantWait             bool
		subscriptionPriority bool
	}{
		{name: "native subscription does not bypass available BPS", subscriptionPriority: true, bpsAcquired: true, nativeAcquired: true, wantAccount: 1},
		{name: "native subscription can absorb full BPS", subscriptionPriority: true, bpsLoad: 100, nativeAcquired: true, wantAccount: 2},
		{name: "BPS stays preferred when both are idle", bpsAcquired: true, nativeAcquired: true, wantAccount: 1},
		{name: "full BPS falls back to idle native", bpsLoad: 100, nativeAcquired: true, wantAccount: 2},
		{name: "slot lost after load read falls back to native", nativeAcquired: true, wantAccount: 2},
		{name: "both busy still return a bounded wait", bpsLoad: 100, wantAccount: 1, wantWait: true},
		{name: "excluded native never bypasses policy", bpsLoad: 100, nativeAcquired: true, nativeExcluded: true, wantAccount: 1, wantWait: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			accounts := []Account{
				{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Concurrency: 1, Priority: 10,
					Extra: map[string]any{"openai_excel_bps": true}},
				{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, Priority: 0},
			}
			if tc.subscriptionPriority {
				accounts[1].Type = AccountTypeOAuth
				accounts[1].Credentials = map[string]any{"plan_type": "plus"}
			}
			acquired, released := []int64{}, []int64{}
			cache := schedulerTestConcurrencyCache{
				loadMap: map[int64]*AccountLoadInfo{
					1: {AccountID: 1, LoadRate: tc.bpsLoad, CurrentConcurrency: tc.bpsLoad / 100},
					2: {AccountID: 2},
				},
				acquireResults: map[int64]bool{1: tc.bpsAcquired, 2: tc.nativeAcquired},
				acquiredIDs:    &acquired, releasedIDs: &released,
			}
			cfg := &config.Config{}
			cfg.Gateway.OpenAIWS.LBTopK = 1
			svc := &OpenAIGatewayService{
				accountRepo: schedulerTestOpenAIAccountRepo{accounts: accounts},
				cfg:         cfg, concurrencyService: NewConcurrencyService(cache),
			}
			scheduler := newDefaultOpenAIAccountScheduler(svc, nil)
			req := OpenAIAccountScheduleRequest{Platform: PlatformOpenAI, RequestedModel: "gpt-6-astra", SubscriptionPriority: tc.subscriptionPriority}
			if tc.nativeExcluded {
				req.ExcludedIDs = map[int64]struct{}{2: {}}
			}
			selection, _, err := scheduler.Select(context.Background(), req)
			require.NoError(t, err)
			require.NotNil(t, selection)
			require.Equal(t, tc.wantAccount, selection.Account.ID)
			if tc.wantWait {
				require.NotNil(t, selection.WaitPlan)
				require.False(t, selection.Acquired)
			} else {
				require.Nil(t, selection.WaitPlan)
				require.True(t, selection.Acquired)
				require.NotNil(t, selection.ReleaseFunc)
				selection.ReleaseFunc()
				require.Equal(t, []int64{tc.wantAccount}, released)
			}
			if tc.nativeExcluded {
				require.NotContains(t, acquired, int64(2))
			}
		})
	}
}

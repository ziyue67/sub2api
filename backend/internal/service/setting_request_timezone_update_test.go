//go:build unit

package service

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestRequestTimezoneSettingsPersistAndInvalidate(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		repo := &settingUpdateRepoStub{}
		svc := NewSettingService(repo, &config.Config{})
		svc.openAIRequestTimezoneEnabledCache.Store(&cachedOpenAIRequestTimezoneEnabled{value: !enabled, expiresAt: time.Now().Add(time.Hour).UnixNano()})
		require.NoError(t, svc.UpdateSettings(context.Background(), &SystemSettings{OpenAIRequestTimezoneEnabled: enabled}))
		require.Equal(t, map[bool]string{false: "false", true: "true"}[enabled], repo.updates[SettingKeyOpenAIRequestTimezoneEnabled])
		require.Zero(t, svc.openAIRequestTimezoneEnabledCache.Load().(*cachedOpenAIRequestTimezoneEnabled).expiresAt)
	}
}

func TestBulkRequestTimezoneValidation(t *testing.T) {
	for _, tc := range []struct {
		platform string
		zone     any
		valid    bool
	}{
		{PlatformOpenAI, "Asia/Tokyo", true}, {PlatformOpenAI, "Unknown/Zone", false},
		{PlatformOpenAI, 123, false}, {PlatformAnthropic, "Asia/Tokyo", false},
	} {
		repo := &accountRepoStubForBulkUpdate{getByIDsAccounts: []*Account{{ID: 1, Platform: tc.platform, Type: AccountTypeOAuth}}}
		svc := &adminServiceImpl{accountRepo: repo}
		_, err := svc.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{AccountIDs: []int64{1}, Extra: map[string]any{openAIRequestTimezoneExtraKey: tc.zone}})
		if tc.valid {
			require.NoError(t, err)
			require.Equal(t, tc.zone, repo.lastBulkUpdate.Extra[openAIRequestTimezoneExtraKey])
		} else {
			require.Error(t, err)
		}
	}
}

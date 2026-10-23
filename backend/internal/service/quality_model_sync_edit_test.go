//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestQualityModelSyncEditPreservesServerRejections(t *testing.T) {
	for _, blocked := range []bool{false, true} {
		t.Run(map[bool]string{false: "stale form cannot revive rejection", true: "stale form cannot erase rejection"}[blocked], func(t *testing.T) {
			svc, repo := newGroupAllowedModelsAdminService()
			account := repo.getByIDAccounts[7]
			key := qualityUnsupportedModelKey("gpt-6-astra")
			var server, stale any = nil, "gpt-6-astra"
			if blocked {
				server, stale = stale, server
			}
			account.Extra[key] = server
			updated, err := svc.UpdateAccount(context.Background(), account.ID, &UpdateAccountInput{
				Credentials:           map[string]any{"model_mapping": map[string]any{"gpt-6-astra": "gpt-6-astra"}},
				Extra:                 map[string]any{key: stale},
				SkipMixedChannelCheck: true,
			})
			require.NoError(t, err)
			require.Equal(t, server, updated.Extra[key])
		})
	}
}

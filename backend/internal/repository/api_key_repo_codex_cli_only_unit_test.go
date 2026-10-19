package repository

import (
	"context"
	"testing"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestGroupEntityToService_PreservesCodexCLIOnly(t *testing.T) {
	got := groupEntityToService(&dbent.Group{ID: 1, Name: "cc", Platform: service.PlatformAnthropic, CodexCliOnly: true})
	require.NotNil(t, got)
	require.True(t, got.CodexCLIOnly)
}

// 鉴权快照是准入中间件读取 codex_cli_only 的唯一来源，投影漏选会让开关静默失效。
func TestAPIKeyRepository_GetByKeyForAuth_CarriesCodexCLIOnly_SQLite(t *testing.T) {
	repo, client := newAPIKeyRepoSQLite(t)
	ctx := context.Background()
	user := mustCreateAPIKeyRepoUser(t, ctx, client, "getbykey-auth-codex-cli-only@test.com")

	group, err := client.Group.Create().
		SetName("g-auth-codex-cli-only").
		SetPlatform(service.PlatformAnthropic).
		SetStatus(service.StatusActive).
		SetSubscriptionType(service.SubscriptionTypeStandard).
		SetRateMultiplier(1).
		SetCodexCliOnly(true).
		Save(ctx)
	require.NoError(t, err)

	key := &service.APIKey{UserID: user.ID, Key: "sk-getbykey-auth-codex-cli-only", Name: "codex only", GroupID: &group.ID, Status: service.StatusActive}
	require.NoError(t, repo.Create(ctx, key))

	got, err := repo.GetByKeyForAuth(ctx, key.Key)
	require.NoError(t, err)
	require.NotNil(t, got.Group)
	require.True(t, got.Group.CodexCLIOnly)
}

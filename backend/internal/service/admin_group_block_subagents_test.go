//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestAdminService_CreateGroup_PersistsBlockSubagentsOnEveryPlatform(t *testing.T) {
	for _, platform := range []string{PlatformAnthropic, PlatformOpenAI, PlatformGemini, PlatformAntigravity, PlatformComposite} {
		repo := &groupRepoStubForAdmin{}
		svc := &adminServiceImpl{groupRepo: repo}
		_, err := svc.CreateGroup(context.Background(), &CreateGroupInput{
			Name: "codex-" + platform, Platform: platform, RateMultiplier: 1, BlockSubagents: true,
		})
		require.NoError(t, err, platform)
		require.True(t, repo.created.BlockSubagents, platform)
	}
}

func TestAdminService_UpdateGroup_TogglesBlockSubagentsAndInvalidatesAuthCache(t *testing.T) {
	existing := &Group{ID: 1, Name: "cc", Platform: PlatformAnthropic, Status: StatusActive, RateMultiplier: 1}
	repo := &groupRepoStubForAdmin{getByID: existing}
	invalidator := &authCacheInvalidatorStub{}
	svc := &adminServiceImpl{groupRepo: repo, authCacheInvalidator: invalidator}
	enabled := true

	_, err := svc.UpdateGroup(context.Background(), existing.ID, &UpdateGroupInput{BlockSubagents: &enabled})
	require.NoError(t, err)
	require.True(t, repo.updated.BlockSubagents)
	require.Equal(t, []int64{existing.ID}, invalidator.groupIDs, "开关改动要立即失效该分组的 Key 鉴权缓存")

	// 不带 block_subagents 的更新保持原值
	_, err = svc.UpdateGroup(context.Background(), existing.ID, &UpdateGroupInput{Name: "renamed"})
	require.NoError(t, err)
	require.True(t, repo.updated.BlockSubagents)
}

func TestAdminService_SimpleModeIgnoresBlockSubagents(t *testing.T) {
	repo := &groupRepoStubForAdmin{}
	svc := &adminServiceImpl{cfg: &config.Config{RunMode: config.RunModeSimple}, groupRepo: repo}
	created, err := svc.CreateGroup(context.Background(), &CreateGroupInput{
		Name: "simple", Platform: PlatformAnthropic, RateMultiplier: 1, BlockSubagents: true,
	})
	require.NoError(t, err)
	require.False(t, created.BlockSubagents)
}

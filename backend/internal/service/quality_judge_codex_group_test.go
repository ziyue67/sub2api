package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestQualityJudgeUsesCodexRestrictedGroupWithoutEmployeeAdmission(t *testing.T) {
	svc, cfg, accounts, slots := judgeFixture()
	groups, ok := svc.groups.(judgeGroupsStub)
	require.True(t, ok)
	group := groups.group
	group.CodexCLIOnly = true
	group.BlockSubagents = true
	group.Platform = PlatformOpenAI
	called := false
	svc.request = func(_ context.Context, accountID int64, model, prompt string) (string, error) {
		called = true
		require.Equal(t, int64(2), accountID)
		require.Equal(t, cfg.Quality.Judge.ModelID, model)
		require.NotEmpty(t, prompt)
		return `{"verdict":"correct","reason":"equivalent"}`, nil
	}
	result := svc.Judge(context.Background(), 1, cfg, "21")
	require.True(t, called)
	require.Equal(t, "correct", result.Verdict)
	require.Equal(t, group.ID, accounts.group)
	require.Equal(t, slots.acquired, slots.released)
}

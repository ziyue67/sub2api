package reauthruntime

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The managed runtime archive is published by this Fork under its own release
// tags; the default must never point at an unrelated upstream that would 404.
// 空值等价于未设置，因此这里用 t.Setenv(…, "") 而不是 os.Unsetenv（后者需处理返回错误）。
func TestRuntimeSourceDefaultsToForkRepository(t *testing.T) {
	// t.Setenv 同时完成清理；这两个变量必须为空才能验证默认来源。
	t.Setenv(runtimeRepoEnv, "")
	t.Setenv(runtimeVersionEnv, "")

	repo, version := runtimeSource("0.2.11")
	require.Equal(t, "ziyue67/sub2api", repo)
	require.Equal(t, "0.2.11", version)
}

func TestRuntimeSourceHonorsOverridesAndRejectsUnsafeValues(t *testing.T) {
	t.Setenv(runtimeRepoEnv, "https://github.com/ranxi2001/sub2api.git")
	t.Setenv(runtimeVersionEnv, "v2.9.6")
	repo, version := runtimeSource("0.2.11")
	require.Equal(t, "ranxi2001/sub2api", repo)
	require.Equal(t, "2.9.6", version)

	// A value that is not owner/repo must not redirect the download.
	t.Setenv(runtimeRepoEnv, "evil.example.com/x")
	repo, _ = runtimeSource("0.2.11")
	require.Equal(t, defaultRuntimeRepo, repo)

	// An invalid version falls back to the application version.
	t.Setenv(runtimeRepoEnv, "ziyue67/sub2api")
	t.Setenv(runtimeVersionEnv, "not-a-version")
	_, version = runtimeSource("0.2.11")
	require.Equal(t, "0.2.11", version)
}

package repository

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func useGroupUsageRepositoryTestTimezone(t *testing.T, name string) configuredTimezone {
	t.Helper()
	loc, err := time.LoadLocation(name)
	require.NoError(t, err)
	// 分组用量汇总按 timezone 包配置的时区换算自然日。unit 构建下
	// integration_harness_test.go 的 TestMain 不参与编译，需在这里初始化。
	previous := timezone.Name()
	require.NoError(t, timezone.Init(name))
	t.Cleanup(func() {
		if err := timezone.Init(previous); err != nil {
			t.Logf("restore timezone %q: %v", previous, err)
		}
	})
	return configuredTimezone{location: loc}
}

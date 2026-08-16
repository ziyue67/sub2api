package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDeepSeekPlatformConstraintsMigration(t *testing.T) {
	content, err := FS.ReadFile("224_add_deepseek_platform_constraints.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "DROP CONSTRAINT IF EXISTS user_platform_quotas_platform_check")
	require.Contains(t, sql, "CHECK (platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'grok', 'deepseek'))")
	require.Contains(t, sql, "DROP CONSTRAINT IF EXISTS composite_model_routes_target_platform_check")
	require.Contains(t, sql, "CHECK (target_platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'grok', 'deepseek'))")
}

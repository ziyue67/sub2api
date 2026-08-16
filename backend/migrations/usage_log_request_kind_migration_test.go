package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUsageLogRequestKindMigration(t *testing.T) {
	content, err := FS.ReadFile("225_add_usage_log_request_kind.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS request_kind TEXT NOT NULL DEFAULT 'normal'")
	require.Contains(t, sql, "DROP CONSTRAINT IF EXISTS usage_logs_request_kind_check")
	require.Contains(t, sql, "CONSTRAINT usage_logs_request_kind_check")
	require.Contains(t, sql, "CHECK (request_kind IN ('normal', 'compact')) NOT VALID")
	require.NotContains(t, sql, "CREATE INDEX")
}

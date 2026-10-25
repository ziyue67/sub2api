package migrations

import (
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

func TestModerationExemptionsMigrationDefaultsAndScope(t *testing.T) {
	raw, err := FS.ReadFile("298_content_moderation_exemption_types.sql")
	require.NoError(t, err)
	sql := strings.ToUpper(string(raw))
	require.Equal(t, 2, strings.Count(sql, "KEYWORD_EXEMPT BOOLEAN NOT NULL DEFAULT FALSE"))
	require.Equal(t, 2, strings.Count(sql, "API_AUDIT_EXEMPT BOOLEAN NOT NULL DEFAULT FALSE"))
	for _, line := range strings.Split(sql, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			continue
		}
		for _, forbidden := range []string{"UPDATE ", "DELETE ", "DROP ", "ALTER TABLE USERS", "AUTO_BAN_EXEMPT"} {
			require.NotContains(t, line, forbidden)
		}
	}
}

func TestModerationEmailExemptionMigrationDefaultsAndScope(t *testing.T) {
	raw, err := FS.ReadFile("301_content_moderation_email_exemption.sql")
	require.NoError(t, err)
	sql := strings.ToUpper(string(raw))
	require.Equal(t, 2, strings.Count(sql, "ADD COLUMN IF NOT EXISTS EMAIL_EXEMPT BOOLEAN NOT NULL DEFAULT FALSE"))
	require.Equal(t, 1, strings.Count(sql, "ALTER TABLE CONTENT_MODERATION_USER_STATE"))
	require.Equal(t, 1, strings.Count(sql, "ALTER TABLE CONTENT_MODERATION_USER_ACTIONS"))
	for _, line := range strings.Split(sql, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			continue
		}
		for _, forbidden := range []string{"UPDATE ", "DELETE ", "DROP ", "ALTER TABLE USERS", "AUTO_BAN_EXEMPT", "KEYWORD_EXEMPT", "API_AUDIT_EXEMPT"} {
			require.NotContains(t, line, forbidden)
		}
	}
}

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

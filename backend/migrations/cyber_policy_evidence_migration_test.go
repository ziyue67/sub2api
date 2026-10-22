package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCyberPolicyEvidenceMigration(t *testing.T) {
	raw, err := FS.ReadFile("297_cyber_policy_evidence.sql")
	require.NoError(t, err)
	sql := strings.ToUpper(string(raw))
	for _, want := range []string{
		"ALTER TABLE CONTENT_MODERATION_LOGS ADD COLUMN IF NOT EXISTS EVIDENCE_STATUS",
		"CREATE TABLE IF NOT EXISTS CYBER_POLICY_EVIDENCE",
		"LOG_ID BIGINT PRIMARY KEY REFERENCES CONTENT_MODERATION_LOGS(ID) ON DELETE CASCADE",
		"CIPHERTEXT TEXT NOT NULL CHECK",
		"EXPIRES_AT TIMESTAMPTZ NOT NULL",
		"IDX_CYBER_POLICY_EVIDENCE_EXPIRY",
		"IDX_CYBER_POLICY_EVIDENCE_PENDING",
	} {
		require.Contains(t, sql, want)
	}
	// Evidence content is ciphertext only; no request-body column may exist.
	for _, disallowed := range []string{"DROP COLUMN", "INPUT_EXCERPT", "REQUEST_BODY", "MESSAGES ", "REASON TEXT"} {
		require.NotContains(t, sql, disallowed)
	}
}

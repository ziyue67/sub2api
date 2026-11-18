package repository

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// usageInsertColumnIndex keeps wiring assertions tied to the persisted column,
// rather than to a moving offset from the end of the argument list.
func usageInsertColumnIndex(t *testing.T, name string) int {
	t.Helper()
	columns := strings.Split(usageLogSelectColumns, ",")
	require.Equal(t, "id", strings.TrimSpace(columns[0]))
	require.Len(t, columns, len(usageLogInsertArgTypes)+1)
	for i, column := range columns[1:] {
		if strings.TrimSpace(column) == name {
			return i
		}
	}
	t.Fatalf("missing usage insert column %q", name)
	return -1
}

func TestCacheCreationBillingPersistence(t *testing.T) {
	audit := &service.CacheCreationBillingAudit{Version: 1, Enabled: true, Status: "converted", OriginalCreationTokens: 100, Original5mTokens: 100, ConvertedTokens: 100, OriginalCreationCost: 0.0005, BilledCreationCost: 0.0008, ActualCostDelta: 0.000255}
	prepared := prepareUsageLogInsert(&service.UsageLog{CacheCreationBilling: audit})
	require.Len(t, prepared.args, len(usageLogInsertArgTypes))
	columns := strings.Split(usageLogSelectColumns, ", ")
	// The select row begins with id; resolve the audit by its persisted name
	// so appending customer_cost_details cannot redirect the scanner fixture.
	auditIndex := usageInsertColumnIndex(t, "cache_creation_billing")
	require.Equal(t, "queue_timing", columns[auditIndex+2])
	require.Equal(t, "platform", columns[auditIndex+3])
	require.Equal(t, "customer_cost_details", columns[auditIndex+4])
	encoded, ok := prepared.args[auditIndex].(string)
	require.True(t, ok, "cache_creation_billing must occupy its named SQL argument")
	require.NotEmpty(t, encoded, "cache_creation_billing must be serialized into args")
	var decoded service.CacheCreationBillingAudit
	require.NoError(t, json.Unmarshal([]byte(encoded), &decoded))
	require.Equal(t, *audit, decoded)
	require.Nil(t, nullCacheCreationBillingJSON(nil))
	// Start from a fully typed zero row built by the scanner itself.
	log, err := scanUsageLog(cacheBillingScanner{encoded: encoded})
	require.NoError(t, err)
	require.Equal(t, audit, log.CacheCreationBilling)
	_, err = scanUsageLog(cacheBillingScanner{encoded: "{"})
	require.ErrorContains(t, err, "decode cache creation billing audit")
}

type cacheBillingScanner struct{ encoded string }

func (s cacheBillingScanner) Scan(dest ...any) error {
	for index, column := range strings.Split(usageLogSelectColumns, ",") {
		if strings.TrimSpace(column) != "cache_creation_billing" {
			continue
		}
		if index >= len(dest) {
			return fmt.Errorf("missing audit destination at column %d", index)
		}
		audit, ok := dest[index].(*sql.NullString)
		if !ok {
			return fmt.Errorf("expected audit destination *sql.NullString, got %T", dest[index])
		}
		*audit = sql.NullString{Valid: true, String: s.encoded}
		return nil
	}
	return fmt.Errorf("missing cache_creation_billing select column")
}

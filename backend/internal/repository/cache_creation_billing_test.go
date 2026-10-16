package repository

import (
	"database/sql"
	"encoding/json"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

func TestCacheCreationBillingPersistence(t *testing.T) {
	audit := &service.CacheCreationBillingAudit{Version: 1, Enabled: true, Status: "converted", OriginalCreationTokens: 100, Original5mTokens: 100, ConvertedTokens: 100, OriginalCreationCost: 0.0005, BilledCreationCost: 0.0008, ActualCostDelta: 0.000255}
	prepared := prepareUsageLogInsert(&service.UsageLog{CacheCreationBilling: audit})
	require.Len(t, prepared.args, len(usageLogInsertArgTypes))
	columns := strings.Split(usageLogSelectColumns, ", ")
	require.Equal(t, "cache_creation_billing", columns[len(columns)-1])
	require.Equal(t, "jsonb", usageLogInsertArgTypes[len(usageLogInsertArgTypes)-1])
	var decoded service.CacheCreationBillingAudit
	require.NoError(t, json.Unmarshal([]byte(prepared.args[len(prepared.args)-1].(string)), &decoded))
	require.Equal(t, *audit, decoded)
	require.Nil(t, nullCacheCreationBillingJSON(nil))
	// Start from a fully typed zero row built by the scanner itself.
	log, err := scanUsageLog(cacheBillingScanner{encoded: prepared.args[len(prepared.args)-1].(string)})
	require.NoError(t, err)
	require.Equal(t, audit, log.CacheCreationBilling)
	_, err = scanUsageLog(cacheBillingScanner{encoded: "{"})
	require.ErrorContains(t, err, "decode cache creation billing audit")
}

type cacheBillingScanner struct{ encoded string }

func (s cacheBillingScanner) Scan(dest ...any) error {
	// Go zero values already satisfy every field; set only the trailing audit.
	*dest[len(dest)-1].(*sql.NullString) = sql.NullString{Valid: true, String: s.encoded}
	return nil
}

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

func TestCacheCreationBillingPersistence(t *testing.T) {
	audit := &service.CacheCreationBillingAudit{Version: 1, Enabled: true, Status: "converted", OriginalCreationTokens: 100, Original5mTokens: 100, ConvertedTokens: 100, OriginalCreationCost: 0.0005, BilledCreationCost: 0.0008, ActualCostDelta: 0.000255}
	prepared := prepareUsageLogInsert(&service.UsageLog{CacheCreationBilling: audit})
	require.Len(t, prepared.args, len(usageLogInsertArgTypes))
	columns := strings.Split(usageLogSelectColumns, ", ")
	// usageLogSelectColumns 以 id 开头，比 usageLogInsertArgTypes 多一列；
	// 尾部列序：cache_creation_billing, queue_timing, platform。
	require.Equal(t, "cache_creation_billing", columns[len(columns)-3])
	require.Equal(t, "queue_timing", columns[len(columns)-2])
	require.Equal(t, "platform", columns[len(columns)-1])
	// prepared.args 中 cache_creation_billing 是唯一包含 version/enabled 的 JSON 字符串。
	var encoded string
	for _, arg := range prepared.args {
		if str, isStr := arg.(string); isStr && strings.Contains(str, `"original_creation_tokens"`) {
			encoded = str
			break
		}
	}
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
	// Go zero values already satisfy every field; set only cache_creation_billing
	// (尾部列序：cache_creation_billing, queue_timing, platform，审计值在倒数第三位)。
	audit, ok := dest[len(dest)-3].(*sql.NullString)
	if !ok {
		return fmt.Errorf("expected audit destination *sql.NullString, got %T", dest[len(dest)-3])
	}
	*audit = sql.NullString{Valid: true, String: s.encoded}
	return nil
}

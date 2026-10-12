package dto

import (
	"encoding/json"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestExcelCredentialStateIsTypedAndRedacted(t *testing.T) {
	account := &service.Account{ID: 7, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Extra: map[string]any{"openai_excel_bps": true, service.ExcelBPSCredentialStateExtraKey: map[string]any{"status": "revoked", "error_code": "token_revoked", "private": "DO_NOT_EXPORT"}}}
	full := AccountFromServiceShallow(account)
	require.NotContains(t, full.Extra, service.ExcelBPSCredentialStateExtraKey)
	require.Equal(t, "revoked", full.ExcelBPSCredentialState.Status)
	compact := AccountListItemFromAccount(full)
	require.Equal(t, full.ExcelBPSCredentialState, compact.ExcelBPSCredentialState)
	for _, value := range []any{full, compact} {
		raw, err := json.Marshal(value)
		require.NoError(t, err)
		require.NotContains(t, string(raw), "DO_NOT_EXPORT")
	}
}

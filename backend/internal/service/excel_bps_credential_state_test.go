package service

import (
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestExcelBPSCredentialState(t *testing.T) {
	now := time.Now()
	account := excelAccount()
	account.Credentials["client_id"] = "independent-grant"
	require.Equal(t, "unknown", ExcelBPSCredentialStateFromAccount(account, now).Status)
	account.Extra[ExcelBPSAuthorizationPendingKey] = true
	require.Equal(t, "pending", ExcelBPSCredentialStateFromAccount(account, now).Status)
	delete(account.Extra, ExcelBPSAuthorizationPendingKey)
	state := ExcelBPSGrantState(now.Add(time.Hour).Unix())
	account.Extra[ExcelBPSCredentialStateExtraKey] = state
	require.Equal(t, "not_expired", ExcelBPSCredentialStateFromAccount(account, now).Status)
	require.Equal(t, "expired", ExcelBPSCredentialStateFromAccount(account, now.Add(2*time.Hour)).Status)
	account.Extra[ExcelBPSCredentialStateExtraKey] = ExcelBPSGrantFailure("token_revoked")
	account.Schedulable = false
	got := ExcelBPSCredentialStateFromAccount(account, now)
	require.Equal(t, "revoked", got.Status)
	require.True(t, got.RequiresManualResume)
	require.Equal(t, "authentication_failed", ExcelBPSGrantFailure("PRIVATE_UPSTREAM_TOKEN").ErrorCode)
}

func TestExcelBPSCredentialDiagnosisIsServerOwned(t *testing.T) {
	key := ExcelBPSCredentialStateExtraKey
	require.NotContains(t, MergeOpenAICodexTicketExtra(map[string]any{key: "forged"}, nil), key)
	result := MergeOpenAICodexTicketExtra(map[string]any{key: "stale", "unrelated": true}, map[string]any{key: "current"})
	require.Equal(t, "current", result[key])
	require.Equal(t, true, result["unrelated"])
}

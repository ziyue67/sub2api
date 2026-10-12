package service

import (
	"encoding/json"
	"time"
)

const ExcelBPSCredentialStateExtraKey = "openai_excel_bps_credential_state"

// Safe, read-only metadata: no tokens, ciphertext, credential hashes or upstream bodies.
type ExcelBPSCredentialState struct {
	Status               string     `json:"status"`
	ExpiresAt            *time.Time `json:"expires_at,omitempty"`
	ObservedAt           *time.Time `json:"observed_at,omitempty"`
	ErrorCode            string     `json:"error_code,omitempty"`
	RequiresManualResume bool       `json:"requires_manual_resume,omitempty"`
}

func ExcelBPSGrantState(expiresAt int64) ExcelBPSCredentialState {
	now := time.Now().UTC()
	state := ExcelBPSCredentialState{Status: "unknown", ObservedAt: &now}
	if expiresAt > 0 {
		expiry := time.Unix(expiresAt, 0).UTC()
		state.ExpiresAt = &expiry
		state.Status = "not_expired"
	}
	return state
}

func ExcelBPSGrantFailure(code string) ExcelBPSCredentialState {
	now := time.Now().UTC()
	state := ExcelBPSCredentialState{Status: "auth_failed", ErrorCode: "authentication_failed", ObservedAt: &now}
	if code == "token_revoked" || code == "token_invalidated" {
		state.Status = "revoked"
		state.ErrorCode = code
	}
	return state
}

func ExcelBPSCredentialStateFromAccount(account *Account, now time.Time) *ExcelBPSCredentialState {
	if account == nil || account.Platform != PlatformOpenAI || account.Type != AccountTypeOAuth || (account.Extra["openai_excel_bps"] != true && !account.IsExcelOAuth()) {
		return nil
	}
	state := ExcelBPSCredentialState{Status: "unknown"}
	if account.IsExcelOAuth() {
		state.ExpiresAt = account.GetCredentialAsTime("expires_at")
		if state.ExpiresAt != nil {
			state.Status = "not_expired"
		}
	} else if raw, ok := account.Extra[ExcelBPSCredentialStateExtraKey]; ok {
		encoded, err := json.Marshal(raw)
		if err == nil {
			_ = json.Unmarshal(encoded, &state)
		}
	}
	// Only emit bounded, server-defined statuses/codes even for legacy imported rows.
	switch state.Status {
	case "not_expired", "expired", "revoked", "auth_failed", "unknown":
	default:
		state.Status = "unknown"
	}
	switch state.ErrorCode {
	case "token_revoked", "token_invalidated", "authentication_failed":
	default:
		state.ErrorCode = ""
	}
	if state.Status != "revoked" && state.ExpiresAt != nil && !now.Before(*state.ExpiresAt) {
		state.Status = "expired"
	}
	if account.Extra[ExcelBPSAuthorizationPendingKey] == true && state.Status != "revoked" && state.Status != "auth_failed" && state.Status != "expired" {
		state.Status = "pending"
	}
	state.RequiresManualResume = !account.Schedulable || account.Status != StatusActive
	return &state
}

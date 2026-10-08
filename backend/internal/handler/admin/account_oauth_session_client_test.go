package admin

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestOAuthSessionImportPreservesClientAndTokenBundle(t *testing.T) {
	for _, wrapper := range []string{"", "tokens", "credentials", "session_info"} {
		t.Run(wrapper, func(t *testing.T) {
			expires := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
			bundle := map[string]any{
				"access_token": "opaque-access", "refresh_token": "paired-refresh", "id_token": "paired-id",
				"client_id": "excel-client-test", "chatgpt_account_id": "workspace", "expires_at": expires.Format(time.RFC3339),
			}
			raw := bundle
			if wrapper != "" {
				raw = map[string]any{wrapper: bundle}
			}
			item, err := normalizeCodexImportEntry(codexImportEntry{Index: 1, Value: raw})
			require.NoError(t, err)
			for key, want := range bundle {
				require.Equal(t, want, item.Credentials[key], key)
			}
		})
	}
}

func TestOAuthSessionImportClientIsolationAndReimport(t *testing.T) {
	for _, existingClient := range []string{"", openai.ClientID, "mobile-client-test"} {
		t.Run(existingClient, func(t *testing.T) {
			existing := service.Account{ID: 42, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
				Credentials: map[string]any{"chatgpt_account_id": "workspace", "chatgpt_user_id": "member",
					"access_token": "old-access", "refresh_token": "old-refresh", "client_id": existingClient}}
			svc := newCodexImportMemoryAdminService([]service.Account{existing})
			handler := NewAccountHandler(svc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
			raw := buildCodexRefreshImportValue(t, "workspace", "member", "excel-refresh")
			raw["client_id"] = "excel-client-test"
			req := CodexSessionImportRequest{SkipDefaultGroupBind: boolPtr(true)}
			entries := []codexImportEntry{{Index: 1, Value: raw}}
			result, err := handler.importCodexSessions(context.Background(), req, entries)
			require.NoError(t, err)
			require.Equal(t, 1, result.Created)
			require.Zero(t, result.Updated)
			require.Empty(t, svc.updatedAccounts)
			require.Equal(t, "old-refresh", svc.accounts[0].Credentials["refresh_token"])
			require.Equal(t, "excel-client-test", svc.createdAccounts[0].Credentials["client_id"])

			raw["refresh_token"] = "excel-refresh-rotated"
			result, err = handler.importCodexSessions(context.Background(), req, entries)
			require.NoError(t, err)
			require.Equal(t, 1, result.Updated)
			require.Zero(t, result.Created)
			require.NotEqual(t, int64(42), svc.updatedAccounts[0].id)
			require.Equal(t, "excel-refresh-rotated", svc.updatedAccounts[0].input.Credentials["refresh_token"])
		})
	}
}

func TestOAuthSessionBatchKeepsDifferentClientsSeparate(t *testing.T) {
	svc := newCodexImportMemoryAdminService(nil)
	handler := NewAccountHandler(svc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	var entries []codexImportEntry
	for n, client := range []string{openai.ClientID, "excel-client-test", "excel-client-test"} {
		raw := buildCodexRefreshImportValue(t, "workspace", "member", "paired-refresh")
		raw["client_id"] = client
		entries = append(entries, codexImportEntry{Index: n + 1, Value: raw})
	}
	result, err := handler.importCodexSessions(context.Background(), CodexSessionImportRequest{}, entries)
	require.NoError(t, err)
	require.Equal(t, 2, result.Created)
	require.Equal(t, 1, result.Skipped)
}

func TestOAuthSessionAccessOnlyRetainsExplicitClient(t *testing.T) {
	raw := buildCodexAccessOnlyImportValue(t, "workspace", "member")
	raw["client_id"] = "excel-client-test"
	item, err := normalizeCodexImportEntry(codexImportEntry{Index: 1, Value: raw})
	require.NoError(t, err)
	merged := mergeCodexImportCredentials(map[string]any{}, item.Credentials, item)
	require.Equal(t, "excel-client-test", merged["client_id"])
	require.NotContains(t, merged, "refresh_token")
}

func TestOAuthSessionCustomClientKeepsWorkspacesSeparate(t *testing.T) {
	svc := newCodexImportMemoryAdminService(nil)
	handler := NewAccountHandler(svc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	var entries []codexImportEntry
	for n, workspace := range []string{"workspace-a", "workspace-b"} {
		raw := buildCodexRefreshImportValue(t, workspace, "same-user", "refresh")
		raw["client_id"] = "excel-client-test"
		entries = append(entries, codexImportEntry{Index: n + 1, Value: raw})
	}
	result, err := handler.importCodexSessions(context.Background(), CodexSessionImportRequest{}, entries)
	require.NoError(t, err)
	require.Equal(t, 2, result.Created)
	require.Zero(t, result.Updated)
	require.Zero(t, result.Skipped)
}

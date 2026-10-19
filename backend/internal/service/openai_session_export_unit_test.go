package service

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSessionExportResultStoreIsOneShot(t *testing.T) {
	store := newSessionExportResultStore()
	body := json.RawMessage(`{"accessToken":"token","sessionToken":"session","expires":"2099-01-01T00:00:00Z","user":{"id":"user-1","email":"owner@example.com"},"account":{"plan":"plus"},"unknown":[1,true,null]}`)
	store.put(7, body)
	body[0] = 'x'
	got, ok := store.consume(7)
	require.True(t, ok)
	require.JSONEq(t, `{"accessToken":"token","sessionToken":"session","expires":"2099-01-01T00:00:00Z","user":{"id":"user-1","email":"owner@example.com"},"account":{"plan":"plus"},"unknown":[1,true,null]}`, string(got))
	_, ok = store.consume(7)
	require.False(t, ok)
}

func TestSessionExportResultStoreExpiresEntries(t *testing.T) {
	store := newSessionExportResultStore()
	store.results[9] = sessionExportResult{body: json.RawMessage(`{}`), expiresAt: time.Now().Add(-time.Second)}
	_, ok := store.consume(9)
	require.False(t, ok)
}

func TestCompactSessionExportPayloadKeepsOnlyImportCredentials(t *testing.T) {
	body, err := compactSessionExportPayload("access", "session")
	require.NoError(t, err)
	require.JSONEq(t, `{"accessToken":"access","sessionToken":"session"}`, string(body))
}

func TestNormalizeSessionExportErrorCodeDoesNotStoreWorkerDetails(t *testing.T) {
	require.Equal(t, "totp_rejected", normalizeSessionExportErrorCode("totp_rejected"))
	require.Equal(t, "worker_failed", normalizeSessionExportErrorCode("password=secret; stack trace"))
	require.Equal(t, "worker_failed", normalizeSessionExportErrorCode(""))
}

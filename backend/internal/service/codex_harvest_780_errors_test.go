package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
)

func TestCodex780StreamErrorsAreTerminalAndRedacted(t *testing.T) {
	for _, tt := range []struct {
		body, kind string
		terminal   bool
	}{
		{`{"type":"error","error":{"code":"invalid_api_key","message":"PRIVATE_TOKEN"}}`, "account_error", true},
		{`{"type":"response.failed","response":{"error":{"code":"model_not_found","message":"PRIVATE_TOKEN"}}}`, "response_incomplete_or_error", true},
		{`{"type":"error","error":{"type":"rate_limit_error"}}`, "rate_limited", false},
		{`{"type":"error","error":{"code":"PRIVATE_TOKEN"}}`, "response_incomplete_or_error", false},
		{`{"type":"error","status":403}`, "account_error", true},
	} {
		err := readCodex780Created(strings.NewReader("data: "+tt.body+"\n\n"), "gpt-6-astra")
		var mint *codexMintError
		require.ErrorAs(t, err, &mint)
		require.Equal(t, tt.kind, mint.kind)
		require.Equal(t, tt.terminal, codex780TerminalFailure(codexHarvestProbeResult{Err: err, Status: 200}))
		require.NotContains(t, err.Error(), "PRIVATE_TOKEN")
		require.EqualError(t, codex780EventError([]byte(tt.body), ""), err.Error(), "WS and SSE share error classification")
	}
	require.Nil(t, codex780EventError([]byte(`{"type":"error","status":403}`), "response.created"))
	for _, status := range []int{400, 404, 422} {
		require.True(t, codex780TerminalFailure(codexHarvestProbeResult{Status: status}))
	}
	for _, status := range []int{200, 429, 500, 503} {
		require.False(t, codex780TerminalFailure(codexHarvestProbeResult{Status: status}))
	}
}

func TestCodex780WebSocketRejectsTerminalErrorWithoutCookies(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()
		if _, _, err = conn.Read(r.Context()); err != nil {
			return
		}
		raw, _ := json.Marshal(map[string]any{"type": "error", "error": map[string]string{"code": "invalid_api_key", "message": "PRIVATE_TOKEN"}})
		_ = conn.Write(r.Context(), coderws.MessageText, raw)
		_, _, _ = conn.Read(r.Context())
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL, nil)
	require.NoError(t, err)
	payload, err := json.Marshal(map[string]any{"model": "gpt-6-astra", "stream": true})
	require.NoError(t, err)
	out := requestCodex780WS(req, "", "", payload, nil, "unified-88", "gpt-6-astra")
	require.Equal(t, http.StatusSwitchingProtocols, out.Status)
	require.True(t, codex780TerminalFailure(out))
	var mint *codexMintError
	require.ErrorAs(t, out.Err, &mint)
	require.Equal(t, "account_error", mint.kind)
	require.NotContains(t, out.Err.Error(), "PRIVATE_TOKEN")
}

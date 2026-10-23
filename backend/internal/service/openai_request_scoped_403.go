package service

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
)

// isOpenAIRequestScoped403 recognizes explicit request/transport failures from
// OpenAI-compatible relays. Neither is evidence that the local credential is
// invalid, even when the relay reports HTTP 403. Keep this narrow so genuine
// account and workspace denials retain their existing handling.
func isOpenAIRequestScoped403(account *Account, statusCode int, body []byte) bool {
	if account == nil || account.Platform != PlatformOpenAI || statusCode != http.StatusForbidden {
		return false
	}
	var payload struct {
		Error struct {
			Code    string `json:"code"`
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	// Some relays append response.failed SSE after their JSON error. Decode the
	// first complete object, trusting only its error fields, never echoed input
	// or a later event in the body.
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&payload); err != nil {
		return false
	}
	code := strings.ToLower(strings.TrimSpace(payload.Error.Code))
	errorType := strings.ToLower(strings.TrimSpace(payload.Error.Type))
	if code == "content_policy_violation" || (code == "" && errorType == "content_policy_violation") {
		return true
	}
	return errorType == "upstream_error" && (code == "" || code == "upstream_error") &&
		strings.EqualFold(strings.TrimSpace(payload.Error.Message),
			"failed to WebSocket dial: expected handshake response status code 101 but got 403")
}

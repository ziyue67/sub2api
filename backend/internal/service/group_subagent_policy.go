package service

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// IsCodexSubagentRequest uses explicit client protocol metadata only. Prompt
// text, tool definitions and ordinary thread IDs are not subagent evidence.
// These client-supplied markers are a usage policy, not client attestation.
func IsCodexSubagentRequest(c *gin.Context, body []byte) bool {
	if c != nil && c.Request != nil {
		for _, header := range []string{openAISubagentHeader, codexParentThreadIDHeader} {
			for _, value := range c.Request.Header.Values(header) {
				if strings.TrimSpace(value) != "" {
					return true
				}
			}
		}
		for _, metadata := range c.Request.Header.Values(codexTurnMetadataHeader) {
			if isSubagentTurnMetadata(metadata) {
				return true
			}
		}
	}
	// Inspect both envelope and payload so empty outer metadata cannot hide a
	// marker in the nested response.create shape (or the reverse).
	for _, view := range []gjson.Result{gjson.ParseBytes(body), openAIRequestPayloadView(body)} {
		metadata := view.Get("client_metadata")
		for _, key := range []string{openAISubagentHeader, codexParentThreadIDHeader} {
			if value := metadata.Get(key); value.Type == gjson.String && strings.TrimSpace(value.String()) != "" {
				return true
			}
		}
		turn := metadata.Get(codexTurnMetadataHeader)
		if (turn.Type == gjson.String || turn.IsObject()) && isSubagentTurnMetadata(turn.String()) {
			return true
		}
	}
	return false
}

func isSubagentTurnMetadata(raw string) bool {
	return codexSubagentKindFromMetadata(raw) != "" || codexParentThreadIDFromMetadata(raw) != ""
}

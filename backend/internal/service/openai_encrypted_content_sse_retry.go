package service

import (
	"strings"

	"github.com/tidwall/gjson"
)

// Adapted from Wei-Shaw/sub2api#8095 (356063e376a6173f1b8424280247dc1df7a4ce32).
// The fork additionally preserves #484's single-reasoning-only recovery policy.
type openAIEncryptedContentRecoverySignal struct {
	payload   []byte
	eventType string
}

func (*openAIEncryptedContentRecoverySignal) Error() string {
	return "upstream rejected encrypted reasoning before output"
}

type openAIEncryptedContentRecoveryContextKey struct{}

// Inspect the attempt body only after an explicit rejection; successful requests
// do not decode or hash their history for this recovery.
type openAIEncryptedContentRecoveryPolicy struct {
	body []byte
}

func isOpenAIInvalidEncryptedContentError(code, message string) bool {
	switch strings.ToLower(strings.TrimSpace(code)) {
	case "invalid_encrypted_content":
		return true
	case "thinking_signature_invalid":
		return strings.Contains(message, "The encrypted content") &&
			strings.Contains(message, "could not be verified") &&
			strings.Contains(message, "could not be decrypted or parsed")
	default:
		return false
	}
}

func isOpenAIInvalidEncryptedContentSSE(payload []byte, eventType string) bool {
	if !gjson.ValidBytes(payload) {
		return false
	}
	path := ""
	switch eventType {
	case "response.failed":
		path = "response.error."
	case "error":
		if gjson.GetBytes(payload, "error").IsObject() {
			path = "error."
		}
	default:
		return false
	}
	return isOpenAIInvalidEncryptedContentError(
		gjson.GetBytes(payload, path+"code").String(),
		gjson.GetBytes(payload, path+"message").String(),
	)
}

func openAICanRecoverEncryptedReasoningSSE(body []byte) bool {
	input := gjson.GetBytes(body, "input")
	count := 0
	unsafe := false
	visit := func(item gjson.Result) {
		kind := strings.TrimSpace(item.Get("type").String())
		if kind == "compaction" || kind == "compaction_summary" {
			unsafe = true
			return
		}
		encrypted := item.Get("encrypted_content")
		if encrypted.Type != gjson.String || encrypted.String() == "" {
			return
		}
		count++
		if kind != "reasoning" {
			unsafe = true
		}
	}
	if input.IsArray() {
		input.ForEach(func(_, item gjson.Result) bool { visit(item); return !unsafe && count <= 1 })
	} else if input.IsObject() {
		visit(input)
	}
	return !unsafe && count == 1
}

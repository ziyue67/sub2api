package service

import (
	"context"
	"github.com/tidwall/gjson"
)

type openAIEncryptedReasoningAffinityKey struct{}
type openAIEncryptedTaskAffinityKey struct{}

// WithOpenAIEncryptedReasoningAffinity marks a Responses admission once, before
// account selection. Opaque task/history items get a separate soft preference:
// unlike the existing reasoning policy, it must not imply hard account ownership.
func WithOpenAIEncryptedReasoningAffinity(ctx context.Context, body []byte) context.Context {
	input := gjson.GetBytes(body, "input")
	found := false
	opaque := false
	visit := func(item gjson.Result) {
		if item.Get("type").String() == "reasoning" {
			e := item.Get("encrypted_content")
			found = found || e.Type == gjson.String && e.String() != ""
		}
		opaque = opaque || openAIHasOpaqueEncryptedTask(item)
	}
	if input.IsArray() {
		input.ForEach(func(_, item gjson.Result) bool { visit(item); return !found || !opaque })
	} else if input.IsObject() {
		visit(input)
	}
	ctx = context.WithValue(ctx, openAIEncryptedReasoningAffinityKey{}, found)
	return context.WithValue(ctx, openAIEncryptedTaskAffinityKey{}, opaque)
}

// Only inspect protocol fields, not strings containing serialized JSON, tool
// arguments/results, or arbitrary nested objects. No task data is changed.
func openAIHasOpaqueEncryptedTask(item gjson.Result) bool {
	kind := item.Get("type").String()
	switch kind {
	case "agent_message", "compaction", "compaction_summary":
	default:
		return false
	}
	if encrypted := item.Get("encrypted_content"); encrypted.Type == gjson.String && encrypted.String() != "" {
		return true
	}
	if kind != "agent_message" {
		return false
	}
	content := item.Get("content")
	if !content.IsArray() {
		return false
	}
	found := false
	content.ForEach(func(_, block gjson.Result) bool {
		encrypted := block.Get("encrypted_content")
		found = block.Get("type").String() == "encrypted_content" && encrypted.Type == gjson.String && encrypted.String() != ""
		return !found
	})
	return found
}

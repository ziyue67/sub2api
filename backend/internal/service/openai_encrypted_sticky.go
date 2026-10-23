package service

import (
	"context"
	"github.com/tidwall/gjson"
)

type openAIEncryptedReasoningAffinityKey struct{}

// WithOpenAIEncryptedReasoningAffinity marks a Responses admission once, before
// account selection. It does not change hard admission or capacity decisions.
func WithOpenAIEncryptedReasoningAffinity(ctx context.Context, body []byte) context.Context {
	input := gjson.GetBytes(body, "input")
	found := false
	visit := func(item gjson.Result) {
		if item.Get("type").String() == "reasoning" {
			e := item.Get("encrypted_content")
			found = found || e.Type == gjson.String && e.String() != ""
		}
	}
	if input.IsArray() {
		input.ForEach(func(_, item gjson.Result) bool { visit(item); return !found })
	} else if input.IsObject() {
		visit(input)
	}
	return context.WithValue(ctx, openAIEncryptedReasoningAffinityKey{}, found)
}

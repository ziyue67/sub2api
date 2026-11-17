package service

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/tidwall/gjson"
)

type openAITextGroupFailoverKey struct{}

// WithOpenAITextGroupFailover is a server-side opt-in for HTTP text handlers
// that can forward both OpenAI and compatible provider requests. Media,
// compact, WebSocket and other gateway handlers must not opt in. Provider-bound
// state cannot be replayed to another platform even through a text endpoint.
func WithOpenAITextGroupFailover(ctx context.Context, body []byte) context.Context {
	allowed := gjson.ValidBytes(body) && !openAIGroupAntiDistillationRequestHasOpaqueHistory(body)
	view := openAIRequestPayloadView(body)
	var opaque func(gjson.Result) bool
	opaque = func(value gjson.Result) bool {
		if value.IsObject() {
			if value.Get("type").String() == "item_reference" || value.Get("type").String() == "compaction" {
				return true
			}
			if encrypted := value.Get("encrypted_content"); encrypted.Exists() && encrypted.Type != gjson.Null && encrypted.String() != "" {
				return true
			}
		}
		found := false
		if value.IsObject() || value.IsArray() {
			value.ForEach(func(_, child gjson.Result) bool {
				found = opaque(child)
				return !found
			})
		}
		return found
	}
	allowed = allowed && !opaque(view.Get("input")) && !opaque(view.Get("messages"))
	return context.WithValue(ctx, openAITextGroupFailoverKey{}, allowed)
}

func compatibleAPIKeyGroupFailover(ctx context.Context, from, to string) bool {
	if from == to {
		return true
	}
	allowed, _ := ctx.Value(openAITextGroupFailoverKey{}).(bool)
	return allowed && domain.UsesOpenAIGateway(from) && domain.UsesOpenAIGateway(to)
}

package service

import (
	"context"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/tidwall/gjson"
)

type openAIImageRequestModelContextKey struct{}

// WithOpenAIImageGenerationRequest records the image model separately from the
// outer text model. Recompute it for each WS turn so image intent cannot leak
// into a later text request. Passive Codex namespace declarations stay text.
func WithOpenAIImageGenerationRequest(ctx context.Context, requestedModel string, body []byte) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	intent := IsExplicitImageGenerationIntent(openAIResponsesEndpoint, requestedModel, body)
	ctx = context.WithValue(ctx, ctxkey.OpenAIImageGenerationIntent, intent)
	model := ""
	if intent {
		model = "gpt-image-2"
		if isOpenAIImageGenerationModel(requestedModel) {
			model = strings.TrimSpace(requestedModel)
		} else if bodyModel := gjson.GetBytes(body, "model").String(); isOpenAIImageGenerationModel(bodyModel) {
			model = strings.TrimSpace(bodyModel)
		} else {
			gjson.GetBytes(body, "tools").ForEach(func(_, tool gjson.Result) bool {
				if openAIJSONString(tool.Get("type")) != "image_generation" {
					return true
				}
				if explicit := openAIJSONString(tool.Get("model")); explicit != "" {
					model = explicit
				}
				return false
			})
		}
	}
	return context.WithValue(ctx, openAIImageRequestModelContextKey{}, model)
}

func openAIImageRequestModel(ctx context.Context, requestedModel string) string {
	if ctx != nil {
		if model, _ := ctx.Value(openAIImageRequestModelContextKey{}).(string); model != "" {
			return model
		}
	}
	if isOpenAIImageGenerationModel(requestedModel) {
		return strings.TrimSpace(requestedModel)
	}
	if OpenAIImagesEndpointFromContext(ctx) {
		return strings.TrimSpace(requestedModel)
	}
	if OpenAIImageGenerationIntentFromContext(ctx) {
		return "gpt-image-2"
	}
	return ""
}

// A Responses-capable text account does not necessarily serve the hosted image
// model. Apply the same account model allowlist and image cooldown as /images.
func openAIImageRequestRejection(ctx context.Context, account *Account, requestedModel string) string {
	if account == nil || !account.IsOpenAI() {
		return ""
	}
	if extraImagesAccountRejected(ctx, account) {
		return "image_model_not_authorized"
	}
	model := openAIImageRequestModel(ctx, requestedModel)
	if model == "" {
		return ""
	}
	if !account.IsModelSupported(model) || account.modelCapabilityRejectedForRequest(ctx, model) {
		return "image_model_not_supported"
	}
	if account.isModelRateLimitedWithContext(WithOpenAIImageGenerationIntent(ctx), model) {
		return "image_model_rate_limited"
	}
	return ""
}

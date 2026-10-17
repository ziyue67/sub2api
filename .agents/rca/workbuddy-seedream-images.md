# WorkBuddy image API compatibility

## Reproduction

On 2026-10-17, authenticated `POST /v1/images/generations` at the user's image gateway returned HTTP 400 for both `seedream-5.0-lite` and `seedream-5.0-pro`: `images endpoint requires an image model`. The same model IDs had generated real images directly through the user's WorkBuddy video panel. `GET /v1/models` already advertised them.

## Root cause and scope

`backend/internal/service/openai_images.go` validates provider image IDs against GPT, Grok and Gemini compatibility predicates. Seedream is absent. The API-key-only capability fence must include Seedream, while native OpenAI Responses/OAuth model predicates must remain unchanged.

Seedream uses `resolution` and `aspect_ratio`; GPT uses `size` and `quality`. Request billing metadata currently derives only from `size`. The gateway must preserve native Seedream options and record the correct requested resolution. Common OpenAI pixel sizes and quality defaults should be adapted to the WorkBuddy Seedream contract without changing reference inputs or credentials.

Seedream provider resolution tiers are not maximum-edge pixel tiers: a Pro 2K 16:9 output is 2816x1584. Apply the normalized provider resolution for Seedream billing only, retaining original output dimensions in usage audit fields. The existing three-tier tariff maps 1.5K to 2K. Generic GPT/Gemini output-size accounting remains unchanged.

## Regression plan

- Accept exactly the two supported Seedream IDs, reject unknown models and video models.
- Restrict these IDs to API-key image routes, including mapped models; native OAuth routes continue rejecting them.
- Check native resolution/aspect-ratio forwarding and 4K request metadata.
- Check JSON and SSE image responses, image count and original provider usage retention.
- Check optional standard size/quality adaptation and preservation of reference images.
- Check Seedream provider tier billing, including 2K 2816x1584 and 1.5K, without discarding output dimensions.
- Retain existing GPT/Gemini/Grok tests and run the backend unit suite.

## Commands

Use Go 1.27.2 with a host-compatible GOOS for execution:

```text
go test -tags unit ./internal/service -run '^TestCompatibleImages.*WorkBuddy' -count=1
go test -tags unit ./... -count=1
```

## Production boundary

Base: currently deployed `de9c85dba0cee0312ceabb516928da41d4f5ca88`. No migrations or frontend changes. Preserve the upstream WorkBuddy key's intentional quality cap, all wallet balances, credentials, PostgreSQL/Redis services and unrelated containers. Build and verify a candidate before a separately approved public cutover. Never retry a paid generation with an uncertain outcome.

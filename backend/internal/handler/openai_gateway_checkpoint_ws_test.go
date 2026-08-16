package handler

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/securityaudit"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const openAIWSCheckpointTestJWTSecret = "deepseek-handler-compact-test-jwt-secret"

type openAIWSCheckpointAuditEngine struct {
	mu    sync.Mutex
	scans []string
}

func (*openAIWSCheckpointAuditEngine) EffectiveMode() securityaudit.Mode {
	return securityaudit.ModeBlocking
}

func (*openAIWSCheckpointAuditEngine) Enqueue(context.Context, securityaudit.Request) error {
	return nil
}

func (e *openAIWSCheckpointAuditEngine) Evaluate(_ context.Context, req securityaudit.Request) (*securityaudit.PromptDecision, error) {
	snapshot, err := securityaudit.ExtractBlockingPromptSnapshot(req, true)
	if err != nil {
		return nil, err
	}
	e.mu.Lock()
	e.scans = append(e.scans, snapshot.ScanText)
	e.mu.Unlock()
	return &securityaudit.PromptDecision{Kind: securityaudit.DecisionAllow, AllowNextStage: true}, nil
}

func (e *openAIWSCheckpointAuditEngine) snapshot() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.scans...)
}

func gatewayOwnedDeepSeekCheckpointForWSTest(t *testing.T, summary string) string {
	t.Helper()
	upstreamBody := deepSeekCompactCompletedResponsesSSE(t, "resp_ws_checkpoint_source", summary, 11, 2)
	h, _, apiKey, _ := newDeepSeekPartialUsageHandler(t, upstreamBody)
	requestBody, err := json.Marshal(map[string]any{
		"model":  "deepseek-v4-flash",
		"stream": true,
		"input": []any{
			map[string]any{"type": "message", "role": "user", "content": "checkpoint source history"},
			map[string]any{"type": "compaction_trigger"},
		},
	})
	require.NoError(t, err)
	c, recorder := deepSeekPartialUsageContext("/v1/responses", string(requestBody), apiKey)
	c.Request.Header.Set("X-Codex-Beta-Features", "remote_compaction_v2")
	h.Responses(c)

	for _, line := range strings.Split(recorder.Body.String(), "\n") {
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if envelope := strings.TrimSpace(gjson.Get(payload, "item.encrypted_content").String()); envelope != "" {
			return envelope
		}
	}
	t.Fatal("DeepSeek compaction response did not contain a checkpoint envelope")
	return ""
}

func checkpointResponseCreatePayload(t *testing.T, model, envelope, followUp string) string {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"type":  "response.create",
		"model": model,
		"input": []any{
			map[string]any{"type": "compaction", "encrypted_content": envelope},
			map[string]any{"type": "message", "role": "user", "content": followUp},
		},
	})
	require.NoError(t, err)
	return string(payload)
}

func requireRestoredCheckpointPayload(t *testing.T, payload []byte, summary string) {
	t.Helper()
	items := gjson.GetBytes(payload, "input").Array()
	require.Len(t, items, 2)
	require.Equal(t, "message", items[0].Get("type").String())
	require.Equal(t, "user", items[0].Get("role").String())
	require.Contains(t, items[0].Get("content.0.text").String(), summary)
	require.NotContains(t, string(payload), "sub2api.deepseek.compact.v1.")
}

func TestOpenAIResponsesWebSocketRestoresDeepSeekCheckpointBeforeFirstTurnAuditDirect(t *testing.T) {
	const summary = "direct OpenAI websocket checkpoint summary"
	envelope := gatewayOwnedDeepSeekCheckpointForWSTest(t, summary)
	auditEngine := &openAIWSCheckpointAuditEngine{}

	got := runOpenAIResponsesWebSocketUsageLogCase(t, openAIResponsesWSUsageLogCase{
		firstPayload:             checkpointResponseCreatePayload(t, "gpt-5.4", envelope, "continue direct"),
		jwtSecret:                openAIWSCheckpointTestJWTSecret,
		userID:                   7844,
		groupPlatform:            service.PlatformOpenAI,
		securityAuditCoordinator: securityaudit.NewCoordinator(nil, auditEngine),
	})

	requireRestoredCheckpointPayload(t, got.upstreamFirstPayload, summary)
	scans := auditEngine.snapshot()
	require.Len(t, scans, 1)
	require.Contains(t, scans[0], summary)
	require.NotContains(t, scans[0], "sub2api.deepseek.compact.v1.")
}

func TestOpenAIResponsesWebSocketRestoresDeepSeekCheckpointBeforeSubsequentTurnAuditComposite(t *testing.T) {
	const (
		summary     = "composite OpenAI websocket checkpoint summary"
		publicModel = "company-coding-model"
	)
	envelope := gatewayOwnedDeepSeekCheckpointForWSTest(t, summary)
	auditEngine := &openAIWSCheckpointAuditEngine{}

	got := runOpenAIResponsesWebSocketUsageLogCase(t, openAIResponsesWSUsageLogCase{
		firstPayload:  `{"type":"response.create","model":"company-coding-model","input":"first turn"}`,
		secondPayload: checkpointResponseCreatePayload(t, publicModel, envelope, "continue composite"),
		accountModelMapping: map[string]any{
			publicModel: "gpt-5.4",
		},
		jwtSecret:     openAIWSCheckpointTestJWTSecret,
		userID:        7844,
		groupPlatform: service.PlatformComposite,
		compositeRoutes: []service.CompositeModelRoute{{
			ID:             9101,
			GroupID:        4201,
			PublicModel:    publicModel,
			MatchType:      service.CompositeRouteMatchExact,
			TargetPlatform: service.PlatformOpenAI,
			UpstreamModel:  "gpt-5.4",
			Endpoint:       service.CompositeRouteEndpointResponses,
			Enabled:        true,
		}},
		securityAuditCoordinator: securityaudit.NewCoordinator(nil, auditEngine),
	})

	require.Len(t, got.upstreamPayloads, 2)
	requireRestoredCheckpointPayload(t, got.upstreamPayloads[1], summary)
	scans := auditEngine.snapshot()
	require.Len(t, scans, 2)
	require.NotContains(t, scans[0], summary)
	require.Contains(t, scans[1], summary)
	require.NotContains(t, scans[1], "sub2api.deepseek.compact.v1.")
}

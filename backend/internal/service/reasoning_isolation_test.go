//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestReasoningCacheTenantIsolationThroughForward(t *testing.T) {
	cache := &reasoningRecordingCache{}
	seed := []byte(`{"model":"deepseek-reasoner","stream":false,"input":[{"type":"reasoning","id":"rs_shared","summary":[{"type":"summary_text","text":"TENANT_A_ONLY"}]},{"type":"message","role":"assistant","content":[{"type":"output_text","text":"prior"}]},{"role":"user","content":"next"}]}`)
	encrypted := []byte(`{"model":"deepseek-reasoner","stream":false,"input":[{"type":"reasoning","id":"rs_shared","summary":[],"encrypted_content":"opaque"},{"type":"message","role":"assistant","content":[{"type":"output_text","text":"prior"}]},{"role":"user","content":"next"}]}`)
	forward := func(userID, keyID int64, body []byte) string {
		c := newDeepSeekChatFallbackContext(t, body)
		c.Set("api_key", &APIKey{ID: keyID, UserID: userID})
		upstream := newOKChatCompletionsUpstream("reasoning-isolation", deepSeekChatFallbackOKBody)
		svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream, cache: cache}
		result, err := svc.Forward(context.Background(), c, forceChatResponsesFallbackAccount(), body)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.EqualValues(t, 3, result.Usage.InputTokens)
		require.EqualValues(t, 2, result.Usage.OutputTokens)
		return gjson.GetBytes(upstream.lastBody, "messages.0.reasoning_content").String()
	}
	require.Equal(t, "TENANT_A_ONLY", forward(101, 1, seed))
	cache.getResp = cache.snapshotSets()
	require.NotEqual(t, "TENANT_A_ONLY", forward(202, 2, encrypted), "other tenant must never receive A's content")
	require.Equal(t, "TENANT_A_ONLY", forward(101, 3, encrypted), "rotating own key must preserve history")
	// A legacy global entry is untrusted even when its ID is known.
	cache.getResp = map[string]string{"rs_shared": "LEGACY_SECRET"}
	require.NotEqual(t, "LEGACY_SECRET", forward(101, 1, encrypted))
	require.NotEqual(t, "LEGACY_SECRET", forward(0, 0, encrypted))
}

func TestReasoningUserIDRequiresAuthenticatedAPIKey(t *testing.T) {
	require.Zero(t, reasoningUserID(nil))
	c := &gin.Context{}
	c.Set("user_id", int64(100))
	require.Zero(t, reasoningUserID(c))
	c.Set("api_key", &APIKey{ID: 9, UserID: 100})
	require.EqualValues(t, 100, reasoningUserID(c))
}

//go:build unit

package service

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAnthropicPassthroughDownstreamCacheUsageRewriteKeepsRawSeparate(t *testing.T) {
	data := `{"type":"message_start","message":{"usage":{"cache_creation":{"ephemeral_5m_input_tokens":100,"ephemeral_1h_input_tokens":0}}}}`
	var raw ClaudeUsage
	parseSSEUsagePassthrough(data, &raw)
	rewritten, changed := rewriteAnthropicPassthroughSSEData(data, cacheTTLTarget1h)
	require.True(t, changed)
	require.Equal(t, 100, raw.CacheCreation5mTokens)
	require.Contains(t, rewritten, `"ephemeral_5m_input_tokens":0`)
	require.Contains(t, rewritten, `"ephemeral_1h_input_tokens":100`)
}

func TestAnthropicPassthroughResponseBodyCacheUsageRewrite(t *testing.T) {
	body := []byte(`{"usage":{"cache_creation":{"ephemeral_5m_input_tokens":7,"ephemeral_1h_input_tokens":3}}}`)
	rewritten, changed := rewriteAnthropicPassthroughResponseBody(body, cacheTTLTarget1h)
	require.True(t, changed)
	require.True(t, strings.Contains(string(rewritten), `"ephemeral_5m_input_tokens":0`))
	require.True(t, strings.Contains(string(rewritten), `"ephemeral_1h_input_tokens":10`))
}

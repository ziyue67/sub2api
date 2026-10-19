package service

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCodexSubagentDetectionUsesProtocolMarkers(t *testing.T) {
	for _, tc := range []struct {
		name, header, value, body string
		blocked                   bool
	}{
		{name: "subagent header", header: "X-OpenAI-Subagent", value: "explorer", blocked: true},
		{name: "parent header", header: "X-Codex-Parent-Thread-Id", value: "parent-fixture", blocked: true},
		{name: "turn subagent", header: "X-Codex-Turn-Metadata", value: `{"subagent_kind":"review"}`, blocked: true},
		{name: "turn parent", header: "X-Codex-Turn-Metadata", value: `{"parent_thread_id":"parent-fixture"}`, blocked: true},
		{name: "desktop subagent", body: `{"client_metadata":{"x-openai-subagent":"worker"}}`, blocked: true},
		{name: "desktop parent", body: `{"client_metadata":{"x-codex-parent-thread-id":"parent-fixture"}}`, blocked: true},
		{name: "desktop turn metadata", body: `{"client_metadata":{"x-codex-turn-metadata":"{\"subagent_kind\":\"worker\"}"}}`, blocked: true},
		{name: "nested frame", body: `{"type":"response.create","client_metadata":{},"response":{"client_metadata":{"x-openai-subagent":"guardian"}}}`, blocked: true},
		{name: "outer frame marker", body: `{"type":"response.create","client_metadata":{"x-openai-subagent":"guardian"},"response":{}}`, blocked: true},
		{name: "safe header cannot hide body marker", header: "X-Codex-Turn-Metadata", value: `{"request_kind":"turn"}`, body: `{"client_metadata":{"x-openai-subagent":"review"}}`, blocked: true},
		{name: "main turn", header: "X-Codex-Turn-Metadata", value: `{"thread_id":"main-fixture","request_kind":"turn","subagent_kind":null,"parent_thread_id":null}`},
		{name: "prewarm", header: "X-Codex-Turn-Metadata", value: `{"request_kind":"prewarm"}`},
		{name: "compaction", body: `{"client_metadata":{"x-codex-turn-metadata":"{\"request_kind\":\"compaction\"}"}}`},
		{name: "tool and prompt text", body: `{"input":"explain spawn_agent and ultra","tools":[{"name":"spawn_agent"}],"thread_id":"main-fixture"}`},
		{name: "malformed metadata", header: "X-Codex-Turn-Metadata", value: "invalid-json"},
		{name: "empty markers", body: `{"client_metadata":{"x-openai-subagent":" ","x-codex-turn-metadata":"{}"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
			if tc.header != "" {
				c.Request.Header.Set(tc.header, tc.value)
			}
			require.Equal(t, tc.blocked, IsCodexSubagentRequest(c, []byte(tc.body)))
		})
	}
}

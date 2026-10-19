//go:build unit

package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/stretchr/testify/require"
)

func TestAccountAdminTestsAndQualityJudgeBypassEmployeeGroupClientGate(t *testing.T) {
	for _, mode := range []string{"connection", "iq", "judge"} {
		t.Run(mode, func(t *testing.T) {
			group := &Group{ID: 8, Platform: PlatformOpenAI, Status: StatusActive, CodexCLIOnly: true, BlockSubagents: true,
				MaxReasoningEffort: "low", MaxReasoningEffortOverLimit: ReasoningEffortOverLimitDeny,
				ReasoningEffortMappings: []ReasoningEffortMapping{{From: "ultra", To: "deny"}},
			}
			account := &Account{
				ID: 34, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Concurrency: 1,
				GroupIDs: []int64{8}, Groups: []*Group{group},
				Credentials: map[string]any{"api_key": "fixture-only", "base_url": "https://vmapi.example"},
				Extra:       map[string]any{openai_compat.ExtraKeyResponsesSupported: true},
			}
			output := `{"verdict":"correct","reason":"equivalent"}`
			delta, err := json.Marshal(map[string]any{"type": "response.output_text.delta", "delta": output})
			require.NoError(t, err)
			response := newJSONResponse(http.StatusOK, "")
			response.Header.Set("Content-Type", "text/event-stream")
			response.Body = io.NopCloser(strings.NewReader("data: " + string(delta) + "\n\ndata: {\"type\":\"response.completed\"}\n\n"))
			upstream := &queuedHTTPUpstream{responses: []*http.Response{response}}
			svc := &AccountTestService{
				httpUpstream: upstream, cfg: &config.Config{},
				accountRepo: &openAIAccountTestRepo{mockAccountRepoForGemini: mockAccountRepoForGemini{accountsByID: map[int64]*Account{34: account}}},
			}
			c, recorder := newTestContext()
			c.Request.Header.Set("User-Agent", "Mozilla/5.0 admin browser")
			switch mode {
			case "connection":
				err = svc.TestAccountConnection(c, 34, "gpt-test", "connection probe", AccountTestModeDefault)
			case "iq":
				err = svc.TestPelicanAccountConnection(c, 34, "gpt-test", "IQ question", "medium")
			case "judge":
				var actual string
				actual, err = svc.runQualityJudgeRequest(context.Background(), 34, "gpt-test", "grade these answers")
				require.Equal(t, output, actual)
			}
			require.NoError(t, err, recorder.Body.String())
			require.Len(t, upstream.requests, 1, "the internal request must reach vmapi despite the employee gate")
			require.Equal(t, "vmapi.example", upstream.requests[0].URL.Host)
		})
	}
}

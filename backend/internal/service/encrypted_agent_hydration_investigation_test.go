package service

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const diagnosticEncryptedAgentBody = `{"model":"gpt-6-luna","stream":true,"instructions":"synthetic diagnostic","input":[{"type":"agent_message","id":"amsg_synthetic","author":"/root/audit","recipient":"/root","content":[{"type":"input_text","text":"Message Type: MESSAGE\nPayload:\n"},{"type":"encrypted_content","encrypted_content":"synthetic-task-ciphertext"}]}]}`

// Encrypted agent payloads also need
// affinity protection. It does not claim that ciphertext may be discarded.
func TestEncryptedAgentHydrationAffinityRegression(t *testing.T) {
	ctx := WithOpenAIEncryptedReasoningAffinity(context.Background(), []byte(diagnosticEncryptedAgentBody))
	require.Equal(t, true, ctx.Value(openAIEncryptedTaskAffinityKey{}), "encrypted agent task needs soft affinity")
	require.Equal(t, false, ctx.Value(openAIEncryptedReasoningAffinityKey{}), "opaque tasks must not inherit reasoning's hard latency ownership policy")
}

func TestEncryptedAgentHydrationDetection(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       bool
	}{
		{"nested task", diagnosticEncryptedAgentBody, true},
		{"top level task", `{"input":[{"type":"agent_message","encrypted_content":"task"}]}`, true},
		{"object input", `{"input":{"type":"agent_message","encrypted_content":"task"}}`, true},
		{"compaction", `{"input":[{"type":"compaction","encrypted_content":"history"}]}`, true},
		{"compaction summary", `{"input":[{"type":"compaction_summary","encrypted_content":"history"}]}`, true},
		{"reasoning then task", `{"input":[{"type":"reasoning","encrypted_content":"reasoning"},{"type":"agent_message","encrypted_content":"task"}]}`, true},
		{"task then reasoning", `{"input":[{"type":"agent_message","encrypted_content":"task"},{"type":"reasoning","encrypted_content":"reasoning"}]}`, true},
		{"empty task", `{"input":[{"type":"agent_message","encrypted_content":""}]}`, false},
		{"null task", `{"input":[{"type":"agent_message","encrypted_content":null}]}`, false},
		{"non string task", `{"input":[{"type":"agent_message","encrypted_content":123}]}`, false},
		{"empty nested", `{"input":[{"type":"agent_message","content":[{"type":"encrypted_content","encrypted_content":""}]}]}`, false},
		{"plaintext task", `{"input":[{"type":"agent_message","content":[{"type":"input_text","text":"encrypted_content"}]}]}`, false},
		{"unrecognized block", `{"input":[{"type":"agent_message","content":[{"type":"input_text","encrypted_content":"not a cipher block"}]}]}`, false},
		{"tool data", `{"input":[{"type":"function_call_output","output":{"type":"agent_message","encrypted_content":"not protocol"}}]}`, false},
		{"reasoning only", `{"input":[{"type":"reasoning","encrypted_content":"reasoning"}]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := WithOpenAIEncryptedReasoningAffinity(context.Background(), []byte(tc.body))
			require.Equal(t, tc.want, ctx.Value(openAIEncryptedTaskAffinityKey{}))
			if tc.name == "reasoning then task" || tc.name == "task then reasoning" {
				require.Equal(t, true, ctx.Value(openAIEncryptedReasoningAffinityKey{}), "mixed histories retain the existing reasoning policy")
			}
		})
	}
}

// Exercise selection and forwarding together: quality alone should not move an
// encrypted task, but full/unavailable accounts must still yield to a ready one.
func TestEncryptedAgentHydrationSchedulingAndForward(t *testing.T) {
	for _, accountType := range []string{AccountTypeAPIKey, AccountTypeOAuth} {
		for _, reason := range []string{"healthy", "slow", "errors", "full", "disabled", "rate_limited", "excluded"} {
			t.Run(accountType+"/"+reason, func(t *testing.T) {
				const session = "agent-scheduling"
				groupID := int64(10103)
				a, b := newStreamLineageHTTPAccount(), newStreamLineageHTTPAccount()
				a.ID, b.ID = 21301, 21302
				a.Priority, b.Priority = 0, 1
				for _, account := range []*Account{a, b} {
					account.Type = accountType
					account.GroupIDs = []int64{groupID}
					account.Credentials["access_token"] = "synthetic-token"
				}
				want := a.ID
				var excluded map[int64]struct{}
				slots := map[int64]bool{a.ID: true, b.ID: true}
				switch reason {
				case "full":
					slots[a.ID], want = false, b.ID
				case "disabled":
					a.Schedulable, want = false, b.ID
				case "rate_limited":
					until := time.Now().Add(time.Hour)
					a.RateLimitResetAt, want = &until, b.ID
				case "excluded":
					excluded, want = map[int64]struct{}{a.ID: {}}, b.ID
				}
				upstream := &httpUpstreamSequenceRecorder{responses: []*http.Response{newStreamLineageSSEResponse(streamLineageDoneEvent)}}
				svc := newStreamLineageHTTPService(upstream)
				svc.cfg.Gateway.OpenAIScheduler.StickyEscapeEnabled = true
				svc.cfg.Gateway.OpenAIScheduler.StickyEscapeTTFTMs = 15000
				svc.cfg.Gateway.OpenAIScheduler.StickyEscapeErrorRate = 0.5
				svc.accountRepo = schedulerTestOpenAIAccountRepo{accounts: []Account{*a, *b}}
				svc.cache = &schedulerTestGatewayCache{sessionBindings: map[string]int64{"openai:" + session: a.ID}}
				svc.rateLimitService = newOpenAIAdvancedSchedulerRateLimitService("true")
				svc.concurrencyService = NewConcurrencyService(schedulerTestConcurrencyCache{acquireResults: slots})
				svc.openaiAccountStats = newOpenAIAccountRuntimeStats()
				for i := 0; i < 5; i++ {
					latency := 20000
					switch reason {
					case "slow":
						svc.openaiAccountStats.report(a.ID, true, &latency)
					case "errors":
						svc.openaiAccountStats.report(a.ID, false, nil)
					}
				}
				body := []byte(diagnosticEncryptedAgentBody)
				ctx := WithOpenAIEncryptedReasoningAffinity(context.Background(), body)
				selection, decision, err := svc.SelectAccountWithScheduler(ctx, &groupID, "", session, "gpt-6-luna", excluded, OpenAIUpstreamTransportAny, false)
				require.NoError(t, err)
				require.NotNil(t, selection)
				require.NotNil(t, selection.Account)
				require.Equal(t, want, selection.Account.ID)
				if want == a.ID {
					require.True(t, decision.StickySessionHit, "quality-only escape must not discard encrypted task affinity")
				}
				require.True(t, selection.Acquired)
				require.Nil(t, selection.WaitPlan, "a ready account must not become an indefinite wait")
				if selection.ReleaseFunc != nil {
					defer selection.ReleaseFunc()
				}
				c, rec := newStreamLineageTestContext(session)
				result, err := svc.Forward(ctx, c, selection.Account, body)
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Equal(t, 1, upstream.callCount)
				require.Equal(t, 1, result.Usage.InputTokens)
				require.Equal(t, 1, result.Usage.OutputTokens)
				require.Contains(t, rec.Body.String(), "response.completed")
				require.Equal(t, diagnosticEncryptedAgentBody, string(body))
				// OAuth already removes input item IDs during store=false
				// normalization. Verify the opaque task, routing and readable
				// content independently of that existing metadata policy.
				for _, field := range []string{"type", "author", "recipient", "content"} {
					path := "input.0." + field
					require.JSONEq(t, gjson.GetBytes(body, path).Raw, gjson.GetBytes(upstream.bodies[0], path).Raw, path)
				}
			})
		}
	}
}

// Exercise the actual forwarding path with synthetic upstream SSE and the
// screenshot's item-ID diagnostic, without sending a paid external request.
func TestEncryptedAgentHydrationForwardObservation(t *testing.T) {
	message := "The encrypted content for item amsg_synthetic could not be verified. Reason: encrypted content hydration failed: Encrypted content could not be decrypted or parsed."
	failure, err := json.Marshal(map[string]any{"type": "response.failed", "response": map[string]any{"id": "synthetic-rejected", "status": "failed", "error": map[string]string{"code": "invalid_encrypted_content", "message": message}}})
	require.NoError(t, err)
	for _, accountType := range []string{AccountTypeAPIKey, AccountTypeOAuth} {
		t.Run(accountType, func(t *testing.T) {
			upstream := &httpUpstreamSequenceRecorder{responses: []*http.Response{newStreamLineageSSEResponse(encryptedSSERetryEvent(string(failure)))}}
			svc := newStreamLineageHTTPService(upstream)
			account := newStreamLineageHTTPAccount()
			account.Type = accountType
			account.Credentials["access_token"] = "synthetic-token"
			c, rec := newStreamLineageTestContext("synthetic-agent-hydration")
			_, err := svc.Forward(context.Background(), c, account, []byte(diagnosticEncryptedAgentBody))
			require.Error(t, err)
			var failover *UpstreamFailoverError
			require.NotErrorAs(t, err, &failover, "rejected opaque history must not trigger an account retry loop")
			require.Equal(t, 1, upstream.callCount)
			require.Contains(t, rec.Body.String(), "invalid_encrypted_content")
			require.Equal(t, "synthetic-task-ciphertext", gjson.GetBytes(upstream.bodies[0], "input.0.content.1.encrypted_content").String())
			t.Log("Observed: screenshot-shaped SSE failure reaches the client; no automatic retry or task deletion")
		})
	}
}

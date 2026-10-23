package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const encryptedSSERetryFailure = `{"type":"response.failed","response":{"id":"rejected-attempt","status":"failed","error":{"code":"invalid_encrypted_content","message":"Encrypted reasoning rejected"}}}`

func encryptedSSERetryEvent(payload string) string {
	return "data: " + payload + "\n\n"
}

type encryptedSSERetryBody struct {
	io.Reader
	closed bool
}

func (b *encryptedSSERetryBody) Close() error {
	b.closed = true
	return nil
}

func TestForwardEncryptedContentSSERetry(t *testing.T) {
	const singleInput = `[{"type":"reasoning","encrypted_content":"synthetic-foreign-cipher","summary":[{"type":"summary_text","text":"keep summary"}]},{"role":"user","content":"continue task"},{"type":"function_call","call_id":"call_history","name":"inspect","arguments":"{}"},{"type":"function_call_output","call_id":"call_history","output":"keep result"}]`
	const httpFailure = `{"error":{"code":"invalid_encrypted_content","message":"Encrypted reasoning rejected"}}`
	const success = `{"type":"response.completed","response":{"id":"accepted-attempt","status":"completed","usage":{"input_tokens":1,"output_tokens":1}}}`
	failure := encryptedSSERetryEvent(encryptedSSERetryFailure)
	preamble := encryptedSSERetryEvent(`{"type":"response.created","response":{"id":"rejected-attempt","padding":"` + strings.Repeat("p", 20000) + `"}}`)
	cases := []struct {
		name       string
		input      string
		first      string
		firstHTTP  bool
		second     string
		secondHTTP bool
		calls      int
		success    bool
	}{
		{name: "staged metadata then failure", first: preamble + failure, calls: 2, success: true},
		{name: "bare error", first: encryptedSSERetryEvent(`{"type":"error","error":{"code":"invalid_encrypted_content","message":"rejected"}}`), calls: 2, success: true},
		{name: "thinking signature alias", first: encryptedSSERetryEvent(`{"type":"response.failed","response":{"error":{"code":"thinking_signature_invalid","message":"The encrypted content synt...pher could not be verified. Reason: Encrypted content could not be decrypted or parsed."}}}`), calls: 2, success: true},
		{name: "text already delivered", first: streamLineageDeltaEvent + failure, calls: 1},
		{name: "tool already delivered", first: encryptedSSERetryEvent(`{"type":"response.output_item.added","item":{"type":"function_call","name":"execute","call_id":"call_live","arguments":"{}"}}`) + failure, calls: 1},
		{name: "reasoning already delivered", first: encryptedSSERetryEvent(`{"type":"response.reasoning_summary_text.delta","delta":"reasoning already visible"}`) + failure, calls: 1},
		{name: "multiple ciphertexts", input: `[{"type":"reasoning","encrypted_content":"one"},{"type":"reasoning","encrypted_content":"two"},{"role":"user","content":"continue task"}]`, first: failure, calls: 1},
		{name: "only compaction history", input: `[{"type":"compaction","encrypted_content":"only-history"},{"role":"user","content":"continue task"}]`, first: failure, calls: 1},
		{name: "encrypted agent task", input: `[{"type":"agent_message","encrypted_content":"only-task","content":[{"type":"input_text","text":"readable header"}]}]`, first: failure, calls: 1},
		{name: "no ciphertext", input: `[{"role":"user","content":"continue task"}]`, first: failure, calls: 1},
		{name: "second SSE failure", first: failure, second: failure, calls: 2},
		{name: "HTTP then SSE share budget", first: httpFailure, firstHTTP: true, second: failure, calls: 2},
		{name: "SSE then HTTP share budget", first: failure, second: httpFailure, secondHTTP: true, calls: 2},
	}
	for _, accountType := range []string{AccountTypeAPIKey, AccountTypeOAuth} {
		for _, timeout := range []int{0, 5} {
			for _, tc := range cases {
				t.Run(fmt.Sprintf("%s/timeout=%d/%s", accountType, timeout, tc.name), func(t *testing.T) {
					input := tc.input
					if input == "" {
						input = singleInput
					}
					body := []byte(`{"model":"gpt-6-luna","stream":true,"instructions":"test","input":` + input + `}`)
					canonical := append([]byte(nil), body...)
					response := func(data, state string, isHTTP bool) *http.Response {
						resp := newStreamLineageSSEResponse(data)
						resp.Header.Set("X-Codex-Turn-State", state)
						resp.Header.Set("X-Request-Id", state)
						if isHTTP {
							resp.StatusCode = http.StatusBadRequest
							resp.Header.Set("Content-Type", "application/json")
						}
						return resp
					}
					second := tc.second
					if second == "" {
						second = encryptedSSERetryEvent(success)
					}
					firstResp := response(tc.first, "rejected-state", tc.firstHTTP)
					tracked := &encryptedSSERetryBody{Reader: firstResp.Body}
					firstResp.Body = tracked
					upstream := &httpUpstreamSequenceRecorder{responses: []*http.Response{
						firstResp, response(second, "accepted-state", tc.secondHTTP),
					}}
					svc := newStreamLineageHTTPService(upstream)
					svc.cfg.Gateway.OpenAIFirstOutputTimeoutSeconds = timeout
					account := newStreamLineageHTTPAccount()
					account.Type = accountType
					account.Credentials["access_token"] = "synthetic-access-token"
					c, rec := newStreamLineageTestContext("synthetic-sse-retry")
					result, err := svc.Forward(context.Background(), c, account, body)
					require.Equal(t, canonical, body, "canonical request must remain immutable")
					require.Equal(t, tc.calls, upstream.callCount)
					if tc.success {
						require.NoError(t, err)
						require.NotNil(t, result)
						require.True(t, tracked.closed, "close rejected response before retry")
						require.NotContains(t, rec.Body.String(), "rejected-attempt")
						require.NotContains(t, rec.Body.String(), "invalid_encrypted_content")
						require.Equal(t, "accepted-state", rec.Header().Get("X-Codex-Turn-State"))
						require.Equal(t, "accepted-state", rec.Header().Get("X-Request-Id"))
						require.Contains(t, rec.Body.String(), "accepted-attempt")
					} else {
						require.Error(t, err)
						var failover *UpstreamFailoverError
						require.NotErrorAs(t, err, &failover, "invalid history must not rotate accounts")
						require.Contains(t, rec.Body.String(), "encrypted_content")
					}
					if tc.calls == 2 {
						retry := upstream.bodies[1]
						require.Equal(t, upstream.reqs[0].Header.Get("Authorization"), upstream.reqs[1].Header.Get("Authorization"), "same-account recovery must retain the upstream identity")
						require.Equal(t, gjson.GetBytes(upstream.bodies[0], "model").String(), gjson.GetBytes(retry, "model").String())
						require.False(t, gjson.GetBytes(retry, "input.0.encrypted_content").Exists())
						require.Equal(t, "keep summary", gjson.GetBytes(retry, "input.0.summary.0.text").String())
						require.Equal(t, "continue task", gjson.GetBytes(retry, "input.1.content").String())
						// OAuth normalizes call IDs before the first upstream attempt.
						// Recovery must preserve that normalized call/result pairing.
						historyCallID := gjson.GetBytes(upstream.bodies[0], "input.2.call_id").String()
						require.NotEmpty(t, historyCallID)
						require.Equal(t, historyCallID, gjson.GetBytes(upstream.bodies[0], "input.3.call_id").String())
						require.Equal(t, historyCallID, gjson.GetBytes(retry, "input.2.call_id").String())
						require.Equal(t, historyCallID, gjson.GetBytes(retry, "input.3.call_id").String())
						require.Equal(t, "keep result", gjson.GetBytes(retry, "input.3.output").String())
					}
				})
			}
		}
	}
}

func TestEncryptedSSERetryClassification(t *testing.T) {
	for _, tc := range []struct {
		name, payload, event string
		matched              bool
	}{
		{"nested failed", encryptedSSERetryFailure, "response.failed", true},
		{"bare root", `{"type":"error","code":"invalid_encrypted_content","message":"rejected"}`, "error", true},
		{"bare nested", `{"type":"error","error":{"code":"invalid_encrypted_content"}}`, "error", true},
		{"overload", `{"type":"response.failed","response":{"error":{"code":"server_error","message":"Please retry later"}}}`, "response.failed", false},
		{"conflicting explicit code", `{"type":"error","code":"server_error","message":"The encrypted content could not be verified. Reason: Encrypted content could not be decrypted or parsed."}`, "error", false},
		{"missing explicit code", `{"type":"error","message":"The encrypted content could not be verified. Reason: Encrypted content could not be decrypted or parsed."}`, "error", false},
		{"different thinking signature", `{"type":"error","code":"thinking_signature_invalid","message":"unrelated signature"}`, "error", false},
		{"nonterminal", encryptedSSERetryFailure, "response.output_text.delta", false},
		{"truncated JSON", `{"type":"error","code":"invalid_encrypted_content"`, "error", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.matched, isOpenAIInvalidEncryptedContentSSE([]byte(tc.payload), tc.event))
		})
	}
}

func TestEncryptedSSERetryEligibility(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		allowed     bool
	}{
		{"single reasoning", `[{"type":"reasoning","encrypted_content":"one"}]`, true},
		{"object reasoning", `{"type":"reasoning","encrypted_content":"one"}`, true},
		{"multiple reasoning", `[{"type":"reasoning","encrypted_content":"one"},{"type":"reasoning","encrypted_content":"two"}]`, false},
		{"opaque compaction", `[{"type":"compaction","encrypted_content":"history"}]`, false},
		{"compaction summary", `[{"type":"compaction_summary","encrypted_content":"history"}]`, false},
		{"reasoning plus compaction", `[{"type":"reasoning","encrypted_content":"one"},{"type":"compaction","encrypted_content":"history"}]`, false},
		{"reasoning plus agent task", `[{"type":"reasoning","encrypted_content":"one"},{"type":"agent_message","encrypted_content":"task"}]`, false},
		{"null is not a rejected ciphertext", `[{"type":"reasoning","encrypted_content":null}]`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.allowed, openAICanRecoverEncryptedReasoningSSE([]byte(`{"input":`+tc.input+`}`)))
		})
	}
}

func TestEncryptedSSERetryCanceledOrCommitted(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(fmt.Sprintf("canceled=%t", canceled), func(t *testing.T) {
			c, rec := newStreamLineageTestContext("synthetic-sse-guard")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx = context.WithValue(ctx, openAIEncryptedContentRecoveryContextKey{}, openAIEncryptedContentRecoveryPolicy{body: []byte(streamLineageTestBody)})
			if canceled {
				cancel()
			} else {
				_, err := c.Writer.WriteString(": already committed\n\n")
				require.NoError(t, err)
				c.Writer.Flush()
			}
			svc := newStreamLineageHTTPService(nil)
			_, err := svc.handleStreamingResponseWithReasoning(ctx, newStreamLineageSSEResponse(encryptedSSERetryEvent(encryptedSSERetryFailure)), c, newStreamLineageHTTPAccount(), time.Now(), "gpt-6-luna", "gpt-6-luna", "")
			require.Error(t, err)
			var signal *openAIEncryptedContentRecoverySignal
			require.NotErrorAs(t, err, &signal)
			if !canceled {
				require.Contains(t, rec.Body.String(), "invalid_encrypted_content")
			}
		})
	}
}

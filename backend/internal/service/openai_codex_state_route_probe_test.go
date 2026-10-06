package service

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBorrowedRouteReusesStateProbeVerdict(t *testing.T) {
	for _, tc := range []struct {
		name        string
		first, next stateProbeReply
		verdict     OpenAICodexStateVerdict
		failure     string
		calls       int
	}{
		{"unchanged", stateProbeMint("mint-secret"), stateProbeReply{status: 200, body: stateProbeCompletedStream}, OpenAICodexStateHealthy, "", 2},
		{"echo", stateProbeMint("mint-secret"), stateProbeReply{status: 200, state: "mint-secret", body: stateProbeCompletedStream}, OpenAICodexStateHealthy, "", 2},
		{"new ticket", stateProbeMint("mint-secret"), stateProbeReply{status: 200, state: "new-secret", body: stateProbeCompletedStream}, OpenAICodexStateDegraded, "", 2},
		{"missing ticket", stateProbeReply{status: 200, body: stateProbeCompletedStream}, stateProbeReply{}, OpenAICodexStateInconclusive, OpenAICodexStateFailureNoTicket, 1},
		{"rate limited", stateProbeMint("mint-secret"), stateProbeReply{status: 429, body: `{"error":{"message":"limited"}}`}, OpenAICodexStateInconclusive, OpenAICodexStateFailureRateLimited, 2},
		{"stream failed", stateProbeMint("mint-secret"), stateProbeReply{status: 200, state: "new-secret", body: stateProbeFailedStream}, OpenAICodexStateInconclusive, OpenAICodexStateFailureStreamError, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The source Cookie stays fixed; the target's own mint supplies __cflb.
			tc.first.cookies = []string{"__cflb=target-lb; Path=/; Secure"}
			up := &stateProbeUpstream{replies: []stateProbeReply{tc.first, tc.next}}
			req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, chatgptCodexURL, nil)
			req.Header.Set("Authorization", "Bearer target-secret")
			req.Header.Set("ChatGPT-Account-ID", "target-id")
			req.Header.Set("Cookie", "__oailb=borrowed-secret; custom=not-for-probe")
			req.Header.Set(openAICodexTurnStateHeader, "business-state")
			req.Header.Set(responsesLiteHeaderKey, "1")
			result := ProbeOpenAICodexStateRoute(t.Context(), up, req, "leased-exit", 202, 1, nil)
			require.Equal(t, tc.verdict, result.Verdict)
			require.Equal(t, tc.failure, result.Failure)
			require.Len(t, up.calls, tc.calls)
			for i, c := range up.calls {
				require.Equal(t, "leased-exit", c.proxy)
				require.True(t, c.close)
				require.Equal(t, "Bearer target-secret", c.header.Get("Authorization"))
				require.Equal(t, "target-id", c.header.Get("ChatGPT-Account-ID"))
				require.Contains(t, c.header.Get("Cookie"), "__oailb=borrowed-secret")
				require.NotContains(t, c.header.Get("Cookie"), "custom=")
				require.Empty(t, c.header.Get(responsesLiteHeaderKey))
				require.Contains(t, c.body, "Reply with OK.")
				require.NotContains(t, c.body, "糖果")
				if i == 0 {
					require.Empty(t, c.header.Get(openAICodexTurnStateHeader))
				} else {
					require.Equal(t, "mint-secret", c.header.Get(openAICodexTurnStateHeader))
					require.Contains(t, c.header.Get("Cookie"), "__cflb=target-lb")
				}
			}
			raw, err := json.Marshal(result)
			require.NoError(t, err)
			for _, secret := range []string{"mint-secret", "new-secret", "target-secret", "borrowed-secret", "business-state"} {
				require.NotContains(t, string(raw), secret)
			}
			require.Equal(t, "business-state", req.Header.Get(openAICodexTurnStateHeader))
		})
	}
}

func TestBorrowedRouteRejectsReassignmentAndCancellation(t *testing.T) {
	for _, cookie := range []string{"__oailb=changed; Path=/", "__oailb=borrowed; Max-Age=0; Path=/", "__oailb=borrowed; Expires=Thu, 01 Jan 1970 00:00:00 GMT; Path=/"} {
		for _, step := range []int{0, 1} {
			replies := []stateProbeReply{stateProbeMint("mint"), {status: 200, body: stateProbeCompletedStream}}
			replies[0].cookies = nil
			replies[step].cookies = []string{cookie}
			up := &stateProbeUpstream{replies: replies}
			req, _ := http.NewRequest(http.MethodPost, chatgptCodexURL, nil)
			req.Header.Set("Cookie", "__oailb=borrowed")
			result := ProbeOpenAICodexStateRoute(t.Context(), up, req, "exit", 202, 1, nil)
			require.Equal(t, OpenAICodexStateInconclusive, result.Verdict)
			require.Equal(t, "route_changed", result.Failure)
			require.Len(t, up.calls, step+1)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	up := &stateProbeUpstream{replies: []stateProbeReply{{err: context.Canceled}}}
	req, _ := http.NewRequest(http.MethodPost, chatgptCodexURL, strings.NewReader(""))
	req.Header.Set("Cookie", "__oailb=borrowed")
	result := ProbeOpenAICodexStateRoute(ctx, up, req, "exit", 202, 1, nil)
	require.Equal(t, OpenAICodexStateInconclusive, result.Verdict)
	require.Equal(t, OpenAICodexStateFailureCancelled, result.Failure)
}

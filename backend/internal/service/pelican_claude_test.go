//go:build unit

package service

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const pelicanClaudeTestHTML = `<!DOCTYPE html><html><body><svg viewBox="0 0 400 300"><circle cx="200" cy="150" r="40"/></svg></body></html>`

// claudeThinkingUpstream streams like Claude Opus 5.5: thinking is always on,
// counts toward max_tokens and comes back as a thinking block with no text under
// the default display. The model thinks for thinkingTokens, then writes the answer.
type claudeThinkingUpstream struct {
	thinkingTokens int64
	answerTokens   int64
	refusal        string // stop_details.category of a refusal after thinking
	bodies         [][]byte
	urls           []string
}

func (u *claudeThinkingUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	body, _ := io.ReadAll(req.Body)
	u.bodies = append(u.bodies, body)
	u.urls = append(u.urls, req.URL.String())
	maxTokens := gjson.GetBytes(body, "max_tokens").Int()

	var sse strings.Builder
	event := func(data any) {
		raw, _ := json.Marshal(data)
		sse.WriteString("data: " + string(raw) + "\n\n")
	}
	event(map[string]any{"type": "message_start", "message": map[string]any{"id": "msg_fixture", "type": "message", "role": "assistant", "model": "claude-opus-5-5", "content": []any{}, "usage": map[string]any{"input_tokens": 40, "output_tokens": 1}}})
	event(map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "thinking", "thinking": "", "signature": ""}})
	event(map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "thinking_delta", "thinking": ""}})
	event(map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "signature_delta", "signature": "fixture-signature"}})
	event(map[string]any{"type": "content_block_stop", "index": 0})

	delta := map[string]any{"stop_reason": "end_turn", "stop_sequence": nil}
	used := u.thinkingTokens + u.answerTokens
	switch {
	case u.refusal != "":
		delta["stop_reason"] = "refusal"
		delta["stop_details"] = map[string]any{"type": "refusal", "category": u.refusal}
		used = u.thinkingTokens
	case maxTokens <= u.thinkingTokens:
		delta["stop_reason"] = "max_tokens"
		used = maxTokens
	default:
		text := pelicanClaudeTestHTML
		if maxTokens < used {
			// The answer stops where the shared budget runs out.
			text = text[:int64(len(text))*(maxTokens-u.thinkingTokens)/u.answerTokens]
			delta["stop_reason"] = "max_tokens"
			used = maxTokens
		}
		event(map[string]any{"type": "content_block_start", "index": 1, "content_block": map[string]any{"type": "text", "text": ""}})
		event(map[string]any{"type": "content_block_delta", "index": 1, "delta": map[string]any{"type": "text_delta", "text": text}})
		event(map[string]any{"type": "content_block_stop", "index": 1})
	}
	event(map[string]any{"type": "message_delta", "delta": delta, "usage": map[string]any{"output_tokens": used}})
	event(map[string]any{"type": "message_stop"})
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(sse.String())),
	}, nil
}

func (u *claudeThinkingUpstream) DoWithTLS(req *http.Request, proxyURL string, accountID int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxyURL, accountID, concurrency)
}

func pelicanClaudeTestService(account *Account, upstream HTTPUpstream) *AccountTestService {
	return &AccountTestService{
		accountRepo:  &openAIAccountTestRepo{mockAccountRepoForGemini: mockAccountRepoForGemini{accountsByID: map[int64]*Account{account.ID: account}}},
		httpUpstream: upstream,
		cfg:          &config.Config{},
	}
}

func pelicanClaudeOAuthAccount() *Account {
	return &Account{ID: 511, Name: "claude-pelican", Platform: PlatformAnthropic, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Concurrency: 1,
		Credentials: map[string]any{"access_token": "fixture-oauth-token"}}
}

// Regression: the Pelican test reused the 1024-token connection probe. Thinking
// counts toward max_tokens and cannot be turned off on Claude Opus 5.5, so the
// budget ran out before any text and the page reported an empty answer.
func TestPelicanClaudeLeavesRoomForThinkingBeforeTheAnswer(t *testing.T) {
	account := pelicanClaudeOAuthAccount()
	upstream := &claudeThinkingUpstream{thinkingTokens: 3000, answerTokens: 2500}
	svc := pelicanClaudeTestService(account, upstream)
	c, rec := newTestContext()

	err := svc.TestPelicanAccountConnection(c, account.ID, "claude-opus-5-5", "draw the pelican", "medium")

	require.NoError(t, err)
	require.Len(t, upstream.bodies, 1)
	output, message := parsePelicanOutput(rec.Body.String())
	require.Empty(t, message)
	require.Equal(t, pelicanClaudeTestHTML, output)
}

func TestPelicanClaudePayloadFollowsModelReasoningSupport(t *testing.T) {
	for _, tc := range []struct {
		model, effort, wantEffort string
		adaptive                  bool
	}{
		{model: "claude-opus-5-5", effort: "high", wantEffort: "high", adaptive: true},
		{model: "claude-sonnet-4-6", effort: "low", wantEffort: "low", adaptive: true},
		// Opus 4.5 accepts effort but enables thinking with a budget, not adaptive.
		{model: "claude-opus-4-5-20251101", effort: "medium", wantEffort: "medium"},
		// Models without effort keep their defaults rather than receiving a 400.
		{model: "claude-haiku-4-5-20251001", effort: "high"},
		{model: "claude-opus-5-5", effort: "minimal"},
		{model: "claude-opus-5-5"},
	} {
		t.Run(tc.model+"/"+tc.effort, func(t *testing.T) {
			payload, err := createPelicanClaudePayload(tc.model, "draw the pelican", tc.effort)
			require.NoError(t, err)
			raw, err := json.Marshal(payload)
			require.NoError(t, err)

			require.Equal(t, int64(pelicanClaudeMaxTokens), gjson.GetBytes(raw, "max_tokens").Int())
			require.Equal(t, "draw the pelican", gjson.GetBytes(raw, "messages.0.content.0.text").String())
			require.Equal(t, tc.wantEffort, gjson.GetBytes(raw, "output_config.effort").String())
			if tc.adaptive {
				require.Equal(t, "adaptive", gjson.GetBytes(raw, "thinking.type").String())
			} else {
				require.False(t, gjson.GetBytes(raw, "thinking").Exists())
			}
		})
	}
}

func TestPelicanClaudeReportsWhyTheAnswerIsMissing(t *testing.T) {
	for _, tc := range []struct {
		name       string
		upstream   *claudeThinkingUpstream
		wantOutput bool
		want       string
	}{
		{name: "thinking used the whole budget", upstream: &claudeThinkingUpstream{thinkingTokens: 40000, answerTokens: 2500}, want: pelicanErrMaxTokens},
		{name: "answer cut off", upstream: &claudeThinkingUpstream{thinkingTokens: 31000, answerTokens: 2500}, wantOutput: true, want: pelicanErrMaxTokens},
		{name: "refused", upstream: &claudeThinkingUpstream{thinkingTokens: 100, refusal: "cyber"}, want: pelicanErrRefused + " (category: cyber)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := pelicanClaudeOAuthAccount()
			svc := pelicanClaudeTestService(account, tc.upstream)
			c, rec := newTestContext()

			require.Error(t, svc.TestPelicanAccountConnection(c, account.ID, "claude-opus-5-5", "draw the pelican", "medium"))

			output, message := parsePelicanOutput(rec.Body.String())
			require.Equal(t, tc.want, message)
			require.Equal(t, tc.wantOutput, output != "")
			// The answer itself is the result; another account would run the same model.
			result := &ScheduledTestResult{Status: "failed", ResponseText: output, ErrorMessage: message}
			require.False(t, pelicanFailedBeforeOutput(result))
		})
	}
}

func TestClaudeConnectionProbeKeepsItsShortBudgetAndSuccessRule(t *testing.T) {
	account := pelicanClaudeOAuthAccount()
	upstream := &claudeThinkingUpstream{thinkingTokens: 3000, answerTokens: 2500}
	svc := pelicanClaudeTestService(account, upstream)
	c, rec := newTestContext()

	require.NoError(t, svc.TestAccountConnection(c, account.ID, "claude-opus-5-5", "", AccountTestModeDefault))

	require.Equal(t, int64(1024), gjson.GetBytes(upstream.bodies[0], "max_tokens").Int())
	require.Equal(t, "hi", gjson.GetBytes(upstream.bodies[0], "messages.0.content.0.text").String())
	require.Contains(t, rec.Body.String(), `"type":"test_complete","success":true`)
}

func TestPelicanGroupStillRetriesAccountFailures(t *testing.T) {
	require.True(t, pelicanFailedBeforeOutput(&ScheduledTestResult{Status: "failed", ErrorMessage: "API returned 401: invalid token"}))
	require.False(t, pelicanFailedBeforeOutput(&ScheduledTestResult{Status: "failed", ErrorMessage: pelicanErrEmptyOutput}))
}

func TestPelicanClaudeVertexServiceAccountSendsTheQuestion(t *testing.T) {
	account := &Account{ID: 512, Name: "claude-vertex", Platform: PlatformAnthropic, Type: AccountTypeServiceAccount, Status: StatusActive, Concurrency: 1,
		Credentials: map[string]any{
			"service_account_json": `{"type":"service_account","project_id":"vertex-proj","private_key_id":"kid","private_key":"-----BEGIN PRIVATE KEY-----\nabc\n-----END PRIVATE KEY-----\n","client_email":"svc@vertex-proj.iam.gserviceaccount.com"}`,
			"location":             "global",
		}}
	key, err := parseVertexServiceAccountKey(account)
	require.NoError(t, err)
	cache := newClaudeTokenCacheStub()
	cache.tokens[vertexServiceAccountCacheKey(account, key)] = "fixture-vertex-token"
	upstream := &claudeThinkingUpstream{thinkingTokens: 3000, answerTokens: 2500}
	svc := pelicanClaudeTestService(account, upstream)
	svc.claudeTokenProvider = NewClaudeTokenProvider(nil, cache, nil)
	c, rec := newTestContext()

	require.NoError(t, svc.TestPelicanAccountConnection(c, account.ID, "claude-opus-5-5", "draw the pelican", "high"))

	require.Len(t, upstream.bodies, 1)
	require.Contains(t, upstream.urls[0], "/publishers/anthropic/models/claude-opus-5-5:streamRawPredict")
	require.Equal(t, "draw the pelican", gjson.GetBytes(upstream.bodies[0], "messages.0.content.0.text").String())
	require.Equal(t, "high", gjson.GetBytes(upstream.bodies[0], "output_config.effort").String())
	output, message := parsePelicanOutput(rec.Body.String())
	require.Empty(t, message)
	require.Equal(t, pelicanClaudeTestHTML, output)
}

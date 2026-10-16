package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/regional/ledger"
	"github.com/stretchr/testify/require"
)

type meterTestLedger struct {
	reservations, starts, completed, canceled int
	startErr                                  error
	event                                     ledger.Event
}

func (l *meterTestLedger) Reserve(context.Context, ledger.Reservation) (bool, error) {
	l.reservations++
	return true, nil
}
func (l *meterTestLedger) Start(context.Context, string) error             { l.starts++; return l.startErr }
func (l *meterTestLedger) CancelBeforeStart(context.Context, string) error { l.canceled++; return nil }
func (l *meterTestLedger) Complete(_ context.Context, e ledger.Event) error {
	l.completed++
	l.event = e
	return nil
}
func testRegionalMeter(l *meterTestLedger) *RegionalMetering {
	sequence := 0
	return &RegionalMetering{Ledger: l, Admit: func(_ context.Context, key *APIKey, account *Account, _ []byte) (ledger.Reservation, ledger.Price, error) {
		sequence++
		return ledger.Reservation{RequestID: fmt.Sprintf("meter-%d", sequence), UserID: key.UserID, APIKeyID: key.ID, AccountID: account.ID, AdmittedAt: time.Now().Add(-time.Millisecond)}, ledger.Price{Model: "mock-text", Input: 1_000_000_000, Output: 2_000_000_000, MaxInput: 1000, MaxOutput: 100}, nil
	}}
}
func TestRegionalMeterActualForwardJSON(t *testing.T) {
	for _, tc := range []struct {
		name, response string
		ok             bool
	}{
		{"terminal usage", "{\"id\":\"mock-response\",\"output\":[],\"usage\":{\"input_tokens\":10,\"output_tokens\":2}}", true},
		{"missing usage", "{\"id\":\"mock-response\",\"output\":[]}", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte("{\"model\":\"mock-text\",\"input\":\"hello\",\"max_output_tokens\":10,\"stream\":false}")
			upstream := &httpUpstreamRecorder{responses: []*http.Response{newOpenAIRejectedFieldTestResponse(200, tc.response)}}
			s := newOpenAIRejectedFieldTestService(upstream)
			l := &meterTestLedger{}
			s.regionalMetering = testRegionalMeter(l)
			c := newOpenAIRejectedFieldTestContext(body)
			c.Set("api_key", &APIKey{ID: 4, UserID: 5})
			result, err := s.Forward(context.Background(), c, newOpenAIRejectedFieldTestAccount(), body)
			if tc.ok {
				require.NoError(t, err)
				require.Equal(t, 1, l.completed)
				require.True(t, result.RegionalMetered)
				require.NoError(t, s.RecordUsage(context.Background(), &OpenAIRecordUsageInput{Result: result}))
			} else {
				require.Error(t, err)
				require.Zero(t, l.completed)
			}
			require.Equal(t, 1, l.starts)
			require.Len(t, upstream.bodies, 1)
			require.Zero(t, l.canceled)
		})
	}
}
func TestRegionalMeterStartFailureNeverCancelsOrSends(t *testing.T) {
	l := &meterTestLedger{startErr: errors.New("commit ack lost")}
	s := &OpenAIGatewayService{regionalMetering: testRegionalMeter(l)}
	body := []byte("{\"model\":\"mock-text\",\"input\":\"hello\",\"max_output_tokens\":10}")
	c := newOpenAIRejectedFieldTestContext(body)
	c.Set("api_key", &APIKey{ID: 4, UserID: 5})
	ctx, finish, err := s.beginRegionalMeter(context.Background(), c, newOpenAIRejectedFieldTestAccount(), body)
	require.NoError(t, err)
	require.Error(t, regionalMeterBeforeSend(ctx))
	require.Error(t, finish(nil, errors.New("failed")))
	require.Zero(t, l.canceled)
	require.Zero(t, l.completed)
	require.Error(t, regionalMeterBeforeSend(ctx))
	require.Equal(t, 1, l.starts)
}
func TestRegionalMeterWSTurnLifecycle(t *testing.T) {
	l := &meterTestLedger{}
	s := &OpenAIGatewayService{regionalMetering: testRegionalMeter(l)}
	body := []byte("{\"type\":\"response.create\",\"model\":\"mock-text\",\"input\":\"hello\",\"max_output_tokens\":10}")
	c := newOpenAIRejectedFieldTestContext(body)
	c.Set("api_key", &APIKey{ID: 4, UserID: 5})
	ctx, hooks, cleanup, err := s.wrapRegionalWSTurns(context.Background(), c, newOpenAIRejectedFieldTestAccount(), body, nil)
	require.NoError(t, err)
	defer cleanup()
	require.NoError(t, regionalMeterValidatePayload(ctx, body))
	require.NoError(t, regionalMeterBeforeSend(ctx))
	require.Error(t, regionalMeterBeforeSend(ctx))
	regionalMeterObserveUsage(ctx, []byte("{\"response\":{\"usage\":{\"input_tokens\":10,\"output_tokens\":2}}}"), "response.completed")
	result := &OpenAIForwardResult{Usage: OpenAIUsage{InputTokens: 10, OutputTokens: 2}}
	hooks.AfterTurn(1, result, nil)
	require.True(t, result.RegionalMetered)
	require.Equal(t, 1, l.completed)
	require.NoError(t, hooks.BeforeRequest(2, body, "mock-text"))
	require.NoError(t, regionalMeterBeforeSend(ctx))
	hooks.AfterTurn(2, nil, context.Canceled)
	require.Equal(t, 2, l.starts)
	require.Equal(t, 1, l.completed)
	require.Zero(t, l.canceled)
	require.Error(t, hooks.BeforeRequest(2, body, "mock-text"))
}
func TestRegionalMeterOutboundBounds(t *testing.T) {
	a := &regionalMeterAttempt{price: ledger.Price{Model: "mock-text"}, output: 10}
	ctx := context.WithValue(context.Background(), regionalMeterKey{}, a)
	for _, raw := range []string{"{\"model\":\"mock-text\"}", "{\"model\":\"mock-text\",\"max_output_tokens\":11}", "{\"model\":\"other\",\"max_output_tokens\":10}", "{\"model\":\"mock-text\",\"max_output_tokens\":10,\"tools\":[{}]}"} {
		require.Error(t, regionalMeterValidatePayload(ctx, []byte(raw)))
	}
	require.NoError(t, regionalMeterValidatePayload(ctx, []byte("{\"model\":\"mock-text\",\"max_output_tokens\":10}")))
}

func TestRegionalMeterOAuthReservesFullOutputWindow(t *testing.T) {
	l := &meterTestLedger{}
	s := &OpenAIGatewayService{regionalMetering: testRegionalMeter(l)}
	body := []byte("{\"model\":\"mock-text\",\"input\":\"hi\",\"max_output_tokens\":10}")
	c := newOpenAIRejectedFieldTestContext(body)
	c.Set("api_key", &APIKey{ID: 4, UserID: 5})
	account := newOpenAIRejectedFieldTestAccount()
	account.Type = AccountTypeOAuth
	ctx, _, err := s.beginRegionalMeter(context.Background(), c, account, body)
	require.NoError(t, err)
	a := regionalMeterAttemptFrom(ctx)
	require.Equal(t, a.price.MaxOutput, a.output)
	expected, err := a.price.Maximum(a.price.MaxOutput)
	require.NoError(t, err)
	require.Equal(t, expected, a.reservation.Maximum)
	require.NoError(t, regionalMeterValidatePayload(ctx, []byte("{\"model\":\"mock-text\"}")))
	a.fullOutputWindow = false
	require.Error(t, regionalMeterValidatePayload(ctx, []byte("{\"model\":\"mock-text\"}")))
}

func TestRegionalMeterRejectsNestedTools(t *testing.T) {
	for _, body := range []string{
		"{\"model\":\"mock-text\",\"max_tokens\":10,\"messages\":[{\"role\":\"assistant\",\"content\":\"ok\",\"tool_calls\":[{}]}]}",
		"{\"model\":\"mock-text\",\"max_tokens\":10,\"messages\":[{\"role\":\"assistant\",\"content\":\"ok\",\"function_call\":{}}]}",
	} {
		_, _, err := regionalTextOutputBound([]byte(body))
		require.Error(t, err)
	}
}

func TestRegionalMeterActualRawChatHTTPAndSSE(t *testing.T) {
	for _, tc := range []struct {
		name, response   string
		stream, complete bool
	}{
		{"json", "{\"id\":\"chat-mock\",\"model\":\"mock-text\",\"choices\":[{\"index\":0,\"message\":{\"role\":\"assistant\",\"content\":\"ok\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":2}}", false, true},
		{"sse", "data: {\"id\":\"chat-mock\",\"model\":\"mock-text\",\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":2}}\n\ndata: [DONE]\n\n", true, true},
		{"partial sse", "data: {\"id\":\"chat-mock\",\"model\":\"mock-text\",\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":2}}\n\n", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stream := "false"
			if tc.stream {
				stream = "true"
			}
			body := []byte("{\"model\":\"mock-text\",\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}],\"max_tokens\":10,\"stream\":" + stream + "}")
			response := newOpenAIRejectedFieldTestResponse(http.StatusOK, tc.response)
			if tc.stream {
				response.Header.Set("Content-Type", "text/event-stream")
			}
			upstream := &httpUpstreamRecorder{responses: []*http.Response{response}}
			s := newOpenAIRejectedFieldTestService(upstream)
			l := &meterTestLedger{}
			s.regionalMetering = testRegionalMeter(l)
			c := newOpenAIRejectedFieldTestContext(body)
			c.Set("api_key", &APIKey{ID: 4, UserID: 5})
			account := newOpenAIRejectedFieldTestAccount()
			account.Extra["openai_responses_supported"] = false
			result, err := s.ForwardAsChatCompletions(context.Background(), c, account, body, "", "")
			if tc.complete {
				require.NoError(t, err)
				require.True(t, result.RegionalMetered)
				require.Equal(t, 1, l.completed)
			} else {
				require.Error(t, err)
				require.Zero(t, l.completed)
			}
			require.Equal(t, 1, l.starts)
			require.Zero(t, l.canceled)
			require.Len(t, upstream.bodies, 1)
		})
	}
}

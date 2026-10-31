package service

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/requesttiming"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Check every returned-line boundary, including the final false Scan. A stage
// must appear as soon as a complete JSON payload is available, without waiting
// for an event separator or EOF, and must never be inferred from stale context.
func TestOpenAISSEScannerTimingEventContext(t *testing.T) {
	tests := []struct {
		name      string
		wire      string
		parsedAt  int
		visibleAt int
	}{
		{"event_without_json_type", "event: response.output_text.delta\ndata: {\"delta\":\"hello\"}\n\n", 2, 2},
		{"json_type_without_event", "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n", 1, 1},
		{"json_type_takes_precedence", "event: response.created\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n", 2, 2},
		{"json_metadata_takes_precedence", "event: response.output_text.delta\ndata: {\"type\":\"response.created\",\"delta\":\"metadata\"}\n\n", 2, 0},
		{"consecutive_different_events", "event: response.created\ndata: {}\n\nevent: response.output_text.delta\ndata: {\"delta\":\"hello\"}\n\nevent: response.completed\ndata: {}\n\n", 2, 5},
		{"blank_resets_event", "event: response.output_text.delta\n\ndata: {\"delta\":\"untyped\"}\n\n", 3, 0},
		{"blank_after_data_resets_event", "event: response.output_text.delta\ndata: {\"delta\":\"\"}\n\ndata: {\"delta\":\"untyped\"}\n\n", 2, 0},
		{"new_event_resets_type", "event: response.output_text.delta\nevent: response.created\ndata: {\"delta\":\"metadata\"}\n\n", 3, 0},
		{"empty_event_resets_type", "event: response.output_text.delta\nevent:\ndata: {\"delta\":\"untyped\"}\n\n", 3, 0},
		{"colonless_event_resets_type", "event: response.output_text.delta\nevent\ndata: {\"delta\":\"untyped\"}\n\n", 3, 0},
		{"comments_and_fields_preserve_context", "event:response.output_text.delta\n: heartbeat\nid: synthetic\nretry: 1000\ndata:{\"delta\":\"hello\"}\n\n", 5, 5},
		{"crlf", "event: response.output_text.delta\r\ndata: {\"delta\":\"hello\"}\r\n\r\n", 2, 2},
		{"multiline", "event: response.output_text.delta\ndata: {\ndata: \"delta\":\ndata: \"hello\"}\n\n", 4, 4},
		{"multiline_eof", "event: response.output_text.delta\ndata: {\n: heartbeat\ndata: \"delta\":\"hello\"}", 4, 4},
		{"single_line_eof", "event: response.output_text.delta\ndata: {\"delta\":\"hello\"}", 2, 2},
		{"incomplete_eof", "event: response.output_text.delta\ndata: {\"delta\":", 0, 0},
		{"event_only_eof", "event: response.output_text.delta", 0, 0},
		{"done_is_not_json", "event: response.output_text.delta\ndata: [DONE]\n\n", 0, 0},
		{"blank_discards_partial_data", "event: response.output_text.delta\ndata: {\n\ndata: \"delta\":\"hello\"}\n\n", 0, 0},
		{"new_event_discards_partial_data", "event: response.output_text.delta\ndata: {\nevent: response.created\ndata: \"delta\":\"hello\"}\n\n", 0, 0},
		{"valid_event_after_partial", "event: response.created\ndata: {\nevent: response.output_text.delta\ndata: {\"delta\":\"hello\"}\n\n", 4, 4},
		{"repaired_events_do_not_leak_type", "event: response.created\ndata: {\"type\":\"response.created\"}{\"type\":\"response.in_progress\"}\n\ndata: {\"delta\":\"untyped\"}\n\n", 2, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// The repair case has more emitted lines than input lines. Use the
			// untimed scanner as the output contract for both modes.
			var lines []string
			plain := newOpenAISSEJSONDocumentScanner(bufio.NewScanner(strings.NewReader(tt.wire)))
			for plain.Scan() {
				lines = append(lines, plain.Text())
			}
			require.NoError(t, plain.Err())
			for _, fragmented := range []bool{false, true} {
				for count := 0; count <= len(lines)+1; count++ {
					t.Run(fmt.Sprintf("fragmented=%t/scan=%d", fragmented, count), func(t *testing.T) {
						collector := requesttiming.New(time.Now(), 0)
						req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(requesttiming.With(context.Background(), collector))
						req, _ = requesttiming.StartAttempt(req, 1, 0)
						var reader io.Reader = strings.NewReader(tt.wire)
						if fragmented {
							reader = iotest.OneByteReader(reader)
						}
						scanner := newOpenAISSEJSONDocumentScanner(bufio.NewScanner(reader))
						scanner.ctx = req.Context()
						for i := 0; i < count; i++ {
							if i == len(lines) {
								require.False(t, scanner.Scan())
								continue
							}
							require.True(t, scanner.Scan())
							require.Equal(t, lines[i], scanner.Text(), "timing must preserve emitted lines")
						}
						require.NoError(t, scanner.Err())
						collector.Finish(200, false)
						collector.WhenFinished(func(snapshot requesttiming.Snapshot) {
							events := snapshot.Attempts[0].Events
							_, parsed := events["sse_parsed_first"]
							_, visible := events["sse_visible_first"]
							require.Equal(t, tt.parsedAt > 0 && count >= tt.parsedAt, parsed, "SSE parsed stage must follow the complete JSON payload")
							require.Equal(t, tt.visibleAt > 0 && count >= tt.visibleAt, visible, "SSE visible stage must use the current event context")
							require.NotContains(t, events, "first_visible", "scanner diagnostics must not set downstream TTFT")
						})
					})
				}
			}
		})
	}
}

func TestOpenAISSEScannerTimingBoundsPendingData(t *testing.T) {
	// Exceed the diagnostic cap across multiple individually bounded lines.
	// Subsequent data in that event must not be mistaken for a fresh payload;
	// the next event must recover normally.
	for _, nextEvent := range []bool{false, true} {
		t.Run(fmt.Sprintf("next_event=%t", nextEvent), func(t *testing.T) {
			padding := strings.Repeat("x", maxOpenAIConcatenatedJSONBytes/2)
			wire := "event: response.output_text.delta\ndata: {\"padding\":\"" + padding + "\ndata: " + padding +
				"\ndata: {\"delta\":\"discarded\"}\n"
			if nextEvent {
				wire += "event: response.output_text.delta\ndata: {\"delta\":\"hello\"}\n\n"
			}
			collector := requesttiming.New(time.Now(), 0)
			req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(requesttiming.With(context.Background(), collector))
			req, _ = requesttiming.StartAttempt(req, 1, 0)
			input := bufio.NewScanner(strings.NewReader(wire))
			input.Buffer(make([]byte, 1024), maxOpenAIConcatenatedJSONBytes)
			scanner := newOpenAISSEJSONDocumentScanner(input)
			scanner.ctx = req.Context()
			for scanner.Scan() {
			}
			require.NoError(t, scanner.Err())
			collector.Finish(200, false)
			collector.WhenFinished(func(snapshot requesttiming.Snapshot) {
				_, parsed := snapshot.Attempts[0].Events["sse_parsed_first"]
				_, visible := snapshot.Attempts[0].Events["sse_visible_first"]
				require.Equal(t, nextEvent, parsed)
				require.Equal(t, nextEvent, visible)
			})
		})
	}
}

func TestOpenAISSEScannerTimingRepairedDocuments(t *testing.T) {
	wire := "event: response.created\ndata: {\"type\":\"response.created\"}{\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\ndata: {\"delta\":\"untyped\"}\n\n"
	var reference []string
	for _, timed := range []bool{false, true} {
		collector := requesttiming.New(time.Now(), 0)
		req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(requesttiming.With(context.Background(), collector))
		req, _ = requesttiming.StartAttempt(req, 1, 0)
		scanner := newOpenAISSEJSONDocumentScanner(bufio.NewScanner(iotest.OneByteReader(strings.NewReader(wire))))
		if timed {
			scanner.ctx = req.Context()
		}
		var lines []string
		for scanner.Scan() {
			lines = append(lines, scanner.Text())
		}
		require.NoError(t, scanner.Err())
		if !timed {
			reference = lines
		} else {
			require.Equal(t, reference, lines, "diagnostics must preserve concatenated JSON repair")
			collector.Finish(200, false)
			collector.WhenFinished(func(snapshot requesttiming.Snapshot) {
				require.Contains(t, snapshot.Attempts[0].Events, "sse_visible_first")
			})
		}
	}
}

// Exercise the real native handler, its synchronous and async scanners, and
// ResponseContext's physical-attempt binding. The caller has a stale transport
// context on purpose; observations belong to resp.Request and its parent only.
func TestOpenAINativeSSETimingEventContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, interval := range []int{0, 30} {
		for _, jsonType := range []bool{false, true} {
			t.Run(fmt.Sprintf("interval=%d/json_type=%t", interval, jsonType), func(t *testing.T) {
				collector := requesttiming.New(time.Now(), 0)
				ctx := requesttiming.With(context.Background(), collector)
				req := httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(ctx)
				req, _ = requesttiming.StartAttempt(req, 1, 0)
				stale, _ := requesttiming.StartTransport(req)
				actual, _ := requesttiming.StartTransport(req)
				delta := `{"delta":"hello"}`
				if jsonType {
					delta = `{"type":"response.output_text.delta","delta":"hello"}`
				}
				wire := "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_timing\",\"created_at\":1}}\n\n" +
					"event: response.output_text.delta\ndata: " + delta + "\n\n" +
					"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_timing\",\"created_at\":1,\"usage\":{\"input_tokens\":2,\"output_tokens\":1}}}\n\n"
				resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Request: actual,
					Body: io.NopCloser(iotest.OneByteReader(strings.NewReader(wire)))}
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = req
				svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{StreamDataIntervalTimeout: interval}}}
				result, err := svc.handleStreamingResponse(stale.Context(), resp, c,
					&Account{ID: 1, Platform: PlatformOpenAI}, time.Now(), "test-model", "test-model")
				require.NoError(t, err)
				require.NotNil(t, result)
				require.NotNil(t, result.firstTokenMs, "native TTFT must still observe visible output")
				require.Equal(t, 2, result.usage.InputTokens)
				require.Equal(t, 1, result.usage.OutputTokens)
				require.Contains(t, recorder.Body.String(), "event: response.output_text.delta\ndata: "+delta+"\n\n")
				collector.Finish(200, false)
				collector.WhenFinished(func(snapshot requesttiming.Snapshot) {
					require.Len(t, snapshot.Attempts, 3)
					parent, staleAttempt, physical := snapshot.Attempts[0], snapshot.Attempts[1], snapshot.Attempts[2]
					for _, key := range []string{"sse_parsed_first", "sse_visible_first"} {
						require.Contains(t, physical.Events, key, "native scanner must record the SSE visible stage on the response attempt")
						require.Equal(t, physical.Events[key], parent.Events[key])
						require.NotContains(t, staleAttempt.Events, key, "diagnostics must not leak to another transport attempt")
					}
					require.Contains(t, physical.Events, "first_visible")
					require.LessOrEqual(t, physical.Events["sse_parsed_first"], physical.Events["sse_visible_first"])
					require.LessOrEqual(t, physical.Events["sse_visible_first"], physical.Events["first_visible"])
				})
			})
		}
	}
}

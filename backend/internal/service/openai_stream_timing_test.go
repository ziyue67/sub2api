package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/requesttiming"
	"github.com/stretchr/testify/require"
)

func TestToolConversionTimingRetainsPreConversionObservation(t *testing.T) {
	c := requesttiming.New(time.Now(), 0)
	req, _ := http.NewRequestWithContext(requesttiming.With(context.Background(), c), "GET", "https://example.test", nil)
	req, _ = requesttiming.StartAttempt(req, 1, 0)
	wire := strings.Join([]string{
		`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"i1","call_id":"c1","name":"exec"}}`,
		"",
		`data: {"type":"response.function_call_arguments.delta","item_id":"i1","delta":"{\"input\":\"di"}`,
		"",
		`data: {"type":"response.function_call_arguments.done","item_id":"i1","arguments":"{\"input\":\"dir\"}"}`,
		"", "",
	}, "\n")
	body := newResponsesClientToolStreamBody(io.NopCloser(bytes.NewBufferString(wire)), apicompat.ResponsesClientToolMapping{CustomTools: map[string]bool{"exec": true}}, defaultMaxLineSize, req.Context())
	out, err := io.ReadAll(body)
	require.NoError(t, err)
	require.NoError(t, body.Close())
	require.Contains(t, string(out), "response.custom_tool_call_input.delta")
	c.Finish(200, false)
	c.WhenFinished(func(s requesttiming.Snapshot) {
		e := s.Attempts[0].Events
		for _, key := range []string{"sse_parsed_first", "sse_visible_first", "tool_converted_first", "tool_visible_first"} {
			require.Contains(t, e, key)
		}
		require.GreaterOrEqual(t, e["tool_visible_first"], e["sse_visible_first"])
	})
}

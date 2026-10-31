package requesttiming

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptrace"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAccountConnectionTimingClockAndPrivacy(t *testing.T) {
	start := time.Now()
	now := start
	c := newCollector(start, 0, func() time.Time { return now })
	ctx := With(context.Background(), c)
	req, _ := http.NewRequestWithContext(ctx, "POST", "https://private.example/secret?api_key=secret", nil)
	req, trace := StartAttempt(req, 87654321, 12345678)
	hooks := httptrace.ContextClientTrace(req.Context())
	now = start.Add(2 * time.Millisecond)
	hooks.GetConn("private.example")
	now = start.Add(7 * time.Millisecond)
	hooks.GotConn(httptrace.GotConnInfo{})
	now = start.Add(11 * time.Millisecond)
	hooks.WroteRequest(httptrace.WroteRequestInfo{})
	now = start.Add(29 * time.Millisecond)
	trace.Response(&http.Response{StatusCode: 200, Header: http.Header{}}, nil)
	now = start.Add(30 * time.Millisecond)
	encoding := Observe(ctx, "account_event_encode")
	now = start.Add(35 * time.Millisecond)
	encoding()
	now = start.Add(40 * time.Millisecond)
	Mode(ctx, "stream")
	Mark(ctx, "account_first_content")
	now = start.Add(45 * time.Millisecond)
	encoding = Observe(ctx, "account_event_encode")
	now = start.Add(50 * time.Millisecond)
	encoding()
	// A second observation cannot replace the true first content.
	now = start.Add(70 * time.Millisecond)
	Mark(ctx, "account_first_content")
	Mark(ctx, "account_stream_end")
	var result AccountTestTiming
	c.WhenFinished(func(d Snapshot) { result = AccountTestProjection(d) })
	now = start.Add(80 * time.Millisecond)
	Outcome(ctx, "success", false)
	c.Finish(200, false)
	require.Equal(t, 80.0, *result.TotalMS)
	for _, tc := range []struct {
		metric               AccountTestMetric
		start, end, duration float64
	}{
		{result.Connection, 2, 7, 5}, {result.ResponseHeaders, 11, 29, 18},
		{result.FirstContent, 0, 40, 40}, {result.StreamOutput, 40, 70, 30},
	} {
		require.Equal(t, tc.start, *tc.metric.StartMS)
		require.Equal(t, tc.end, *tc.metric.EndMS)
		require.Equal(t, tc.duration, *tc.metric.DurationMS)
	}
	require.Equal(t, start.Add(80*time.Millisecond), *result.FinishedAt)
	require.Equal(t, 10.0, *result.LocalProcessing.DurationMS)
	require.Equal(t, 30.0, *result.LocalProcessing.StartMS)
	require.Equal(t, 50.0, *result.LocalProcessing.EndMS)
	require.Equal(t, 2, result.LocalProcessing.Segments)
	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	for _, secret := range []string{"private.example", "secret", "87654321", "12345678", "route", "proxy", "trace_id", "account_id", "request_bytes"} {
		require.NotContains(t, string(encoded), secret)
	}
}

func TestAccountConnectionTimingMissingAndRanges(t *testing.T) {
	for _, tc := range []struct {
		mode     string
		attempts []Attempt
		reason   string
	}{
		{"non_stream", nil, "non_stream"},
		{"stream", nil, "no_content"},
		{"stream", []Attempt{{Kind: "egress"}, {Kind: "egress"}}, "multiple_requests"},
	} {
		s := AccountTestProjection(Snapshot{StartedAt: time.Now(), TTFTMode: tc.mode, TotalMS: 100, Attempts: tc.attempts})
		require.Nil(t, s.FirstContent.DurationMS)
		require.Equal(t, tc.reason, s.FirstContent.Reason)
	}
	for _, bounds := range [][2]float64{{-1, 0}, {5, 4}, {0, 101}} {
		require.Nil(t, accountMetric(bounds[0], bounds[1], 100).DurationMS)
	}
	require.Equal(t, 0.0, *accountMetric(0, 0, 100).DurationMS)
	// Reason codes are stable enums, never derived from provider error strings.
	require.False(t, strings.Contains(AccountTestStarted(time.Now()).LocalProcessing.Reason, ":"))
}

func TestAccountConnectionTimingHeadersPrecedeBodySetup(t *testing.T) {
	start := time.Now()
	now := start
	c := newCollector(start, 0, func() time.Time { return now })
	req, _ := http.NewRequestWithContext(With(context.Background(), c), "POST", "https://fixture.invalid", nil)
	req, trace := StartAttempt(req, 1, 0)
	now = start.Add(5 * time.Millisecond)
	httptrace.ContextClientTrace(req.Context()).WroteRequest(httptrace.WroteRequestInfo{})
	resp := &http.Response{StatusCode: 200, Header: http.Header{}}
	now = start.Add(10 * time.Millisecond)
	trace.Headers(resp)
	// Decompression initialization or other body setup can block after headers.
	now = start.Add(70 * time.Millisecond)
	trace.Response(resp, nil)
	var result AccountTestTiming
	c.WhenFinished(func(d Snapshot) { result = AccountTestProjection(d) })
	now = start.Add(80 * time.Millisecond)
	c.Finish(200, false)
	require.Equal(t, 10.0, *result.ResponseHeaders.EndMS)
	require.Equal(t, 5.0, *result.ResponseHeaders.DurationMS)
}

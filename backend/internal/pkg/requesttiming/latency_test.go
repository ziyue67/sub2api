package requesttiming

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestIssue603RealHTTPTransportBoundaries(t *testing.T) {
	release := make(chan struct{})
	var releaseOnce sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.Copy(io.Discard, r.Body)
		if err != nil {
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: synthetic\n\n")
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Errorf("flush synthetic SSE: %v", err)
			return
		}
		<-release
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer func() { releaseOnce.Do(func() { close(release) }); server.Close() }()
	c := New(time.Now(), 0)
	client := server.Client()
	client.Timeout = 3 * time.Second
	req, err := http.NewRequestWithContext(With(context.Background(), c), "POST", server.URL, strings.NewReader("synthetic body"))
	require.NoError(t, err)
	req, _ = StartAttempt(req, 7, 0)
	req, tr := StartTransport(req)
	resp, err := client.Do(req)
	tr.Response(resp, err)
	require.NoError(t, err)
	_, err = bufio.NewReader(resp.Body).ReadString('\n')
	require.NoError(t, err)
	Output(ResponseContext(req.Context(), resp), true, true, "completed")
	releaseOnce.Do(func() { close(release) })
	_, err = io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	c.Finish(200, false)
	c.WhenFinished(func(s Snapshot) {
		derived := DeriveLatency(s)
		require.Len(t, derived.Attempts, 1)
		a := derived.Attempts[0]
		require.Equal(t, "measured", a.Upload.Status)
		require.Equal(t, "measured", a.SemanticWait.Status)
		require.GreaterOrEqual(t, *a.Upload.MS, 0.0)
		require.GreaterOrEqual(t, *a.SemanticWait.MS, 0.0)
	})

	c = New(time.Now(), 0)
	req, err = http.NewRequestWithContext(With(context.Background(), c), "POST", server.URL, strings.NewReader("short"))
	require.NoError(t, err)
	req.ContentLength = 1024
	req, _ = StartAttempt(req, 7, 0)
	req, tr = StartTransport(req)
	resp, err = client.Do(req)
	tr.Response(resp, err)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	require.Error(t, err)
	c.Finish(502, false)
	c.WhenFinished(func(s Snapshot) {
		a := DeriveLatency(s).Attempts[0]
		require.Nil(t, a.Upload.MS)
		require.Equal(t, "write_failed", a.Upload.Status)
		require.Nil(t, a.SemanticWait.MS)
	})
}

func latencyAttempt(number int, account int64, start, end float64) Attempt {
	return Attempt{Kind: "http", Number: number, AccountID: account, StartMS: start, EndMS: &end, RequestBytes: 2 << 20, Events: map[string]float64{"headers_written": start + 10, "request_written": start + 40, "first_semantic": start + 100, "first_visible": start + 150, "response_headers": start + 90}}
}

// Marshaling the existing collector is a production-path RED counterexample:
// old collector source compiles but does not publish the derived JSON contract.
func TestIssue603CollectorPublishesAttemptMetrics(t *testing.T) {
	c := New(time.Now(), 0)
	c.data.Attempts = []Attempt{latencyAttempt(1, 1, 0, 200)}
	c.Finish(200, false)
	c.WhenFinished(func(s Snapshot) {
		raw, err := json.Marshal(s)
		require.NoError(t, err)
		var doc map[string]any
		require.NoError(t, json.Unmarshal(raw, &doc))
		require.Contains(t, doc, "latency", "finished collector must publish attempt-derived observations")
	})
}

func TestIssue603AttemptBoundaryAndFailoverSemantics(t *testing.T) {
	s := Snapshot{BodyBytes: 2 << 20, Attempts: []Attempt{latencyAttempt(1, 1, 0, 200), latencyAttempt(2, 1, 220, 420), latencyAttempt(3, 2, 440, 640)}}
	s.Events = map[string]float64{"first_semantic": 1}
	s.Attempts = append([]Attempt{{Kind: "egress", AccountID: 99}}, s.Attempts...)
	s.Attempts[0].Number = 9
	for i := 1; i < len(s.Attempts); i++ {
		s.Attempts[i].Parent = 9
	}
	got := DeriveLatency(s)
	require.Len(t, got.Attempts, 3)
	require.Equal(t, 1, got.AccountSwitches)
	require.Equal(t, 1, got.SameAccountRetries)
	require.Equal(t, 30.0, *got.Attempts[0].Upload.MS)
	require.Equal(t, 60.0, *got.Attempts[0].SemanticWait.MS)
	require.Equal(t, 110.0, *got.Attempts[0].VisibleWait.MS)
	require.Equal(t, 60.0, *got.ExtraUploadComponentMS)
	require.Equal(t, "measured_components", got.CostStatus)
	for _, tc := range []struct {
		name   string
		modify func(*Attempt)
		status string
	}{
		{"missing", func(a *Attempt) { delete(a.Events, "request_written") }, "not_collected"},
		{"reversed", func(a *Attempt) { a.Events["request_written"] = 0 }, "invalid_boundary"},
		{"failed write", func(a *Attempt) { a.Events["request_write_failed"] = 30 }, "write_failed"},
		{"unfinished", func(a *Attempt) { a.EndMS = nil }, "invalid_boundary"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := latencyAttempt(1, 1, 0, 200)
			tc.modify(&a)
			v := DeriveLatency(Snapshot{Attempts: []Attempt{a}})
			require.Nil(t, v.Attempts[0].Upload.MS)
			require.Equal(t, tc.status, v.Attempts[0].Upload.Status)
			require.Nil(t, v.Attempts[0].SemanticWait.MS)
		})
	}
	a := latencyAttempt(1, 1, 0, 200)
	a.Events["request_written"] = a.Events["headers_written"]
	require.Equal(t, 0.0, *DeriveLatency(Snapshot{Attempts: []Attempt{a}}).Attempts[0].Upload.MS, "measured zero must survive")
	s.Attempts[2].StartMS = 100
	require.Equal(t, "overlap", DeriveLatency(s).CostStatus)
	require.Nil(t, DeriveLatency(s).ExtraUploadComponentMS)
	s.Truncated = true
	require.Equal(t, "partial", DeriveLatency(s).Status)
	s.Truncated = false
	s.Attempts = append(s.Attempts, Attempt{Kind: "egress", Number: 10, AccountID: 3})
	require.Equal(t, "partial", DeriveLatency(s).Status, "unobserved physical send must not assert complete switch history")
	unknown := Snapshot{BodyBytes: 2 << 20, Attempts: []Attempt{latencyAttempt(1, 1, 0, 200), latencyAttempt(2, 0, 220, 420)}}
	derived := DeriveLatency(unknown)
	require.Equal(t, 1, derived.UnknownTransitions)
	require.Equal(t, "partial", derived.Status, "unknown account identity cannot be a complete zero-switch sample")
	require.Equal(t, "partial", derived.CostStatus)
}

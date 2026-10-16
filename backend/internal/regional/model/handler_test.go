package model

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/regional/ledger"
	"github.com/stretchr/testify/require"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type memoryLedger struct {
	started, completed bool
	event              ledger.Event
	reservations       int
}

func (l *memoryLedger) Reserve(context.Context, ledger.Reservation) (bool, error) {
	l.reservations++
	return true, nil
}
func (l *memoryLedger) Start(context.Context, string) error             { l.started = true; return nil }
func (l *memoryLedger) CancelBeforeStart(context.Context, string) error { return nil }
func (l *memoryLedger) Complete(_ context.Context, e ledger.Event) error {
	l.completed = true
	l.event = e
	return nil
}
func TestHandlerRealHTTPAndSSE(t *testing.T) {
	for _, tc := range []struct {
		name, body, content string
		stream, complete    bool
	}{
		{name: "HTTP", body: "{\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":2}}", content: "application/json", complete: true},
		{name: "SSE", body: "data: {\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":2}}\n\ndata: [DONE]\n\n", content: "text/event-stream", stream: true, complete: true},
		{name: "EOF without usage", body: "data: [DONE]\n\n", content: "text/event-stream", stream: true},
		{name: "missing terminal", body: "data: {\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":2}}\n\n", content: "text/event-stream", stream: true},
		{name: "excess output", body: "{\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":200}}", content: "application/json"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := &memoryLedger{}
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.True(t, l.started)
				w.Header().Set("Content-Type", tc.content)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer upstream.Close()
			h := &Handler{Ledger: l, Admit: func(context.Context, Request) (ledger.Reservation, ledger.Price, error) {
				return ledger.Reservation{AccountID: 9}, ledger.Price{Model: "text", Input: 1000000000, Output: 2000000000, MaxInput: 1000, MaxOutput: 100}, nil
			}, Forward: func(ctx context.Context, res ledger.Reservation, _ Request) (*http.Response, error) {
				require.EqualValues(t, 9, res.AccountID)
				r, _ := http.NewRequestWithContext(ctx, "POST", upstream.URL, nil)
				return upstream.Client().Do(r)
			}}
			raw := "{\"model\":\"text\",\"messages\":[{\"role\":\"user\",\"content\":\"hello\"}],\"max_tokens\":10,\"stream\":"
			if tc.stream {
				raw += "true}"
			} else {
				raw += "false}"
			}
			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(raw)))
			require.True(t, l.started)
			require.Equal(t, tc.complete, l.completed)
		})
	}
}
func TestHandlerRejectsUnboundedFeaturesBeforeAdmission(t *testing.T) {
	for _, body := range []string{"{\"model\":\"text\",\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}]}", "{\"model\":\"text\",\"max_tokens\":10,\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}],\"tools\":[]}"} {
		h := &Handler{Ledger: &memoryLedger{}, Admit: func(context.Context, Request) (ledger.Reservation, ledger.Price, error) {
			t.Fatal("must reject before admission")
			return ledger.Reservation{}, ledger.Price{}, nil
		}, Forward: func(context.Context, ledger.Reservation, Request) (*http.Response, error) {
			t.Fatal("must not forward")
			return nil, nil
		}}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body)))
		require.Equal(t, 400, w.Code)
	}
}

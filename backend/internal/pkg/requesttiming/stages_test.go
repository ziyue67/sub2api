package requesttiming

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestStreamingStagesAreFirstObservationAndAttemptScoped(t *testing.T) {
	c := New(time.Now(), 0)
	req, _ := http.NewRequestWithContext(With(context.Background(), c), "GET", "https://example.test", nil)
	req, _ = StartAttempt(req, 1, 2)
	first, _ := StartTransport(req)
	second, _ := StartTransport(req)
	body := WrapStage(first.Context(), io.NopCloser(strings.NewReader("ab")), "raw_body_first")
	_, _ = body.Read(make([]byte, 1))
	before := c.data.Attempts[1].Events["raw_body_first"]
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); Stage(first.Context(), "raw_body_first") }()
	}
	wg.Wait()
	if before != c.data.Attempts[1].Events["raw_body_first"] {
		t.Fatal("first timestamp overwritten")
	}
	Stage(second.Context(), "decoded_body_first")
	Encoding(first.Context(), &http.Response{Header: http.Header{"Content-Encoding": {"zstd"}}})
	Encoding(second.Context(), &http.Response{Uncompressed: true})
	c.Finish(200, false)
	c.WhenFinished(func(s Snapshot) {
		if _, ok := s.Attempts[2].Events["raw_body_first"]; ok {
			t.Fatal("stage leaked across retries")
		}
		if s.Attempts[0].Events["raw_body_first"] != before {
			t.Fatal("parent missing stage")
		}
		if s.Attempts[1].ContentEncoding != "zstd" || s.Attempts[2].ContentEncoding != "gzip" || !s.Attempts[2].AutoDecompressed {
			t.Fatal("encoding lost")
		}
	})
}
func TestEmptyBodyDoesNotRecordFirstData(t *testing.T) {
	c := New(time.Now(), 0)
	req, _ := http.NewRequestWithContext(With(context.Background(), c), "GET", "https://example.test", nil)
	req, _ = StartAttempt(req, 1, 0)
	_, _ = io.ReadAll(WrapStage(req.Context(), io.NopCloser(strings.NewReader("")), "raw_body_first"))
	if len(c.data.Attempts[0].Events) != 0 {
		t.Fatal("EOF counted as data")
	}
}

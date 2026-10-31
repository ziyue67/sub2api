package repository

import (
	"bufio"
	"compress/gzip"
	"context"
	"github.com/Wei-Shaw/sub2api/internal/pkg/requesttiming"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
)

// A flushed SSE frame must be readable while the upstream remains open.
func TestStreamDecompressionBeforeEOF(t *testing.T) {
	for _, encoding := range []string{"gzip", "zstd"} {
		t.Run(encoding, func(t *testing.T) {
			r, w := io.Pipe()
			defer func() { _ = r.Close() }()
			release := make(chan struct{})
			defer close(release)
			go func() {
				defer func() { _ = w.Close() }()
				var enc interface {
					io.Writer
					Flush() error
					Close() error
				}
				if encoding == "gzip" {
					enc = gzip.NewWriter(w)
				} else {
					enc, _ = zstd.NewWriter(w)
				}
				_, _ = enc.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n"))
				_ = enc.Flush()
				<-release
				_ = enc.Close()
			}()
			got := make(chan string, 1)
			go func() {
				resp := &http.Response{Header: http.Header{"Content-Encoding": {encoding}}, Body: r}
				decompressResponseBody(resp)
				scanner := bufio.NewScanner(resp.Body)
				if scanner.Scan() {
					got <- scanner.Text()
				} else {
					got <- ""
				}
			}()
			select {
			case line := <-got:
				if line == "" {
					t.Fatal("empty frame")
				}
				t.Log("frame delivered before EOF")
			case <-time.After(time.Second):
				t.Error("flushed frame blocked until more data or EOF")
			}
		})
	}
}

func TestStreamTimingDoesNotLabelUndecodedBodyAsDecoded(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "unsupported-test")
		_, _ = io.WriteString(w, "encoded test bytes")
	}))
	defer server.Close()
	collector := requesttiming.New(time.Now(), 0)
	req, _ := http.NewRequestWithContext(requesttiming.With(context.Background(), collector), "GET", server.URL, nil)
	req, _ = requesttiming.StartAttempt(req, 1, 0)
	resp, err := doUpstreamRequest(server.Client(), req)
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	collector.Finish(200, false)
	collector.WhenFinished(func(s requesttiming.Snapshot) {
		a := s.Attempts[1]
		if a.ContentEncoding != "unsupported-test" {
			t.Fatal("lost original encoding")
		}
		if _, ok := a.Events["raw_body_first"]; !ok {
			t.Fatal("missing raw data")
		}
		if _, ok := a.Events["decoded_body_first"]; ok {
			t.Fatal("undecoded data mislabeled")
		}
	})
}

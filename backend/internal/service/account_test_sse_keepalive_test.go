package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAccountTestKeepaliveContinuesAfterEventsAndHeaders(t *testing.T) {
	c, rec := newCompactBridgeTestContext(t, false)
	original := c.Writer
	stop := startAccountTestSSEKeepalive(c, keepaliveTestInterval)
	defer stop()
	s := &AccountTestService{}
	s.sendEvent(c, TestEvent{Type: "test_start", Model: "gpt-6-astra"})
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Flush()
	time.Sleep(5 * keepaliveTestInterval)
	s.sendEvent(c, TestEvent{Type: "content", Text: "<!doctype html><html>ok</html>"})
	time.Sleep(5 * keepaliveTestInterval)
	s.sendEvent(c, TestEvent{Type: "test_complete", Success: true})
	stop()
	stop()

	// 停拍后再读 recorder，避免测试自身与后台 writer 产生竞态。
	body := rec.Body.String()
	require.Same(t, original, c.Writer)
	require.NoError(t, c.Request.Context().Err())
	require.GreaterOrEqual(t, strings.Count(body, ": keepalive\n\n"), 3)
	require.Contains(t, strings.Split(body, `"type":"content"`)[1], ": keepalive\n\n")
	text, errMsg := parseTestSSEOutput(body)
	require.Empty(t, errMsg)
	require.Equal(t, "<!doctype html><html>ok</html>", text)
	require.Equal(t, 1, strings.Count(body, `"type":"test_complete"`))
	time.Sleep(3 * keepaliveTestInterval)
	require.Equal(t, body, rec.Body.String())
}

func TestAccountTestKeepaliveAlreadyCanceledDoesNotWrite(t *testing.T) {
	c, rec := newCompactBridgeTestContext(t, false)
	ctx, cancel := context.WithCancel(c.Request.Context())
	c.Request = c.Request.WithContext(ctx)
	cancel()
	stop := startAccountTestSSEKeepalive(c, keepaliveTestInterval)
	stop()
	require.Empty(t, rec.Body.String())
}

func TestAccountTestKeepaliveStreamTerminalSemantics(t *testing.T) {
	for _, tc := range []struct {
		name, upstream, text, errorText string
		success                         bool
	}{
		{"completed", "data: {\"type\":\"response.output_text.delta\",\"delta\":\"<html>ok</html>\"}\n\ndata: {\"type\":\"response.completed\"}\n\n", "<html>ok</html>", "", true},
		{"failed", "data: {\"type\":\"response.failed\",\"response\":{\"error\":{\"message\":\"probe failed\"}}}\n\n", "", "probe failed", false},
		{"truncated", "data: {\"type\":\"response.created\"}\n\n", "", "Stream ended before response.completed", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, rec := newCompactBridgeTestContext(t, false)
			stop := startAccountTestSSEKeepalive(c, keepaliveTestInterval)
			defer stop()
			reader, writer := io.Pipe()
			producerDone := make(chan struct{})
			go func() {
				defer close(producerDone)
				time.Sleep(5 * keepaliveTestInterval)
				_, _ = io.WriteString(writer, tc.upstream)
				_ = writer.Close()
			}()
			err := (&AccountTestService{}).processOpenAIStream(c, reader)
			_ = reader.Close()
			<-producerDone
			stop()
			require.Equal(t, tc.success, err == nil)
			text, errMsg := parseTestSSEOutput(rec.Body.String())
			require.Equal(t, tc.text, text)
			require.Equal(t, tc.errorText, errMsg)
			require.Equal(t, tc.success, strings.Contains(rec.Body.String(), `"type":"test_complete"`))
			require.GreaterOrEqual(t, strings.Count(rec.Body.String(), ": keepalive\n\n"), 2)
		})
	}
}

type failingAccountTestWriter struct {
	gin.ResponseWriter
}

func (w *failingAccountTestWriter) Write([]byte) (int, error) {
	return 0, errors.New("client disconnected")
}

func TestAccountTestKeepaliveCancelsOnWriteFailure(t *testing.T) {
	for _, heartbeat := range []bool{false, true} {
		t.Run(map[bool]string{false: "event", true: "heartbeat"}[heartbeat], func(t *testing.T) {
			c, _ := newCompactBridgeTestContext(t, false)
			stop := startAccountTestSSEKeepalive(c, keepaliveTestInterval)
			defer stop()
			w, ok := c.Writer.(*accountTestKeepaliveWriter)
			require.True(t, ok, "account test writer must serialize keepalive and business events")
			w.k.mu.Lock()
			broken := &failingAccountTestWriter{ResponseWriter: w.ResponseWriter}
			w.ResponseWriter = broken
			w.k.writer = broken
			w.k.mu.Unlock()
			if !heartbeat {
				_, err := w.WriteString("data: content\n\n")
				require.Error(t, err)
			}
			select {
			case <-c.Request.Context().Done():
			case <-time.After(time.Second):
				t.Fatal("upstream context was not canceled")
			}
		})
	}
}

func TestAccountTestKeepaliveCancellationReleasesReader(t *testing.T) {
	c, rec := newCompactBridgeTestContext(t, false)
	parent, cancel := context.WithCancel(c.Request.Context())
	c.Request = c.Request.WithContext(parent)
	stop := startAccountTestSSEKeepalive(c, keepaliveTestInterval)
	reader, writer := io.Pipe()
	released := make(chan struct{})
	ctx := c.Request.Context()
	go func() {
		defer close(released)
		<-ctx.Done()
		_ = writer.CloseWithError(ctx.Err())
	}()
	go cancel()
	err := (&AccountTestService{}).processOpenAIStream(c, reader)
	_ = reader.Close()
	<-released
	stop()
	require.Error(t, err)
	require.NotContains(t, rec.Body.String(), `"type":"test_complete"`)
}

// 真实 HTTP 首包等待：客户端必须先收到 SSE，再等上游响应，取消后两端都要退出。
func TestAccountTestKeepaliveBeforeUpstreamHeaders(t *testing.T) {
	upstreamStarted := make(chan struct{})
	upstreamDone := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(upstreamStarted)
		defer close(upstreamDone)
		<-r.Context().Done()
	}))
	defer upstream.Close()
	handlerDone := make(chan struct{})
	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(handlerDone)
		c, _ := gin.CreateTestContext(w)
		c.Request = r
		stop := startAccountTestSSEKeepalive(c, keepaliveTestInterval)
		defer stop()
		req, _ := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, upstream.URL, nil)
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			_ = resp.Body.Close()
		}
	}))
	defer downstream.Close()
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(downstream.URL)
	require.NoError(t, err)
	require.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))
	first := make([]byte, len(": keepalive\n\n"))
	_, err = io.ReadFull(resp.Body, first)
	require.NoError(t, err)
	require.Equal(t, ": keepalive\n\n", string(first))
	<-upstreamStarted
	_, err = io.ReadFull(resp.Body, first)
	require.NoError(t, err)
	_ = resp.Body.Close()
	for _, done := range []<-chan struct{}{handlerDone, upstreamDone} {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("HTTP cancellation did not release handler/upstream")
		}
	}
}

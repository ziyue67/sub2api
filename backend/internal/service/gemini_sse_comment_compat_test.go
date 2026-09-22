package service

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type geminiSSEObserveWriter struct {
	gin.ResponseWriter
	firstDataOnce sync.Once
	keepaliveOnce sync.Once
	firstData     chan struct{}
	keepalive     chan struct{}
}

func newGeminiSSEObserveWriter(writer gin.ResponseWriter) *geminiSSEObserveWriter {
	return &geminiSSEObserveWriter{
		ResponseWriter: writer,
		firstData:      make(chan struct{}),
		keepalive:      make(chan struct{}),
	}
}

func (w *geminiSSEObserveWriter) observe(data string) {
	if strings.Contains(data, `"text":"partial"`) {
		w.firstDataOnce.Do(func() { close(w.firstData) })
	}
	if strings.Contains(data, ":\n\n") {
		w.keepaliveOnce.Do(func() { close(w.keepalive) })
	}
}

func (w *geminiSSEObserveWriter) Write(data []byte) (int, error) {
	n, err := w.ResponseWriter.Write(data)
	if n > 0 {
		w.observe(string(data[:n]))
	}
	return n, err
}

func (w *geminiSSEObserveWriter) WriteString(data string) (int, error) {
	n, err := w.ResponseWriter.WriteString(data)
	if n > 0 {
		w.observe(data[:n])
	}
	return n, err
}

func TestGeminiClientRejectsSSEComments(t *testing.T) {
	cases := []struct {
		name string
		hint string
		want bool
	}{
		{"go-genai (Antigravity CLI)", "google-genai-sdk/1.71.0 gl-go/go1.28-20260721-RC03 cl/951519500 +3ebc191975 X:fieldtrack,boringcrypto", true},
		{"python-genai", "google-genai-sdk/1.20.0 gl-python/3.12.4", true},
		{"js-genai tolerates comments", "google-genai-sdk/1.9.0 gl-node/22.3.0", false},
		{"gemini-cli", "GeminiCLI/0.60.0 (darwin; arm64)", false},
		{"curl", "curl/8.7.1", false},
		{"empty", "", false},
		{"case-insensitive", "Google-GenAI-SDK/1.0.0 GL-Go/go1.27", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, geminiClientRejectsSSEComments(tc.hint))
		})
	}
}

func TestDownstreamRejectsSSECommentsReadsBothHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := newAntigravityCompatContext(http.MethodPost, "/v1beta/models/gemini-3.8-flash:streamGenerateContent", nil)
	require.False(t, downstreamRejectsSSEComments(c))

	c.Request.Header.Set("X-Goog-Api-Client", "google-genai-sdk/1.71.0 gl-go/go1.28")
	require.True(t, downstreamRejectsSSEComments(c))

	c.Request.Header.Del("X-Goog-Api-Client")
	c.Request.Header.Set("User-Agent", "google-genai-sdk/1.71.0 gl-go/go1.28")
	require.True(t, downstreamRejectsSSEComments(c))

	require.False(t, downstreamRejectsSSEComments(nil))
}

// runAntigravityGeminiStreamWithIdle 起一条上游流：确认首个 data 事件已写到下游后，
// 再观察 idle 时长并关闭上游，避免 runner 调度延迟吞掉整个心跳观察窗口。
func runAntigravityGeminiStreamWithIdle(t *testing.T, userAgent string, idle time.Duration) (string, bool) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	svc := newAntigravityCompatService(
		config.GatewayConfig{MaxLineSize: defaultMaxLineSize, StreamKeepaliveInterval: 1},
		nil,
	)
	c, recorder := newAntigravityCompatContext(http.MethodPost, "/v1beta/models/gemini-3.8-flash:streamGenerateContent", nil)
	observer := newGeminiSSEObserveWriter(c.Writer)
	c.Writer = observer
	if userAgent != "" {
		c.Request.Header.Set("User-Agent", userAgent)
	}
	reader, writer := io.Pipe()
	defer func() { _ = reader.Close() }()
	defer func() { _ = writer.Close() }()
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: reader}
	done := make(chan error, 1)
	go func() {
		_, err := svc.handleGeminiStreamingResponse(c, resp, time.Now())
		done <- err
	}()
	_, err := io.WriteString(
		writer,
		`data: {"response":{"responseId":"resp_1","candidates":[{"content":{"parts":[{"text":"partial"}]}}],"usageMetadata":{"promptTokenCount":8,"candidatesTokenCount":1}}}`+"\n\n",
	)
	require.NoError(t, err)
	select {
	case <-observer.firstData:
	case <-time.After(5 * time.Second):
		require.FailNow(t, "timed out waiting for the first downstream data event")
	}

	sawKeepalive := false
	select {
	case <-observer.keepalive:
		sawKeepalive = true
	case <-time.After(idle):
	}
	require.NoError(t, writer.Close())
	select {
	case err = <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		require.FailNow(t, "timed out waiting for the stream handler to stop")
	}
	return recorder.Body.String(), sawKeepalive
}

func TestAntigravityGeminiStreamKeepsCommentKeepaliveForOrdinaryClients(t *testing.T) {
	out, sawKeepalive := runAntigravityGeminiStreamWithIdle(t, "curl/8.7.1", 2500*time.Millisecond)
	require.True(t, sawKeepalive, "ordinary clients should still get the idle keepalive")
	require.Contains(t, out, ":\n\n", "ordinary clients should still get the idle keepalive")
	require.Contains(t, out, `"text":"partial"`)
}

func TestAntigravityGeminiStreamSkipsCommentKeepaliveForGoGenai(t *testing.T) {
	out, sawKeepalive := runAntigravityGeminiStreamWithIdle(t, "google-genai-sdk/1.71.0 gl-go/go1.28-20260721-RC03", 2200*time.Millisecond)
	require.False(t, sawKeepalive, "go-genai must never receive an SSE comment event")
	require.Contains(t, out, `"text":"partial"`)
	for _, event := range strings.Split(out, "\n\n") {
		require.False(t, strings.HasPrefix(event, ":"), "go-genai must never receive an SSE comment event, got %q", event)
	}
}

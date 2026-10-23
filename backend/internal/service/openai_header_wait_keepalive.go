package service

import (
	"bytes"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const openAIHeaderWaitWriterKey = "openai_header_wait_writer"

// WrapOpenAIResponsesHeaderWait owns the downstream representation across account
// attempts. No timer runs here. Each physical header wait arms a bounded timer,
// and joins it before the request goroutine resumes touching the response.
func WrapOpenAIResponsesHeaderWait(c *gin.Context, enabled bool) func() {
	if !enabled || c == nil || c.Writer == nil || openAICompactClientWantsStream(c) {
		return func() {}
	}
	if _, ok := c.Get(openAIHeaderWaitWriterKey); ok {
		return func() {}
	}
	original := c.Writer
	w := &openAIHeaderWaitWriter{ResponseWriter: original, c: c}
	c.Set(openAIHeaderWaitWriterKey, w)
	c.Writer = w
	return func() {
		if c.Writer == w {
			c.Writer = original
		}
		c.Set(openAIHeaderWaitWriterKey, nil)
	}
}

type openAIHeaderWaitWriter struct {
	gin.ResponseWriter
	c            *gin.Context
	committed    bool
	errorStatus  int
	terminalSent bool
	writeErr     error
}

func (w *openAIHeaderWaitWriter) WriteHeader(status int) {
	if w.committed && status >= 400 {
		w.errorStatus = status
		return
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *openAIHeaderWaitWriter) Write(p []byte) (int, error) {
	if w.writeErr != nil {
		return 0, w.writeErr
	}
	if w.terminalSent {
		return len(p), nil
	}
	if w.committed && w.errorStatus >= 400 {
		return w.writeFailure(p)
	}
	n, err := w.ResponseWriter.Write(p)
	// Slot-wait and reader heartbeats may commit the same SSE before this
	// transport's timer fires. They need the same error representation guard.
	if (bytes.HasPrefix(p, []byte(": keepalive")) || bytes.Equal(p, []byte(":\n\n"))) && n > 0 && err == nil && w.Status() == http.StatusOK && strings.HasPrefix(w.Header().Get("Content-Type"), "text/event-stream") {
		w.committed = true
	}
	if err != nil {
		w.writeErr = err
	}
	return n, err
}
func (w *openAIHeaderWaitWriter) WriteString(s string) (int, error) { return w.Write([]byte(s)) }
func (w *openAIHeaderWaitWriter) Flush()                            { _ = w.FlushError() }
func (w *openAIHeaderWaitWriter) FlushError() error {
	if w.writeErr != nil {
		return w.writeErr
	}
	err := FlushGatewayResponse(w.ResponseWriter)
	if err != nil {
		w.writeErr = err
	}
	return err
}

// Gin's JSON/Data renderers write a complete error representation in one Write.
// Catch them after an early heartbeat without modifying upstream classification,
// request contents, account retry decisions or the pre-heartbeat HTTP boundary.
func (w *openAIHeaderWaitWriter) writeFailure(body []byte) (int, error) {
	message := sanitizeUpstreamErrorMessage(strings.TrimSpace(extractUpstreamErrorMessage(body)))
	if message == "" {
		message = "Upstream request failed"
	}
	errType := strings.TrimSpace(gjson.GetBytes(body, "error.type").String())
	if errType == "" {
		errType = "upstream_error"
	}
	code := strings.TrimSpace(extractUpstreamErrorCode(body))
	if code == "" {
		code = errType
	}
	source, _ := marshalOpenAIUpstreamJSON(gin.H{"error": gin.H{
		"type": errType, "code": code, "message": message,
		"param": gjson.GetBytes(body, "error.param").Value(),
	}})
	w.terminalSent = true
	MarkResponseCommitted(w.c)
	MarkOpsStreamFailure(w.c, errType, code, message, w.errorStatus)
	header := w.Header()
	header.Set("Content-Type", "text/event-stream")
	header.Del("Content-Length")
	header.Del("Content-Encoding")
	frame := buildOpenAIResponseFailedSSE("", "", time.Now().Unix(), source, message)
	if _, err := w.ResponseWriter.WriteString(frame); err != nil {
		w.writeErr = err
		return 0, err
	}
	if err := w.FlushError(); err != nil {
		return 0, err
	}
	return len(body), nil
}

// startOpenAIHeaderWaitKeepalive must wrap only the transport call: while it is
// armed, the request goroutine does not access Gin/writer state. Stop joins the
// existing guarded heartbeat writer before response/error processing resumes.
func (s *OpenAIGatewayService) startOpenAIHeaderWaitKeepalive(c *gin.Context, stream bool) func() {
	if !stream || c == nil || s == nil || s.cfg == nil || s.cfg.Gateway.StreamKeepaliveInterval <= 0 {
		return func() {}
	}
	raw, ok := c.Get(openAIHeaderWaitWriterKey)
	w, okWriter := raw.(*openAIHeaderWaitWriter)
	if !ok || !okWriter || w == nil || w.writeErr != nil || w.terminalSent || openAIStreamClientOutputStarted(c, false) {
		return func() {}
	}
	if existing, ok := c.Get(openAICompactSSEKeepaliveKey); ok && existing != nil {
		return func() {}
	}
	return startOpenAIHeaderWaitKeepaliveInterval(c, w, time.Duration(s.cfg.Gateway.StreamKeepaliveInterval)*time.Second)
}

func startOpenAIHeaderWaitKeepaliveInterval(c *gin.Context, w *openAIHeaderWaitWriter, interval time.Duration) func() {
	stop := startOpenAISSEKeepalive(c, interval)
	value, _ := c.Get(openAICompactSSEKeepaliveKey)
	k, _ := value.(*openAICompactSSEKeepalive)
	var once sync.Once
	return func() {
		once.Do(func() {
			stop()
			if k == nil {
				return
			}
			// Stop acquired k.mu: all heartbeat writes happened-before this handoff.
			k.mu.Lock()
			n, started := k.bytes, k.started
			k.mu.Unlock()
			recordOpenAIStreamKeepaliveBytes(c, n)
			if started && w.Status() == http.StatusOK && strings.HasPrefix(w.Header().Get("Content-Type"), "text/event-stream") {
				w.committed = true
			}
			c.Set(openAICompactSSEKeepaliveKey, nil)
		})
	}
}

// OpenAIHeaderWaitDisconnected prevents new attempts after a failed downstream
// write/flush, while the already sent upstream request can still settle usage.
func OpenAIHeaderWaitDisconnected(c *gin.Context) bool {
	if c == nil {
		return false
	}
	value, _ := c.Get(openAIHeaderWaitWriterKey)
	w, _ := value.(*openAIHeaderWaitWriter)
	return w != nil && w.writeErr != nil
}

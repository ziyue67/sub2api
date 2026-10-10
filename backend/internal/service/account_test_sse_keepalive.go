package service

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// startAccountTestSSEKeepalive 在首包等待和正文静默期间保活；注释不构成模型输出。
// 复用已有心跳器的互斥锁，但业务事件后仍继续保活，退出时等待协程释放 writer。
func startAccountTestSSEKeepalive(c *gin.Context, interval time.Duration) func() {
	originalWriter := c.Writer
	originalContext := c.Request.Context()
	ctx, cancel := context.WithCancel(originalContext)
	c.Request = c.Request.WithContext(ctx)
	k := &openAICompactSSEKeepalive{writer: originalWriter, stop: make(chan struct{})}
	w := &accountTestKeepaliveWriter{
		openAICompactKeepaliveWriter: &openAICompactKeepaliveWriter{ResponseWriter: originalWriter, k: k},
		ctx:                          ctx, cancel: cancel,
	}
	c.Writer = w
	requestPath := "background"
	if c.Request.URL != nil {
		requestPath = c.Request.URL.Path
	}
	// 立即提交真实的 SSE 字节，让代理在上游尚未返回 HTTP 响应时也能看到连接已建立。
	if ctx.Err() == nil && !k.beat() {
		log.Printf("[WARN] account test SSE initial write failed path=%s; canceling upstream", requestPath)
		cancel()
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-k.stop:
				return
			case <-ticker.C:
				if ctx.Err() != nil {
					return
				}
				if !k.beat() {
					log.Printf("[WARN] account test SSE keepalive write failed path=%s; canceling upstream", requestPath)
					cancel()
					return
				}
			}
		}
	}()
	return func() {
		k.Stop()
		cancel()
		<-done
		c.Writer = originalWriter
		// 成功测试后的限流恢复仍需使用调用方的有效上下文。
		c.Request = c.Request.WithContext(originalContext)
	}
}

type accountTestKeepaliveWriter struct {
	*openAICompactKeepaliveWriter
	ctx    context.Context
	cancel context.CancelFunc
}

func (w *accountTestKeepaliveWriter) Header() http.Header {
	w.k.mu.Lock()
	w.k.paused = true
	w.k.mu.Unlock()
	return w.ResponseWriter.Header()
}

func (w *accountTestKeepaliveWriter) Write(data []byte) (int, error) {
	w.k.mu.Lock()
	defer w.k.mu.Unlock()
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	w.k.paused = false
	n, err := w.ResponseWriter.Write(data)
	if err != nil {
		w.cancel()
	}
	return n, err
}

func (w *accountTestKeepaliveWriter) WriteString(value string) (int, error) {
	return w.Write([]byte(value))
}

func (w *accountTestKeepaliveWriter) WriteHeader(code int) {
	w.k.mu.Lock()
	defer w.k.mu.Unlock()
	w.ResponseWriter.WriteHeader(code)
}

func (w *accountTestKeepaliveWriter) WriteHeaderNow() {
	w.k.mu.Lock()
	defer w.k.mu.Unlock()
	w.ResponseWriter.WriteHeaderNow()
}

func (w *accountTestKeepaliveWriter) Flush() {
	w.k.mu.Lock()
	defer w.k.mu.Unlock()
	if w.ctx.Err() == nil {
		w.k.paused = false
		w.ResponseWriter.Flush()
	}
}

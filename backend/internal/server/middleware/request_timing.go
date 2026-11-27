package middleware

import (
	"crypto/tls"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/requesttiming"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// ConnectionTraceRecorder persists metadata independently of billing.
type ConnectionTraceRecorder interface {
	RecordConnectionTrace(service.ConnectionTrace)
}

// RequestTiming runs before authentication and body pre-read. WebSocket turns
// have a different lifetime and must not be labelled as an HTTP request trace.
func RequestTiming(recorders ...ConnectionTraceRecorder) gin.HandlerFunc {
	return func(c *gin.Context) {
		path := strings.TrimRight(c.Request.URL.Path, "/")
		if c.Request.Method != http.MethodPost || (!strings.HasSuffix(path, "/responses") && !strings.HasSuffix(path, "/responses/compact")) {
			c.Next()
			return
		}
		collector := requesttiming.New(time.Now(), c.Request.ContentLength)
		c.Request = c.Request.WithContext(requesttiming.With(c.Request.Context(), collector))
		c.Request.Body = collector.WrapBody(c.Request.Body)
		writer := &timingWriter{ResponseWriter: c.Writer, collector: collector}
		c.Writer = writer
		defer func() {
			// Copy bounded metadata even before authentication. Never retain headers,
			// bodies, credential values or gin.Context in the async writer.
			if len(recorders) > 0 && recorders[0] != nil {
				requestID, _ := c.Request.Context().Value(ctxkey.RequestID).(string)
				clientID, _ := c.Request.Context().Value(ctxkey.ClientRequestID).(string)
				accountID, _ := c.Request.Context().Value(ctxkey.AccountID).(int64)
				record := service.ConnectionTrace{RequestID: requestID, ClientRequestID: clientID, AccountID: accountID, Model: connectionMetadata(c.GetString("ops_model"), 256), Path: service.ConnectionPath(path), ClientIP: connectionMetadata(SecurityClientIP(c), 64), UserAgent: connectionMetadata(c.GetHeader("User-Agent"), 512), Stream: c.GetBool("ops_stream")}

				record.Source = "application"
				record.Host = connectionMetadata(c.Request.Host, 255)
				record.Method = connectionMetadata(c.Request.Method, 16)
				record.HTTPVersion = connectionMetadata(c.Request.Proto, 16)
				record.Scheme = "http"
				if c.Request.TLS != nil {
					record.Scheme = "https"
					record.TLSVersion = tls.VersionName(c.Request.TLS.Version)
				}
				if host, _, err := net.SplitHostPort(c.Request.RemoteAddr); err == nil {
					if peer := net.ParseIP(host); peer != nil && peer.IsLoopback() {
						if proto := c.GetHeader("X-Forwarded-Proto"); proto == "https" || proto == "http" {
							record.Scheme = proto
						}
					}
				}
				// Diagnostic attribution is independent of authentication. The
				// fallback is populated only by a successful trusted key lookup.
				key, authenticated := GetAPIKeyFromContext(c)
				identitySource := ""
				if authenticated {
					identitySource = "authenticated"
				} else {
					key, _ = GetOpsFallbackAPIKey(c)
					if key != nil {
						identitySource = "recognized_credential"
					}
				}
				if key != nil {
					record.UserID = key.UserID
					if record.UserID == 0 && key.User != nil {
						record.UserID = key.User.ID
					}
					record.APIKeyID = key.ID
					if key.GroupID != nil {
						record.GroupID = *key.GroupID
					}
				}
				rejection := ""
				if reason, ok := GetIngressRejectReason(c); ok {
					switch reason {
					case IngressRejectUserInactive:
						rejection = "USER_INACTIVE"
					case IngressRejectAPIKeyDisabled:
						rejection = "API_KEY_DISABLED"
					case IngressRejectAPIKeyRequired:
						rejection = "API_KEY_REQUIRED"
					case IngressRejectInvalidAPIKey:
						rejection = "INVALID_API_KEY"
					case IngressRejectInvalidAuthRateLimited:
						rejection = "INVALID_AUTH_RATE_LIMITED"
					}
				}
				collector.WhenFinished(func(detail requesttiming.Snapshot) {
					record.TraceID = detail.TraceID
					record.StartedAt = detail.StartedAt
					detail.IdentitySource = identitySource
					detail.AuthRejection = rejection
					record.Detail = detail
					recorders[0].RecordConnectionTrace(record)
				})
			}
			collector.Finish(writer.Status(), c.Request.Context().Err() != nil)
		}()
		c.Next()
	}
}

type timingWriter struct {
	gin.ResponseWriter
	collector *requesttiming.Collector
}

func (w *timingWriter) Write(p []byte) (int, error) {
	start := time.Now()
	n, err := w.ResponseWriter.Write(p)
	w.collector.Written(start, n, err, false)
	return n, err
}
func (w *timingWriter) WriteString(s string) (int, error) {
	start := time.Now()
	n, err := w.ResponseWriter.WriteString(s)
	w.collector.Written(start, n, err, false)
	return n, err
}
func (w *timingWriter) Flush() { _ = w.FlushError() }
func (w *timingWriter) FlushError() error {
	start := time.Now()
	err := service.FlushGatewayResponse(w.ResponseWriter)
	w.collector.Written(start, 0, err, err == nil)
	return err
}
func (w *timingWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// Bound untrusted display metadata by bytes without producing invalid UTF-8.
func connectionMetadata(value string, limit int) string {
	value = strings.ToValidUTF8(value, "")
	if len(value) > limit {
		value = strings.ToValidUTF8(value[:limit], "")
	}
	return value
}

package middleware

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/requesttiming"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func TestRequestTimingPreReadAndHandlerRead(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RequestTiming())
	var got requesttiming.Snapshot
	r.Use(func(c *gin.Context) {
		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			t.Fatal(err)
		}
		c.Request.Body = io.NopCloser(strings.NewReader(string(body)))
		c.Next()
	})
	r.POST("/v1/responses", func(c *gin.Context) {
		_, _ = io.ReadAll(c.Request.Body)
		requesttiming.From(c.Request.Context()).WhenFinished(func(s requesttiming.Snapshot) { got = s })
		c.String(200, "ok")
		c.Writer.Flush()
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader("abc")))
	if got.BodyBytes != 3 || !got.BodyComplete || got.DownstreamBytes != 2 || got.Status != 200 {
		t.Fatalf("bad trace %+v", got)
	}
}

type timingFlushErrorRecorder struct {
	*httptest.ResponseRecorder
	err error
}

func (w *timingFlushErrorRecorder) FlushError() error { return w.err }

func TestRequestTimingRecordsTransportFlushFailureThroughAdmissionGuard(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RequestTiming(), ServerTiming(true))
	var snapshot requesttiming.Snapshot
	want := errors.New("transport flush failed")
	r.POST("/v1/responses", func(c *gin.Context) {
		requesttiming.From(c.Request.Context()).WhenFinished(func(s requesttiming.Snapshot) { snapshot = s })
		c.Writer = &apiKeyAdmissionWriter{ResponseWriter: c.Writer, ctx: c.Request.Context()}
		_, err := c.Writer.WriteString("data: test\n\n")
		if err != nil {
			t.Fatal(err)
		}
		if err := service.FlushGatewayResponse(c.Writer); !errors.Is(err, want) {
			t.Fatalf("flush error = %v, want %v", err, want)
		}
	})
	recorder := &timingFlushErrorRecorder{ResponseRecorder: httptest.NewRecorder(), err: want}
	r.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/responses", nil))
	if !snapshot.DownstreamError {
		t.Fatal("flush failure must be visible in request timing")
	}
	if _, ok := snapshot.Events["first_flush"]; ok {
		t.Fatal("failed flush recorded as successful")
	}
}

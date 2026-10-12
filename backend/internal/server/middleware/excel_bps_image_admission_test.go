package middleware

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type bpsImageUnreadBody struct{ read bool }

func (b *bpsImageUnreadBody) Read([]byte) (int, error) { b.read = true; return 0, io.EOF }
func (*bpsImageUnreadBody) Close() error               { return nil }

// The middleware only marks client requests; the budget is reserved after
// account selection, so it never reads or rejects a request itself.
func TestExcelBPSImageAdmissionMarksClientRequestsWithoutReadingBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(ExcelBPSImageAdmission())
	var marked bool
	next := func(c *gin.Context) {
		marked = service.IsExcelBPSImageAdmissionMarked(c)
		c.Status(http.StatusNoContent)
	}
	for _, path := range []string{"/responses", "/v1/responses", "/backend-api/codex/responses", "/chat/completions", "/v1/chat/completions", "/v1/messages"} {
		r.POST(path, next)
		r.GET(path, next)
	}
	r.POST("/v1/responses/*subpath", next)
	r.POST("/v1/images/generations", next)

	for _, tt := range []struct {
		method, path string
		want         bool
	}{
		{http.MethodPost, "/responses", true},
		{http.MethodPost, "/v1/responses", true},
		{http.MethodPost, "/v1/responses/compact", true},
		{http.MethodPost, "/backend-api/codex/responses", true},
		{http.MethodPost, "/chat/completions", true},
		{http.MethodPost, "/v1/chat/completions", true},
		{http.MethodPost, "/v1/messages", true},
		{http.MethodGet, "/v1/responses", false},
		{http.MethodPost, "/v1/images/generations", false},
	} {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			body := &bpsImageUnreadBody{}
			req := httptest.NewRequest(tt.method, tt.path, nil)
			req.Body = body
			req.ContentLength = 2 << 30
			w := httptest.NewRecorder()
			marked = false

			r.ServeHTTP(w, req)

			require.Equal(t, http.StatusNoContent, w.Code)
			require.Equal(t, tt.want, marked)
			require.False(t, body.read, "the marker must not read the body")
		})
	}
}

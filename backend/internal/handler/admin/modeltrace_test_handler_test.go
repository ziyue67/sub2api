//go:build unit

package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestModelTraceRequestValidationBeforeService(t *testing.T) {
	for _, input := range []struct{ id, body string }{
		{"0", `{"model_id":"gpt-6-astra"}`},
		{"bad", `{"model_id":"gpt-6-astra"}`},
		{"42", `{`},
		{"42", `{"model_id":" "}`},
		{"42", `{"model_id":"` + strings.Repeat("x", 101) + `"}`},
		{"42", `{"model_id":"gpt-6-astra","reasoning_effort":"invalid"}`},
	} {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Params = gin.Params{{Key: "id", Value: input.id}}
		c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(input.body))
		c.Request.Header.Set("Content-Type", "application/json")
		(&AccountHandler{}).ModelTraceTest(c)
		require.Equal(t, http.StatusBadRequest, rec.Code)
		require.NotContains(t, rec.Header().Get("Content-Type"), "text/event-stream")
	}
}

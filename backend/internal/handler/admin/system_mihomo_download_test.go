package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/mihomo"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMihomoDownloadModeContract(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       int
	}{
		{"direct", "{\"mode\":\"direct\"}", http.StatusOK},
		{"proxy", "{\"mode\":\"proxy\"}", http.StatusOK},
		{"auto", "{\"mode\":\"auto\"}", http.StatusOK},
		{"missing", "{}", http.StatusBadRequest},
		{"invalid", "{\"mode\":\"private-token\"}", http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			kernel := mihomo.New(t.TempDir())
			t.Cleanup(kernel.Close)
			h := &SystemHandler{kernel: kernel}
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPut, "/api/v1/admin/system/mihomo/download-mode", strings.NewReader(tc.body))
			c.Request.Header.Set("Content-Type", "application/json")
			h.SetMihomoDownloadMode(c)
			require.Equal(t, tc.want, rec.Code)
			require.NotContains(t, rec.Body.String(), "private-token")
			if tc.want == http.StatusOK {
				require.Equal(t, mihomo.SubscriptionDownloadMode(tc.name), kernel.Status().DownloadMode)
				require.Contains(t, rec.Body.String(), "subscription_download_mode")
			}
		})
	}
}

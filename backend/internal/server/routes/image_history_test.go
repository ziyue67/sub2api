package routes

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestImageHistoryGatewayRoutesAuthAndAdmission(t *testing.T) {
	for _, mode := range []string{config.RunModeStandard, config.RunModeSimple} {
		t.Run(mode, func(t *testing.T) {
			router, rates, key := newKeyBillingRouteTestRouter(mode)
			for _, path := range []string{"/v1/images/history", "/v1/images/history/00000000-0000-4000-8000-000000000000", "/v1/images/history/00000000-0000-4000-8000-000000000000/files/1"} {
				for _, auth := range []bool{false, true} {
					req := httptest.NewRequest(http.MethodGet, path, nil)
					if auth {
						req.Header.Set("Authorization", "Bearer "+key)
					}
					w := httptest.NewRecorder()
					router.ServeHTTP(w, req)
					want := http.StatusUnauthorized
					// Nil archive means the request reached the history handler. In simple
					// mode the key is ungrouped: generation admission must not intercept it.
					if auth {
						want = http.StatusServiceUnavailable
					}
					require.Equal(t, want, w.Code, w.Body.String())
					require.Contains(t, w.Header().Get("Content-Type"), "application/json")
					require.Zero(t, rates.lookupCalls)
				}
			}
		})
	}
}

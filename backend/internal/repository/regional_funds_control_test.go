package repository

import (
	"github.com/Wei-Shaw/sub2api/internal/regional/control"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRegionalFundsControlPreservesAccountAndSlotRoutes(t *testing.T) {
	c := &regionalConcurrencyCache{}
	mux := http.NewServeMux()
	for _, path := range []string{"/accounts/rpm", "/accounts/state", "/slots/issue"} {
		mux.HandleFunc(control.Prefix+path, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("owner", "original")
			w.WriteHeader(http.StatusAccepted)
		})
	}
	var funds http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("owner", "funds")
		w.WriteHeader(http.StatusNoContent)
	})
	c.fundsHandler.Store(&funds)
	server := httptest.NewServer(c.withFundsControl(mux))
	defer server.Close()
	for _, path := range []string{"/accounts/rpm", "/accounts/state", "/slots/issue", "/quota/issue", "/quota/close"} {
		resp, err := http.Post(server.URL+control.Prefix+path, "application/json", nil)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		expected := "original"
		status := http.StatusAccepted
		if path == "/quota/issue" || path == "/quota/close" {
			expected = "funds"
			status = http.StatusNoContent
		}
		require.Equal(t, expected, resp.Header.Get("owner"))
		require.Equal(t, status, resp.StatusCode)
	}
}

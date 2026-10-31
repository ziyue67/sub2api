//go:build integration

package repository

import (
	"net/http"
	"testing"
	"time"

	"github.com/imroc/req/v3"
)

// External integration tests replace only the final network boundary. The real
// OAuth adapter, verifier, DTO, service, repository and isolated DB all execute.
func Issue594InstallOfflineOAuthTransport(t *testing.T, handler func(*http.Request) (*http.Response, error)) {
	t.Helper()
	client := req.C()
	client.GetTransport().WrapRoundTripFunc(func(_ http.RoundTripper) req.HttpRoundTripFunc { return handler })
	key := buildReqClientKey(reqClientOptions{Timeout: 120 * time.Second})
	previous, existed := sharedReqClients.Load(key)
	sharedReqClients.Store(key, client)
	t.Cleanup(func() {
		if existed {
			sharedReqClients.Store(key, previous)
		} else {
			sharedReqClients.Delete(key)
		}
	})
}

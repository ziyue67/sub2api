//go:build integration

package service

import "net/http"

// Test-only bridge to the actual final transport. No alternate request builder,
// fence or HTTP implementation is substituted by this bridge.
func Issue594SendProductionUpstream(s *OpenAIGatewayService, r *http.Request, a *Account) (*http.Response, error) {
	return s.doOpenAIUpstream(r, "", a)
}

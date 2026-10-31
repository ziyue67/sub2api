//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// Uses the production credential dispatch, not a substitute connection router.
func TestIssue594UnknownConnectionCannotFallBackToCodex(t *testing.T) {
	for _, kind := range []string{"unknown", "swic", "siwc_identity"} {
		t.Run(kind, func(t *testing.T) {
			account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
				Credentials: map[string]any{"access_token": "synthetic-unrelated-codex-token"},
				Extra:       map[string]any{"openai_connection_kind": kind}}
			token, _, err := (&OpenAIGatewayService{}).GetAccessToken(context.Background(), account)
			require.Error(t, err, "unknown or identity-only connections must never inherit Codex authorization")
			require.Empty(t, token)
		})
	}
}

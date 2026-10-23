package service

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestConnectionPathDropsCredentialsAndCapabilities(t *testing.T) {
	for _, pair := range [][2]string{
		{"/v1/responses?api_key=secret#token", "/v1/responses"},
		{"/api/bps-images/short-token?signature=secret", "/api/[redacted]/[redacted]"},
		{"/v1/responses/compact", "/v1/responses/compact"},
		{"/private-auth-token/v1/responses", "/[redacted]/v1/responses"},
	} {
		require.Equal(t, pair[1], ConnectionPath(pair[0]))
	}
}

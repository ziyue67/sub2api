package middleware

import (
	"github.com/stretchr/testify/require"
	"net/http"
	"testing"
)

func TestImageHistoryReadPaths(t *testing.T) {
	id := "00000000-0000-4000-8000-000000000000"
	for _, path := range []string{"/v1/images/history", "/v1/images/history/" + id, "/v1/images/history/" + id + "/files/1"} {
		require.True(t, isImageHistoryRead(http.MethodGet, path), path)
		require.False(t, isImageHistoryRead(http.MethodPost, path), path)
		require.False(t, isImageHistoryRead(http.MethodDelete, path), path)
	}
	for _, path := range []string{"/v1/images/generations", "/v1/images/historyevil", "/v1/images/history/../../etc", "/v1/images/history/" + id + "/anything/1", "/v1/images/history/" + id + "/files/1/more"} {
		require.False(t, isImageHistoryRead(http.MethodGet, path), path)
	}
}

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A caller may have observed a miss just before another refresh finishes. Once
// the first flight has completed, the late caller starts a new flight. It must
// consume the now-fresh entry rather than repeat the upstream request.
func TestOpenAIModelsSingleflightRechecksCompletedRefresh(t *testing.T) {
	svc := &OpenAIGatewayService{}
	const key = "late-cold-caller"
	manifest := &OpenAIModelsResponse{Body: []byte(`{"models":[]}`)}
	svc.openAIModelsCache.set(key, manifest, time.Now())
	calls := 0
	result := <-svc.refreshCachedOpenAIModels(key, openAIModelsRequest{}, func(context.Context, string) (*OpenAIModelsResponse, error) {
		calls++
		return manifest, nil
	})
	require.NoError(t, result.Err)
	require.Equal(t, 0, calls, "a fresh cache entry must avoid a second upstream request")
	require.NotNil(t, result.Val)
}

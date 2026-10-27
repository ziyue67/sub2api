//go:build unit

package handler

import (
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func TestPriorityObservationMappedBodyNormalization(t *testing.T) {
	body := []byte(`{"model":"public-alias","stream":true}`)
	replace := func(b []byte, m string) []byte {
		out, err := sjson.SetBytes(b, "model", m)
		require.NoError(t, err)
		return out
	}
	got := openAIModelMappedBody(body, true, " route-model ", replace)
	require.Equal(t, "route-model", gjson.GetBytes(got, "model").String())
	require.Equal(t, "route-model", openAIChannelForwardModel(service.ChannelMappingResult{Mapped: true, MappedModel: " route-model "}, "public-alias"))
	require.Equal(t, body, openAIModelMappedBody(body, true, "  ", replace))
}

func TestPriorityObservationCompositePublicIdentity(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	// This is the same context contract used by composite middleware.
	c.Request = c.Request.WithContext(service.WithCompositeRouteDecision(c.Request.Context(), service.CompositeRouteDecision{Matched: true, PublicModel: "public-composite", UpstreamModel: "composite-destination", TargetPlatform: service.PlatformOpenAI}))
	mapping := service.ChannelMappingResult{Mapped: true, MappedModel: "route-model"}
	fields := clientRequestedUsageFields(c, mapping, "composite-destination", "account-upstream")
	require.Equal(t, "public-composite", fields.OriginalModel)
	bindOpenAIPriorityObservation(c, nil, "composite-destination")
	require.Equal(t, "/v1/responses", GetInboundEndpoint(c))
}

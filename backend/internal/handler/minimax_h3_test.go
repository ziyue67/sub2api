//go:build unit

package handler

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestMiniMaxH3GroupGateAndLookupOwnership(t *testing.T) {
	for _, scenario := range []string{"disabled", "wrong duration", "accepted", "upstream unavailable", "other key", "other user", "lookup owned", "wrong content type"} {
		t.Run(scenario, func(t *testing.T) {
			h, slots, bindings, upstream := newGrokMediaSlotHandler(t, false, false, service.PlatformMiniMax)
			c, w := grokMediaSlotContext(context.Background(), true)
			key, _ := middleware.GetAPIKeyFromContext(c)
			key.Group.Platform = service.PlatformMiniMax
			key.Group.AllowImageGeneration = false // video has its own switch
			key.Group.VideoModelPrices = map[string]map[string]float64{service.MiniMaxH3PriceFamily: {"request_1s": 0.02}}
			key.Group.RateMultiplier = 0.1
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", strings.NewReader(`{"model":"MiniMax-H3","prompt":"waves","seconds":"1"}`))
			c.Request.Header.Set("Content-Type", "application/json")
			upstream.call = func(req *http.Request, _ int64) (*http.Response, error) {
				if scenario == "upstream unavailable" {
					return nil, errors.New("network unavailable")
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"id":"task","status":"queued"}`))}, nil
			}
			want, calls := http.StatusOK, 1
			switch scenario {
			case "disabled":
				key.Group.VideoModelPrices = nil
				want, calls = http.StatusBadRequest, 0
			case "wrong duration":
				key.Group.VideoModelPrices[service.MiniMaxH3PriceFamily] = map[string]float64{"request_5s": 0.08}
				want, calls = http.StatusBadRequest, 0
			case "wrong content type":
				c.Request.Header.Set("Content-Type", "multipart/form-data")
				want, calls = http.StatusUnsupportedMediaType, 0
			case "upstream unavailable":
				want = http.StatusBadGateway
			case "other key", "other user", "lookup owned":
				require.NoError(t, h.gatewayService.BindGrokMediaVideoRequestAccount(context.Background(), key.GroupID, service.MiniMaxH3TaskKey("task"), 10, 20, 1))
				c.Request = httptest.NewRequest(http.MethodGet, "/v1/videos/task", nil)
				if scenario == "other key" {
					key.ID++
				}
				if scenario == "other user" {
					c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 11, Concurrency: 5})
				}
				if scenario != "lookup owned" {
					want, calls = http.StatusNotFound, 0
				}
			}
			h.MiniMaxH3Videos(c)
			require.Equal(t, want, w.Code, w.Body.String())
			require.Equal(t, calls, upstream.calls, "POST must not retry on ambiguous upstream errors")
			slots.assertReleased(t)
			if scenario == "accepted" {
				pending, err := h.gatewayService.LoadGrokVideoPendingBilling(context.Background(), service.MiniMaxH3TaskKey("task"), 10, 20)
				require.NoError(t, err)
				require.NotNil(t, pending)
				require.InDelta(t, 0.02, *pending.MiniMaxH3BaseCost, 1e-9)
				require.Equal(t, 1, pending.VideoDurationSeconds)
				require.Greater(t, bindings.writes, 0)
			}
		})
	}
}

func TestMiniMaxH3CompletionBillsRequestedSecondsOnce(t *testing.T) {
	h, _, _, _ := newGrokMediaSlotHandler(t, false, false, service.PlatformMiniMax)
	c, _ := grokMediaSlotContext(context.Background(), false)
	key, _ := middleware.GetAPIKeyFromContext(c)
	subject, _ := middleware.GetAuthSubjectFromContext(c)
	taskID := service.MiniMaxH3TaskKey("task")
	base := 0.02
	pending := service.GrokVideoPendingBilling{
		Model: "video-alias", BillingModel: "video-alias", UpstreamModel: "MiniMax-H3",
		VideoResolution: "768p", VideoDurationSeconds: 1, MiniMaxH3BaseCost: &base,
	}
	require.NoError(t, h.gatewayService.StoreGrokVideoPendingBilling(context.Background(), taskID, subject.UserID, key.ID, pending))
	loaded, err := h.gatewayService.LoadGrokVideoPendingBilling(context.Background(), taskID, subject.UserID, key.ID)
	require.NoError(t, err)
	require.Equal(t, "768p", loaded.VideoResolution)
	queued := &service.OpenAIForwardResult{ResponseID: taskID}
	require.Nil(t, prepareMiniMaxH3CompletionBilling(context.Background(), h, key, subject, taskID, queued))
	completed := &service.OpenAIForwardResult{ResponseID: taskID, VideoCount: 1, VideoDurationSeconds: 15}
	foreign := subject
	foreign.UserID++
	require.Nil(t, prepareMiniMaxH3CompletionBilling(context.Background(), h, key, foreign, taskID, completed))
	result := prepareMiniMaxH3CompletionBilling(context.Background(), h, key, subject, taskID, completed)
	require.NotNil(t, result)
	require.Equal(t, 1, result.VideoDurationSeconds, "bill requested seconds, not upstream response or MP4 length")
	require.Equal(t, "768p", result.VideoResolution)
	require.Equal(t, base, *result.MiniMaxH3BaseCost)
	require.Equal(t, "video-alias", result.BillingModel)
	require.Nil(t, prepareMiniMaxH3CompletionBilling(context.Background(), h, key, subject, taskID, completed))
	require.NotEqual(t, service.StableGrokVideoBillingRequestID("task"), result.RequestID)
}

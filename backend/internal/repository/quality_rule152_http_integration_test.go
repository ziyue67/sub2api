//go:build integration

package repository_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestQuality152HTTPUpdatePreservesServerIsolation(t *testing.T) {
	marker := func(stamp string) map[string]any {
		return map[string]any{"sample-model": map[string]any{"reason": "quality_rule:7", "isolated_at": stamp}}
	}
	for _, tc := range []struct {
		name          string
		present, omit bool
	}{{"same-owner-new-time", true, false}, {"omitted-client-value", true, true}, {"restored-server-must-not-resurrect", false, false}} {
		t.Run(tc.name, func(t *testing.T) {
			var current any
			if tc.present {
				current = marker("2026-01-01T00:00:02.222222222Z")
			}
			svc, repo, account := repository.Quality152AdminFixture(t, current)
			// No test/probe service or real credentials: Update cannot launch a probe.
			handler := admin.NewAccountHandler(svc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
			gin.SetMode(gin.TestMode)
			router := gin.New()
			router.PUT("/accounts/:id", handler.Update)
			extra := map[string]any{"form_setting": "http-saved"}
			if !tc.omit {
				extra[service.QualityModelIsolationKey] = marker("2026-01-01T00:00:01.111111111Z")
			}
			response := qualityLiveRequest(router, "PUT", fmt.Sprintf("/accounts/%d", account.ID), map[string]any{"name": "quality152-http-edited", "extra": extra})
			require.Equal(t, 200, response.Code, response.Body.String())
			got, err := repo.GetByID(context.Background(), account.ID)
			require.NoError(t, err)
			require.Equal(t, "quality152-http-edited", got.Name)
			require.Equal(t, "http-saved", got.Extra["form_setting"])
			if current == nil {
				require.NotContains(t, got.Extra, service.QualityModelIsolationKey)
			} else {
				expected, _ := json.Marshal(current)
				actual, _ := json.Marshal(got.Extra[service.QualityModelIsolationKey])
				require.JSONEq(t, string(expected), string(actual))
			}
			require.Equal(t, service.StatusActive, got.Status)
			require.True(t, got.Schedulable)
			require.Equal(t, 20, got.Concurrency)
		})
	}
}

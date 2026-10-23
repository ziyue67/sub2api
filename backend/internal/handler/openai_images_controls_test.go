package handler

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIGatewayHandlerImages_RejectsBeforeScheduling(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		model       string
		allowImages bool
		status      int
		errorType   string
	}{
		{"gpt-image-2", false, http.StatusForbidden, "permission_error"},
		{"nai-diffusion-4-5-full", false, http.StatusForbidden, "permission_error"},
		{"nai-diffusion-4-5-full", true, http.StatusBadRequest, "invalid_request_error"},
	} {
		t.Run(tc.model+tc.errorType, func(t *testing.T) {
			body := []byte(`{"model":"` + tc.model + `","prompt":"draw","size":"1024x1024"}`)
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")
			groupID := int64(111)
			c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
				ID: 222, GroupID: &groupID,
				Group: &service.Group{ID: groupID, AllowImageGeneration: tc.allowImages},
				User:  &service.User{ID: 333},
			})
			c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 333, Concurrency: 1})
			h := &OpenAIGatewayHandler{
				gatewayService:      &service.OpenAIGatewayService{},
				billingCacheService: &service.BillingCacheService{},
				apiKeyService:       &service.APIKeyService{},
				concurrencyHelper:   &ConcurrencyHelper{concurrencyService: &service.ConcurrencyService{}},
			}
			h.Images(c)
			require.Equal(t, tc.status, rec.Code)
			require.Equal(t, tc.errorType, gjson.GetBytes(rec.Body.Bytes(), "error.type").String())
			if tc.allowImages {
				require.Contains(t, rec.Body.String(), "images endpoint requires an image model")
			} else {
				require.Contains(t, rec.Body.String(), service.ImageGenerationPermissionMessage())
			}
		})
	}
}

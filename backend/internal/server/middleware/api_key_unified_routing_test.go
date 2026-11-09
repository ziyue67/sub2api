//go:build unit

package middleware

import (
	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http/httptest"
	"testing"
)

func TestUnifiedRoutingMetadataNeverUsesDisabledPrimary(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		key := &service.APIKey{GroupRouting: &domain.APIKeyGroupRouting{Strategy: service.APIKeyRoutingLowestRate, Routes: []domain.APIKeyGroupRoute{{GroupID: 1, Enabled: enabled}}}}
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("GET", "/v1/models", nil)
		require.True(t, isMultiGroupModelListing(c, key), "one or zero enabled candidates must use entitlement listing, not legacy primary/capacity")
		c.Request = httptest.NewRequest("GET", "/v1/sub2api/billing", nil)
		require.True(t, isMultiGroupBillingInfoRequest(c, key), "billing declaration must enumerate canonical enabled candidates")
	}
}

package handler

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// switchToNextAPIKeyRoute advances a single request to the next configured
// group route. Selection exhaustion may also use this path, but only retryable
// upstream failures start a route cooldown.
func switchToNextAPIKeyRoute(
	c *gin.Context,
	apiKeyService *service.APIKeyService,
	apiKey *service.APIKey,
	attempted map[int64]struct{},
	markCurrentRouteFailed bool,
) (bool, error) {
	if c == nil || apiKeyService == nil || apiKey == nil || len(apiKey.GroupRoutes) == 0 {
		return false, nil
	}
	if apiKey.GroupID != nil {
		attempted[*apiKey.GroupID] = struct{}{}
		if markCurrentRouteFailed {
			apiKeyService.MarkGroupRouteFailed(apiKey, *apiKey.GroupID)
		}
	}

	switched, err := apiKeyService.ApplyNextGroupRoute(c.Request.Context(), apiKey, attempted)
	if err != nil || !switched || apiKey.Group == nil {
		return switched, err
	}
	// Keep handler, service and Ops readers on the same effective group.
	c.Set(string(middleware.ContextKeyAPIKey), apiKey)
	middleware.SetOpsFallbackAPIKey(c, apiKey)
	ctx := context.WithValue(c.Request.Context(), ctxkey.Group, apiKey.Group)
	c.Request = c.Request.WithContext(ctx)
	return true, nil
}

// switchToGrokMediaGroupRoute advances through the API key's configured
// routes until it finds a Grok platform group. Media endpoints can have an
// OpenAI image group as the primary route, so the generic route failover must
// be platform-aware before account scheduling begins.
func switchToGrokMediaGroupRoute(
	c *gin.Context,
	apiKeyService *service.APIKeyService,
	apiKey *service.APIKey,
	attempted map[int64]struct{},
) (bool, error) {
	if apiKey == nil || apiKeyService == nil {
		return false, nil
	}
	for {
		switched, err := switchToNextAPIKeyRoute(c, apiKeyService, apiKey, attempted, false)
		if err != nil || !switched {
			return switched, err
		}
		if apiKey.Group != nil && apiKey.Group.Platform == service.PlatformGrok {
			return true, nil
		}
		// The just-selected non-Grok route must not be selected again while
		// searching for the next candidate.
		if apiKey.GroupID != nil {
			attempted[*apiKey.GroupID] = struct{}{}
		}
	}
}

func selectedGroupRouteSubscription(c *gin.Context, apiKeyService *service.APIKeyService, apiKey *service.APIKey) (*service.UserSubscription, error) {
	if c == nil || apiKeyService == nil {
		return nil, nil
	}
	subscription, err := apiKeyService.GetActiveRouteSubscription(c.Request.Context(), apiKey)
	if err != nil {
		return nil, err
	}
	c.Set(string(middleware.ContextKeySubscription), subscription)
	return subscription, nil
}

func checkSelectedGroupRouteEligibility(c *gin.Context, billing *service.BillingCacheService, apiKey *service.APIKey, subscription *service.UserSubscription) error {
	if c == nil || billing == nil || apiKey == nil {
		return nil
	}
	return billing.CheckBillingEligibility(
		c.Request.Context(),
		apiKey.User,
		apiKey,
		apiKey.Group,
		subscription,
		service.QuotaPlatform(c.Request.Context(), apiKey),
	)
}

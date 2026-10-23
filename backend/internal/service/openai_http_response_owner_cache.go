package service

import (
	"context"
	"time"
)

// OpenAIHTTPResponseOwnerCache is an optional, authoritative shared-owner
// adapter. Production Redis implements it without expanding GatewayCache and
// breaking older adapters. This is tenant authorization, not WS socket routing.
type OpenAIHTTPResponseOwnerCache interface {
	SetHTTPResponseOwner(ctx context.Context, groupID int64, responseID string, userID, apiKeyID int64, ttl time.Duration) error
	GetHTTPResponseOwner(ctx context.Context, groupID int64, responseID string) (userID, apiKeyID int64, found bool, err error)
}

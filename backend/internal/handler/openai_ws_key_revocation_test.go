//go:build unit

package handler

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
)

type wsRevocationKeyRepo struct {
	service.APIKeyRepository
	initial *service.APIKey
	changed atomic.Bool
	change  string
}

func (r *wsRevocationKeyRepo) GetByKeyForAuth(context.Context, string) (*service.APIKey, error) {
	key := *r.initial
	user := *key.User
	key.User = &user
	if r.changed.Load() {
		switch r.change {
		case "deleted":
			return nil, service.ErrAPIKeyNotFound
		case "disabled":
			key.Status = service.StatusDisabled
		case "replaced":
			key.ID++
		case "database_error":
			return nil, errors.New("synthetic database failure")
		}
	}
	return &key, nil
}

func (*wsRevocationKeyRepo) UpdateLastUsed(context.Context, int64, time.Time) error { return nil }

// Keep the handshake's positive L2 entry throughout the test, modelling a
// delayed/lost invalidation on another instance. No live Redis is needed.
type wsRevocationAuthCache struct {
	service.APIKeyCache
	mu    sync.Mutex
	entry *service.APIKeyAuthCacheEntry
}

func (c *wsRevocationAuthCache) GetAuthCache(context.Context, string) (*service.APIKeyAuthCacheEntry, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.entry, nil
}

func (c *wsRevocationAuthCache) SetAuthCache(_ context.Context, _ string, entry *service.APIKeyAuthCacheEntry, _ time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entry = entry
	return nil
}

func TestOpenAIResponsesWebSocketRevalidatesKeyWithoutCache(t *testing.T) {
	for _, mode := range []string{service.OpenAIWSIngressModePassthrough, service.OpenAIWSIngressModeCtxPool} {
		for _, scenario := range []struct {
			name          string
			change        string
			atAccountSlot int32
			reason        string
			status        coderws.StatusCode
		}{
			{name: "active_control"},
			{name: "deleted_between_turns", change: "deleted", reason: "Invalid API key"},
			{name: "disabled_between_turns", change: "disabled", reason: "API key is disabled"},
			{name: "replaced_between_turns", change: "replaced", reason: "Invalid API key"},
			{name: "database_error", change: "database_error", reason: "temporarily unavailable", status: coderws.StatusTryAgainLater},
			{name: "deleted_during_first_admission", change: "deleted", atAccountSlot: 1, reason: "Invalid API key"},
			{name: "deleted_during_followup_admission", change: "deleted", atAccountSlot: 2, reason: "Invalid API key"},
		} {
			t.Run(mode+"/"+scenario.name, func(t *testing.T) {
				repo := &wsRevocationKeyRepo{change: scenario.change}
				var svc *service.APIKeyService
				var slots atomic.Int32
				tc := openAIResponsesWSUsageLogCase{
					ingressMode:             mode,
					firstPayload:            `{"type":"response.create","model":"gpt-5.1","stream":true}`,
					secondPayload:           `{"type":"response.create","model":"gpt-5.1","stream":true,"previous_response_id":"resp_usage_e2e_1"}`,
					firstFrameCloseExpected: scenario.atAccountSlot == 1,
					secondTurnCloseExpected: scenario.change != "" && scenario.atAccountSlot != 1,
					closeReason:             scenario.reason,
					closeStatus:             scenario.status,
					authSetup: func(key *service.APIKey, cfg *config.Config) *service.APIKeyService {
						key.User.Concurrency = 1
						key.Group = &service.Group{ID: *key.GroupID, Status: service.StatusActive, Platform: service.PlatformOpenAI, Hydrated: true}
						repo.initial = key
						cfg.APIKeyAuth.L2TTLSeconds = 60
						svc = service.NewAPIKeyService(repo, nil, nil, nil, nil, &wsRevocationAuthCache{}, cfg)
						return svc
					},
					afterAccountSlot: func() {
						if slots.Add(1) == scenario.atAccountSlot {
							repo.changed.Store(true)
						}
					},
					afterFirstUpstreamRequest: func(*service.ChannelService) error {
						if scenario.atAccountSlot == 0 {
							repo.changed.Store(true)
						}
						return nil
					},
				}
				result := runOpenAIResponsesWebSocketUsageLogCase(t, tc)
				if scenario.change == "" {
					require.Len(t, result.logs, 2, "both admitted turns keep their usage records")
				}
				// Verify the fixture really retained a usable stale cache entry.
				cached, err := svc.GetByKey(context.Background(), repo.initial.Key)
				require.NoError(t, err)
				require.Equal(t, repo.initial.ID, cached.ID)
				require.Equal(t, service.StatusActive, cached.Status)
			})
		}
	}
}

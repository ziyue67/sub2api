package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"
)

type wsNodeOwnerCacheStub struct {
	stubGatewayCache
	mu     sync.Mutex
	owners map[WSNodeOwnerRef]string
	err    error
}

func (c *wsNodeOwnerCacheStub) ClaimWSNodeOwner(_ context.Context, ref WSNodeOwnerRef, owner string, _ time.Duration) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return false, c.err
	}
	if c.owners == nil {
		c.owners = map[WSNodeOwnerRef]string{}
	}
	if old, ok := c.owners[ref]; ok && old != owner {
		return false, nil
	}
	c.owners[ref] = owner
	return true, nil
}

func (c *wsNodeOwnerCacheStub) MatchesWSNodeOwner(_ context.Context, ref WSNodeOwnerRef, owner string) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.owners[ref] == owner, c.err
}

func ownerTestService(cache GatewayCache, node string) *OpenAIGatewayService {
	return &OpenAIGatewayService{cache: cache, cfg: &config.Config{Runtime: config.RuntimeConfig{WSNodeOwner: config.WSNodeOwnerConfig{Enabled: true, NodeID: node}}}}
}

func TestWSNodeOwnerBootAndLocalConnectionGate(t *testing.T) {
	cache := &wsNodeOwnerCacheStub{}
	writer := ownerTestService(cache, "node-a")
	ctx := context.Background()
	ref := WSNodeOwnerRef{GroupID: 17, APIKeyID: 101, AccountID: 201, ResponseID: "resp_fixture"}
	require.ErrorIs(t, writer.checkWSNodeOwner(ctx, ref), ErrOpenAIWSNodeOwner)
	writer.getOpenAIWSStateStore().BindResponseConn(ref.ResponseID, "local-conn", time.Minute)
	require.NoError(t, writer.bindWSNodeOwner(ctx, ref))
	require.NoError(t, writer.checkWSNodeOwner(ctx, ref))
	for _, node := range []string{"node-b", "node-a"} {
		other := ownerTestService(cache, node)
		other.getOpenAIWSStateStore().BindResponseConn(ref.ResponseID, "local-conn", time.Minute)
		err := other.checkWSNodeOwner(ctx, ref)
		require.ErrorIs(t, err, ErrOpenAIWSNodeOwner, "another node or a new boot cannot inherit a socket")
		var closeErr *OpenAIWSClientCloseError
		require.ErrorAs(t, err, &closeErr)
		require.Equal(t, websocket.StatusPolicyViolation, closeErr.StatusCode())
		var retry *UpstreamFailoverError
		require.False(t, errors.As(err, &retry), "owner rejection must not enter model replay/failover")
	}
	wrongKey := ref
	wrongKey.APIKeyID++
	require.ErrorIs(t, writer.checkWSNodeOwner(ctx, wrongKey), ErrOpenAIWSNodeOwner)
	writer.getOpenAIWSStateStore().DeleteResponseConn(ref.ResponseID)
	require.ErrorIs(t, writer.checkWSNodeOwner(ctx, ref), ErrOpenAIWSNodeOwner)
	writer.getOpenAIWSStateStore().BindResponseConn(ref.ResponseID, "different-conn", time.Minute)
	require.ErrorIs(t, writer.checkWSNodeOwner(ctx, ref), ErrOpenAIWSNodeOwner, "same boot must not silently transfer ownership to another connection")
	writer.getOpenAIWSStateStore().BindResponseConn(ref.ResponseID, "local-conn", time.Minute)
	cache.err = errors.New("synthetic-private-error")
	err := writer.checkWSNodeOwner(ctx, ref)
	require.ErrorIs(t, err, ErrOpenAIWSNodeOwner)
	require.NotContains(t, err.Error(), "synthetic-private")
	writer.cfg.Runtime.WSNodeOwner.Enabled = false
	require.NoError(t, writer.checkWSNodeOwner(ctx, ref), "legacy default does not add ownership calls")
}

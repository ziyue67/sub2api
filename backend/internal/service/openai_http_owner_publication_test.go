package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type legacyHTTPResponseOwnerPublicationCache struct {
	stubGatewayCache
	writes, failWrite int
	getCalls          int
	err               error
}

func (c *legacyHTTPResponseOwnerPublicationCache) SetSessionAccountID(ctx context.Context, groupID int64, key string, id int64, ttl time.Duration) error {
	c.writes++
	if c.writes == c.failWrite {
		return c.err
	}
	return c.stubGatewayCache.SetSessionAccountID(ctx, groupID, key, id, ttl)
}

func (c *legacyHTTPResponseOwnerPublicationCache) GetSessionAccountID(_ context.Context, _ int64, key string) (int64, error) {
	c.getCalls++
	return c.sessionBindings[key], nil
}

func TestHTTPResponseOwnerLegacyFailedPublicationDoesNotAuthorizeWriter(t *testing.T) {
	for _, failure := range []struct {
		name  string
		write int
	}{{"user-record", 1}, {"key-record", 2}} {
		t.Run(failure.name, func(t *testing.T) {
			ctx := context.Background()
			publishErr := errors.New("isolated publication failure")
			cache := &legacyHTTPResponseOwnerPublicationCache{failWrite: failure.write, err: publishErr}
			writer := NewOpenAIWSStateStore(cache)
			require.ErrorIs(t, writer.BindHTTPResponseOwner(ctx, 17, "resp_publication", 101, 201, time.Minute), publishErr)
			require.Equal(t, failure.write, cache.writes)
			for _, reader := range []OpenAIWSStateStore{writer, NewOpenAIWSStateStore(cache)} {
				before := cache.getCalls
				user, key, found, err := reader.GetHTTPResponseOwner(ctx, 17, "resp_publication")
				require.NoError(t, err)
				require.False(t, found, "failed new owner must not survive as a writer-only grant")
				require.Zero(t, user)
				require.Zero(t, key)
				require.Greater(t, cache.getCalls, before, "failed writer must consult shared state, not a local grant")
			}

			// Recovery publishes both legacy records before the local grant is
			// exposed; writer and a new process-shaped reader then agree.
			cache.failWrite = 0
			require.NoError(t, writer.BindHTTPResponseOwner(ctx, 17, "resp_publication", 101, 201, time.Minute))
			for _, reader := range []OpenAIWSStateStore{writer, NewOpenAIWSStateStore(cache)} {
				user, key, found, err := reader.GetHTTPResponseOwner(ctx, 17, "resp_publication")
				require.NoError(t, err)
				require.True(t, found)
				require.Equal(t, int64(101), user)
				require.Equal(t, int64(201), key)
			}
		})
	}
}

func TestHTTPResponseOwnerLegacyPublicationKeepsLocalOnlyMode(t *testing.T) {
	ctx := context.Background()
	store := NewOpenAIWSStateStore(nil)
	require.NoError(t, store.BindHTTPResponseOwner(ctx, 17, "resp_local", 101, 201, time.Minute))
	user, key, found, err := store.GetHTTPResponseOwner(ctx, 17, "resp_local")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, int64(101), user)
	require.Equal(t, int64(201), key)
}

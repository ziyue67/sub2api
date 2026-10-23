//go:build integration

package authcache_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type authFixtureSource struct {
	mu      sync.Mutex
	present map[string]bool
	calls   [2]map[string]int
}

type authFixtureRepository struct {
	// Unexpected repository operations fail instead of silently reaching a DB.
	service.APIKeyRepository
	source *authFixtureSource
	node   int
}

func (r *authFixtureRepository) GetByKeyForAuth(ctx context.Context, key string) (*service.APIKey, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.source.mu.Lock()
	r.source.calls[r.node][key]++
	present := r.source.present[key]
	r.source.mu.Unlock()
	if !present {
		return nil, service.ErrAPIKeyNotFound
	}
	return &service.APIKey{
		ID: 1001, UserID: 2001, Key: key, Status: service.StatusActive,
		User: &service.User{ID: 2001, Status: service.StatusActive, Role: service.RoleUser},
	}, nil
}

func (s *authFixtureSource) count(node int, key string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[node][key]
}

func TestIndependentAPIKeyServicesInvalidateHotL1ThroughRealPubSub(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("isolated Redis fixture requires Unix sockets")
	}
	binary, err := exec.LookPath("redis-server")
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal("redis-server is required in CI; backend workflow installs it")
		}
		t.Skip("redis-server is required for the isolated combination fixture")
	}
	temp, err := os.MkdirTemp("", "s2-auth-")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, os.RemoveAll(temp)) })
	socket := filepath.Join(temp, "r.sock")
	//nolint:gosec // G204: fixed CI tool and fixture-only arguments, no deployment config or endpoint.
	process := exec.Command(binary, "--port", "0", "--unixsocket", socket, "--unixsocketperm", "700", "--save", "", "--appendonly", "no")
	process.Stdout, process.Stderr = io.Discard, io.Discard
	require.NoError(t, process.Start())
	t.Cleanup(func() {
		_ = process.Process.Kill()
		done := make(chan struct{})
		go func() { _ = process.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("isolated Redis process did not join after termination")
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	newClient := func() *redis.Client {
		t.Helper()
		client := redis.NewClient(&redis.Options{
			Network: "unix", Addr: socket, PoolSize: 1, MaxRetries: -1,
			ContextTimeoutEnabled: true, DialTimeout: time.Second,
			ReadTimeout: time.Second, WriteTimeout: time.Second,
		})
		t.Cleanup(func() { require.NoError(t, client.Close()) })
		return client
	}
	controller := newClient()
	require.Eventually(t, func() bool { return controller.Ping(ctx).Err() == nil }, 3*time.Second, 10*time.Millisecond)
	const (
		positive  = "fixture-known-credential"
		negative  = "fixture-absent-credential"
		unrelated = "fixture-unrelated-credential"
	)
	source := &authFixtureSource{
		present: map[string]bool{positive: true, negative: false, unrelated: true},
		calls:   [2]map[string]int{{}, {}},
	}
	cfg := &config.Config{APIKeyAuth: config.APIKeyAuthCacheConfig{
		// Leave room for Ristretto's internal item cost as well as the few
		// fixture entries; capacity eviction must not impersonate invalidation.
		L1Size: 1024, L1TTLSeconds: 120, NegativeTTLSeconds: 120,
		L2TTLSeconds: 0, JitterPercent: 0,
	}}
	var nodes [2]*service.APIKeyService
	var connectionIDs [2]int64
	for node := range nodes {
		client := newClient()
		id, err := client.ClientID(ctx).Result()
		require.NoError(t, err)
		connectionIDs[node] = id
		cache := repository.NewAPIKeyCache(client)
		repo := &authFixtureRepository{source: source, node: node}
		nodes[node] = service.NewAPIKeyService(repo, nil, nil, nil, nil, cache, cfg)
		// The bounded L1 caches live only in this dedicated test binary.
		// Do not add a production cache-destruction API solely for this test.
		t.Cleanup(nodes[node].StopAuthCacheInvalidationSubscriber)
		nodes[node].StartAuthCacheInvalidationSubscriber(ctx)
	}
	require.NotEqual(t, connectionIDs[0], connectionIDs[1])
	require.Eventually(t, func() bool {
		counts, err := controller.PubSubNumSub(ctx, "auth:cache:invalidate").Result()
		return err == nil && counts["auth:cache:invalidate"] == 2 &&
			nodes[0].AuthCacheInvalidationSubscriberHealth().Connected &&
			nodes[1].AuthCacheInvalidationSubscriberHealth().Connected
	}, 3*time.Second, 10*time.Millisecond)
	lookupMatches := func(node int, key string, present bool) bool {
		value, err := nodes[node].GetByKey(ctx, key)
		if !present {
			return value == nil && errors.Is(err, service.ErrAPIKeyNotFound)
		}
		return err == nil && value != nil && value.Key == key && value.Status == service.StatusActive
	}
	assertHot := func(node int, key string, present bool) {
		t.Helper()
		// Ristretto buffers writes: observe a real public lookup with no source
		// call rather than sleeping or inspecting private service/cache fields.
		require.Eventually(t, func() bool {
			before := source.count(node, key)
			return lookupMatches(node, key, present) && source.count(node, key) == before
		}, time.Second, 10*time.Millisecond, "lookup must hit the intended hot L1 entry")
	}
	for node := range nodes {
		assertHot(node, positive, true)
		assertHot(node, negative, false)
		assertHot(node, unrelated, true)
	}
	var beforePositive, beforeNegative, beforeUnrelated [2]int
	for node := range nodes {
		beforePositive[node] = source.count(node, positive)
		beforeNegative[node] = source.count(node, negative)
		beforeUnrelated[node] = source.count(node, unrelated)
	}
	source.mu.Lock()
	source.present[positive], source.present[negative] = false, true
	source.mu.Unlock()
	// Changing the source alone must not be mistaken for invalidation. Both
	// nodes still return the old positive/negative results without a source call.
	for node := range nodes {
		require.True(t, lookupMatches(node, positive, true))
		require.True(t, lookupMatches(node, negative, false))
		require.Equal(t, beforePositive[node], source.count(node, positive))
		require.Equal(t, beforeNegative[node], source.count(node, negative))
	}
	publisher := repository.NewAPIKeyCache(controller)
	for _, key := range []string{positive, negative} {
		digest := sha256.Sum256([]byte(key))
		require.NoError(t, publisher.PublishAuthCacheInvalidation(ctx, hex.EncodeToString(digest[:])))
	}
	for node := range nodes {
		require.Eventually(t, func() bool {
			return lookupMatches(node, positive, false) && source.count(node, positive) > beforePositive[node]
		}, 3*time.Second, 10*time.Millisecond)
		require.Eventually(t, func() bool {
			return lookupMatches(node, negative, true) && source.count(node, negative) > beforeNegative[node]
		}, 3*time.Second, 10*time.Millisecond)
		assertHot(node, positive, false)
		assertHot(node, negative, true)
		require.True(t, lookupMatches(node, unrelated, true))
		require.Equal(t, beforeUnrelated[node], source.count(node, unrelated), "per-key messages must preserve unrelated hot entries")
	}
}

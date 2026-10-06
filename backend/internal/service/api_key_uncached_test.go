//go:build unit

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/stretchr/testify/require"
)

type uncachedKeyDeniedModelsRepo struct {
	UserGroupRateRepository
	err error
}

func (*uncachedKeyDeniedModelsRepo) GetRPMOverrideByUserAndGroup(context.Context, int64, int64) (*int, error) {
	return nil, nil
}

func (r *uncachedKeyDeniedModelsRepo) GetDeniedModelsByUserAndGroup(context.Context, int64, int64) ([]string, error) {
	return []string{"denied-model"}, r.err
}

func TestAPIKeyServiceGetByKeyUncached(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name    string
		missing bool
		err     error
	}{
		{name: "active"},
		{name: "deleted", err: ErrAPIKeyNotFound},
		{name: "database_error", err: errors.New("synthetic database error")},
		{name: "nil_result", missing: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			groupID := int64(42)
			current := &APIKey{ID: 100, UserID: 7, GroupID: &groupID, Status: StatusActive,
				IPWhitelist: []string{"192.0.2.0/24"}, User: &User{ID: 7, Status: StatusActive}}
			var lookupErr error
			calls := 0
			repo := &authRepoStub{getByKeyForAuth: func(context.Context, string) (*APIKey, error) {
				calls++
				if current == nil || lookupErr != nil {
					return current, lookupErr
				}
				key := *current
				user := *key.User
				key.User = &user
				return &key, nil
			}}
			denied := &uncachedKeyDeniedModelsRepo{}
			svc := NewAPIKeyService(repo, nil, nil, nil, denied, nil, &config.Config{
				APIKeyAuth: config.APIKeyAuthCacheConfig{L1Size: 100, L1TTLSeconds: 60, Singleflight: true},
			})
			t.Cleanup(svc.authCacheL1.Close)
			_, err := svc.GetByKey(ctx, "synthetic-key")
			require.NoError(t, err)
			svc.authCacheL1.Wait()
			require.Equal(t, 1, calls)
			lookupErr = tc.err
			if tc.missing {
				current = nil
			}
			got, err := svc.GetByKeyUncached(ctx, "synthetic-key")
			require.Equal(t, 2, calls, "must query the repository despite a warm L1 entry")
			switch {
			case tc.err != nil:
				require.ErrorIs(t, err, tc.err)
			case tc.missing:
				require.ErrorIs(t, err, ErrAPIKeyNotFound)
			default:
				require.NoError(t, err)
				require.Equal(t, []string{"denied-model"}, got.DeniedModelsInGroup())
				allowed, _ := ip.CheckIPRestrictionWithCompiledRules("198.51.100.1", got.CompiledIPWhitelist, got.CompiledIPBlacklist)
				require.False(t, allowed, "fresh auth must still compile IP restrictions")
				denied.err = errors.New("synthetic permissions failure")
				_, err = svc.GetByKeyUncached(ctx, "synthetic-key")
				require.ErrorIs(t, err, denied.err, "permission lookup failure must not allow access")
			}
			// Uncached reads must not overwrite the shared auth cache either.
			cached, err := svc.GetByKey(ctx, "synthetic-key")
			require.NoError(t, err)
			require.Equal(t, StatusActive, cached.Status)
		})
	}
}

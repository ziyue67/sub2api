//go:build integration

package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestRegionalAuthAuthorityConsistentPermissionsAndRevocation(t *testing.T) {
	ctx := context.Background()
	region := fmt.Sprintf("auth-%d", time.Now().UnixNano())
	var userID, groupID, keyID int64
	require.NoError(t, integrationDB.QueryRowContext(ctx, "INSERT INTO users(email,password_hash,status) VALUES($1,'sensitive-password','active') RETURNING id", region+"@test.invalid").Scan(&userID))
	require.NoError(t, integrationDB.QueryRowContext(ctx, "INSERT INTO groups(name,platform,is_exclusive,status) VALUES($1,'openai',true,'active') RETURNING id", region).Scan(&groupID))
	rawKey := "sk-" + region
	require.NoError(t, integrationDB.QueryRowContext(ctx, "INSERT INTO api_keys(user_id,group_id,key,name,status) VALUES($1,$2,$3,'regional','active') RETURNING id", userID, groupID, rawKey).Scan(&keyID))
	_, err := integrationDB.ExecContext(ctx, "INSERT INTO user_allowed_groups(user_id,group_id) VALUES($1,$2)", userID, groupID)
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, `INSERT INTO user_group_rate_multipliers(user_id,group_id,rpm_override,denied_models) VALUES($1,$2,7,'["old-model"]')`, userID, groupID)
	require.NoError(t, err)
	defer func() {
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM regional_auth_streams WHERE region_id=$1", region)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM api_keys WHERE id=$1", keyID)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM user_group_rate_multipliers WHERE user_id=$1", userID)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM user_allowed_groups WHERE user_id=$1", userID)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM user_subscriptions WHERE user_id=$1", userID)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM users WHERE id=$1", userID)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM groups WHERE id=$1", groupID)
	}()
	authority := NewRegionalAuthAuthority(integrationDB)
	_, err = authority.Restore(ctx, region, false)
	require.Error(t, err)
	_, err = authority.Restore(ctx, region, true)
	require.NoError(t, err)
	lookup := bytes.Repeat([]byte{1}, 32)
	wire, err := authority.Publish(ctx, region, func(ctx context.Context, revision uint64, source service.RegionalAuthSource) ([]byte, error) {
		require.Equal(t, uint64(1), revision)
		before, err := source.ListRegionalAuth(ctx, []int64{userID})
		if err != nil {
			return nil, err
		}
		require.Len(t, before, 1)
		require.Equal(t, []int64{groupID}, before[0].APIKey.User.AllowedGroups)
		require.Equal(t, []string{"old-model"}, before[0].APIKey.User.UserGroupDeniedModels)
		tx, err := integrationDB.BeginTx(ctx, nil)
		require.NoError(t, err)
		_, err = tx.ExecContext(ctx, "DELETE FROM user_allowed_groups WHERE user_id=$1", userID)
		require.NoError(t, err)
		_, err = tx.ExecContext(ctx, `UPDATE user_group_rate_multipliers SET rpm_override=3,denied_models='["new-model"]' WHERE user_id=$1`, userID)
		require.NoError(t, err)
		_, err = tx.ExecContext(ctx, "UPDATE groups SET name='after' WHERE id=$1", groupID)
		require.NoError(t, err)
		require.NoError(t, tx.Commit())
		after, err := source.ListRegionalAuth(ctx, []int64{userID})
		if err != nil {
			return nil, err
		}
		require.Equal(t, before[0].APIKey.User.AllowedGroups, after[0].APIKey.User.AllowedGroups)
		require.Equal(t, 7, *after[0].APIKey.User.UserGroupRPMOverride)
		require.Equal(t, []string{"old-model"}, after[0].APIKey.User.UserGroupDeniedModels)
		require.Equal(t, region, after[0].APIKey.Group.Name)
		return service.ExportRegionalAuth(ctx, source, []int64{userID}, lookup)
	})
	require.NoError(t, err)
	require.NotContains(t, string(wire), rawKey)
	require.NotContains(t, string(wire), "sensitive-password")
	restored, err := authority.Restore(ctx, region, false)
	require.NoError(t, err)
	require.Equal(t, wire, restored)
	_, err = authority.Publish(ctx, region, func(context.Context, uint64, service.RegionalAuthSource) ([]byte, error) {
		return nil, errors.New("permissions unavailable")
	})
	require.Error(t, err)
	// Failed exports neither overwrite the view nor consume a revision.
	_, err = authority.Publish(ctx, region, func(ctx context.Context, revision uint64, source service.RegionalAuthSource) ([]byte, error) {
		require.Equal(t, uint64(2), revision)
		records, err := source.ListRegionalAuth(ctx, []int64{userID})
		if err != nil {
			return nil, err
		}
		require.Len(t, records, 1)
		require.Empty(t, records[0].APIKey.User.AllowedGroups)
		require.Equal(t, 3, *records[0].APIKey.User.UserGroupRPMOverride)
		require.Equal(t, []string{"new-model"}, records[0].APIKey.User.UserGroupDeniedModels)
		return service.ExportRegionalAuth(ctx, source, []int64{userID}, lookup)
	})
	require.NoError(t, err)
	// Malformed permission rows must not silently become an unrestricted grant.
	previous, err := authority.Restore(ctx, region, false)
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, `UPDATE user_group_rate_multipliers SET denied_models='{}' WHERE user_id=$1`, userID)
	require.NoError(t, err)
	_, err = authority.Publish(ctx, region, func(ctx context.Context, _ uint64, source service.RegionalAuthSource) ([]byte, error) {
		return service.ExportRegionalAuth(ctx, source, []int64{userID}, lookup)
	})
	require.Error(t, err)
	unchanged, err := authority.Restore(ctx, region, false)
	require.NoError(t, err)
	require.Equal(t, previous, unchanged)
	_, err = integrationDB.ExecContext(ctx, `UPDATE user_group_rate_multipliers SET denied_models='["new-model"]' WHERE user_id=$1`, userID)
	require.NoError(t, err)
	// Deleting key/user/group and disabling any of them must publish a removal.
	for _, mutation := range []string{
		"UPDATE api_keys SET status='disabled' WHERE id=$1",
		"UPDATE api_keys SET deleted_at=NOW() WHERE id=$1",
		"UPDATE users SET status='disabled' WHERE id=$1",
		"UPDATE users SET deleted_at=NOW() WHERE id=$1",
		"UPDATE groups SET status='disabled' WHERE id=$1",
		"UPDATE groups SET deleted_at=NOW() WHERE id=$1",
	} {
		_, err = integrationDB.ExecContext(ctx, "UPDATE api_keys SET status='active',deleted_at=NULL WHERE id=$1", keyID)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(ctx, "UPDATE users SET status='active',deleted_at=NULL WHERE id=$1", userID)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(ctx, "UPDATE groups SET status='active',deleted_at=NULL WHERE id=$1", groupID)
		require.NoError(t, err)
		id := keyID
		if bytes.Contains([]byte(mutation), []byte("users")) {
			id = userID
		}
		if bytes.Contains([]byte(mutation), []byte("groups")) {
			id = groupID
		}
		_, err = integrationDB.ExecContext(ctx, mutation, id)
		require.NoError(t, err)
		packet, err := authority.Publish(ctx, region, func(ctx context.Context, _ uint64, source service.RegionalAuthSource) ([]byte, error) {
			return service.ExportRegionalAuth(ctx, source, []int64{userID}, lookup)
		})
		require.NoError(t, err)
		var doc service.RegionalAuthDocument
		require.NoError(t, json.Unmarshal(packet, &doc))
		require.Empty(t, doc.Entries, mutation)
	}
	// Subscription access is a grant, independent from balance/usage counters.
	_, err = integrationDB.ExecContext(ctx, "UPDATE groups SET status='active',deleted_at=NULL,subscription_type=$2 WHERE id=$1", groupID, service.SubscriptionTypeSubscription)
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, "INSERT INTO user_subscriptions(user_id,group_id,starts_at,expires_at,status) VALUES($1,$2,NOW()-INTERVAL '1 hour',NOW()+INTERVAL '1 hour','active')", userID, groupID)
	require.NoError(t, err)
	for _, revoked := range []bool{false, true} {
		if revoked {
			_, err = integrationDB.ExecContext(ctx, "UPDATE user_subscriptions SET deleted_at=NOW() WHERE user_id=$1", userID)
			require.NoError(t, err)
		}
		_, err = authority.Publish(ctx, region, func(ctx context.Context, _ uint64, source service.RegionalAuthSource) ([]byte, error) {
			records, err := source.ListRegionalAuth(ctx, []int64{userID})
			if err != nil {
				return nil, err
			}
			if revoked {
				require.Empty(t, records)
			} else {
				require.Len(t, records, 1)
				require.NotNil(t, records[0].Subscription)
				require.True(t, records[0].Subscription.ExpiresAt.After(time.Now()))
			}
			return service.ExportRegionalAuth(ctx, source, []int64{userID}, lookup)
		})
		require.NoError(t, err)
	}

}
func TestRegionalAuthAuthoritySinglePublisher(t *testing.T) {
	ctx := context.Background()
	region := fmt.Sprintf("auth-lock-%d", time.Now().UnixNano())
	defer func() {
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM regional_auth_streams WHERE region_id=$1", region)
	}()
	authority := NewRegionalAuthAuthority(integrationDB)
	_, err := authority.Restore(ctx, region, true)
	require.NoError(t, err)
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := authority.Publish(ctx, region, func(context.Context, uint64, service.RegionalAuthSource) ([]byte, error) {
			close(entered)
			<-release
			return []byte("first"), nil
		})
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("publisher did not enter")
	}
	_, err = authority.Publish(ctx, region, func(context.Context, uint64, service.RegionalAuthSource) ([]byte, error) {
		return nil, errors.New("must not export")
	})
	close(release)
	require.ErrorContains(t, err, "publisher busy")
	require.NoError(t, <-done)
	restored, err := NewRegionalAuthAuthority(integrationDB).Restore(ctx, region, false)
	require.NoError(t, err)
	require.Equal(t, []byte("first"), restored)
}

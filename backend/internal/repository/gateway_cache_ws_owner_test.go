package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestWSNodeOwnerClaimIsolationAndExpiry(t *testing.T) {
	cache, mr := newHTTPResponseOwnerTestCache(t)
	ctx := context.Background()
	ref := service.WSNodeOwnerRef{GroupID: 17, APIKeyID: 101, AccountID: 201, ResponseID: "resp_owner_fixture"}
	owner := "v1:node-a:boot-a:conn-digest-a"
	matched, err := cache.MatchesWSNodeOwner(ctx, ref, owner)
	require.NoError(t, err)
	require.False(t, matched)
	claimed, err := cache.ClaimWSNodeOwner(ctx, ref, owner, time.Minute)
	require.NoError(t, err)
	require.True(t, claimed)
	require.NotContains(t, wsNodeOwnerKey(ref), ref.ResponseID)
	for _, foreign := range []string{"v1:node-b:boot-a:conn-digest-a", "v1:node-a:boot-b:conn-digest-a", "v1:node-a:boot-a:conn-digest-b", "v2:node-a:boot-a:conn-digest-a"} {
		matched, err = cache.MatchesWSNodeOwner(ctx, ref, foreign)
		require.False(t, matched)
		if foreign[1] == '2' {
			require.Error(t, err)
			continue
		}
		require.NoError(t, err)
		claimed, err = cache.ClaimWSNodeOwner(ctx, ref, foreign, time.Hour)
		require.NoError(t, err)
		require.False(t, claimed, "foreign boot cannot replace or refresh a live owner")
	}
	require.Equal(t, time.Minute, mr.TTL(wsNodeOwnerKey(ref)))
	for _, different := range []service.WSNodeOwnerRef{
		{GroupID: 18, APIKeyID: 101, AccountID: 201, ResponseID: ref.ResponseID},
		{GroupID: 17, APIKeyID: 102, AccountID: 201, ResponseID: ref.ResponseID},
		{GroupID: 17, APIKeyID: 101, AccountID: 202, ResponseID: ref.ResponseID},
	} {
		matched, err = cache.MatchesWSNodeOwner(ctx, different, owner)
		require.NoError(t, err)
		require.False(t, matched)
	}
	mr.FastForward(2 * time.Minute)
	matched, err = cache.MatchesWSNodeOwner(ctx, ref, owner)
	require.NoError(t, err)
	require.False(t, matched)
	mr.SetError("synthetic-private-topology")
	_, err = cache.MatchesWSNodeOwner(ctx, ref, owner)
	require.ErrorIs(t, err, errWSNodeOwnerUnavailable)
	require.NotContains(t, err.Error(), "synthetic-private")
}

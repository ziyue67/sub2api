package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type codexWebSocketCapabilityResolverStub struct {
	values map[int64]bool
	calls  map[int64]int
}

func (s *codexWebSocketCapabilityResolverStub) GroupCodexSupportsWebSockets(_ context.Context, group *Group) bool {
	groupID := int64(0)
	if group != nil {
		groupID = group.ID
	}
	if s.calls == nil {
		s.calls = make(map[int64]int)
	}
	s.calls[groupID]++
	return s.values[groupID]
}

func TestAPIKeyServiceFillCodexWebSocketCapabilitiesUsesServerResolver(t *testing.T) {
	resolver := &codexWebSocketCapabilityResolverStub{values: map[int64]bool{1: true, 2: false}}
	svc := NewAPIKeyService(nil, nil, nil, nil, nil, nil, nil)
	svc.SetCodexWebSocketCapabilityResolver(resolver)
	keys := []APIKey{
		{ID: 1, Group: &Group{ID: 1}},
		{ID: 2, Group: &Group{ID: 1}},
		{ID: 3, Group: &Group{ID: 2}},
		{ID: 4},
	}

	svc.fillCodexWebSocketCapabilities(context.Background(), keys)

	require.True(t, keys[0].Group.CodexSupportsWebSockets)
	require.True(t, keys[1].Group.CodexSupportsWebSockets)
	require.False(t, keys[2].Group.CodexSupportsWebSockets)
	require.Equal(t, map[int64]int{1: 1, 2: 1}, resolver.calls)
}

package dto

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestGroupMapperExposesBlockSubagentsToAdminsOnly(t *testing.T) {
	group := &service.Group{ID: 3, Name: "cc", Platform: service.PlatformAnthropic, BlockSubagents: true}

	admin, err := json.Marshal(GroupFromServiceAdmin(group))
	require.NoError(t, err)
	require.Contains(t, string(admin), `"block_subagents":true`)

	user, err := json.Marshal(GroupFromService(group))
	require.NoError(t, err)
	require.NotContains(t, string(user), "block_subagents")
}

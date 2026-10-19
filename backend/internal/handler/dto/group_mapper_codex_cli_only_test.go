package dto

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestGroupMapperExposesCodexCLIOnlyToAdminsOnly(t *testing.T) {
	group := &service.Group{ID: 3, Name: "cc", Platform: service.PlatformAnthropic, CodexCLIOnly: true}

	admin, err := json.Marshal(GroupFromServiceAdmin(group))
	require.NoError(t, err)
	require.Contains(t, string(admin), `"codex_cli_only":true`)

	user, err := json.Marshal(GroupFromService(group))
	require.NoError(t, err)
	require.NotContains(t, string(user), "codex_cli_only")
}

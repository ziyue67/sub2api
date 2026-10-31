package service

import (
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
	"time"
)

func TestUpstreamChannelEntityValidation(t *testing.T) {
	m := UpstreamChannelMutation{Confirm: true, Code: "relay-a", Name: "  上游A  ", Remark: " 说明 "}
	require.NoError(t, ValidateUpstreamChannelMutation(&m, true))
	require.Equal(t, "上游A", m.Name)
	for _, code := range []string{"", "a/b", "https://host", "备注", strings.Repeat("x", 65)} {
		m.Code = code
		require.Error(t, ValidateUpstreamChannelMutation(&m, true))
	}
	m = UpstreamChannelMutation{Name: "上游", Remark: strings.Repeat("😀", 500), Revision: new(int64(1)), Confirm: true}
	require.NoError(t, ValidateUpstreamChannelMutation(&m, false))
	m.Remark += "中"
	require.Error(t, ValidateUpstreamChannelMutation(&m, false))
	require.NoError(t, ValidateUpstreamChannelBindings(UpstreamChannelBindingsMutation{ChannelID: 5, Confirm: true, Bindings: []UpstreamChannelBinding{{AccountID: 1, Revision: 0}}}), "no URL or account platform is required")
	require.NoError(t, ValidateUpstreamChannelBindings(UpstreamChannelBindingsMutation{ChannelID: 0, Confirm: true, Bindings: []UpstreamChannelBinding{{AccountID: 1, ChannelID: 5, Revision: 1}}}))
	for _, m := range []UpstreamChannelBindingsMutation{{Confirm: false, Bindings: []UpstreamChannelBinding{{AccountID: 1}}}, {Confirm: true, Bindings: make([]UpstreamChannelBinding, 101)}, {Confirm: true, Bindings: []UpstreamChannelBinding{{AccountID: 1}, {AccountID: 1}}}} {
		require.Error(t, ValidateUpstreamChannelBindings(m))
	}
}

func TestExplicitChannelProfitFilterNormalization(t *testing.T) {
	now := time.Now()
	f := UpstreamOperationsFilter{From: now.Add(-time.Hour), Until: now, Upstream: "channel:27", Dimension: "upstream", ModelScope: "public", Sort: "name", Direction: "asc", Page: 1, PageSize: 20}
	require.NoError(t, NormalizeProfitOperationsFilter(&f))
	require.Equal(t, int64(27), f.ChannelID)
	require.Empty(t, f.Upstream)
	f.Upstream = "channel:0"
	require.Error(t, NormalizeProfitOperationsFilter(&f))
	f.Upstream = "channel:28"
	f.ChannelID = 27
	require.Error(t, NormalizeProfitOperationsFilter(&f))
}

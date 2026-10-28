package service

import (
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestProfitOperationsCostMultiplierSourcesAndIndependentValidation(t *testing.T) {
	for _, tc := range []struct {
		raw   any
		want  *float64
		basis string
	}{{nil, profitPtr(.1), "current_default_0.1"}, {0.0, profitPtr(0), "current_saved_multiplier"}, {.25, profitPtr(.25), "current_saved_multiplier"}, {"bad", nil, "invalid_multiplier"}, {-1, nil, "invalid_multiplier"}} {
		v, b := OperationsCostMultiplier(&Account{Extra: map[string]any{"cost_multiplier": tc.raw}})
		require.Equal(t, tc.want, v)
		require.Equal(t, tc.basis, b)
	}
	now := time.Now()
	f := UpstreamOperationsFilter{From: now.Add(-time.Hour), Until: now, Dimension: "upstream", ModelScope: "public", Sort: "estimated_profit", Direction: "desc", Page: 1, PageSize: 20}
	require.NoError(t, ValidateProfitOperationsFilter(f))
	require.Error(t, ValidateUpstreamOperationsFilter(f), "financial sorting is confined to profit API")
	f.Sort = "name;DROP TABLE usage_logs"
	require.Error(t, ValidateProfitOperationsFilter(f))
}
func profitPtr(v float64) *float64 { return &v }

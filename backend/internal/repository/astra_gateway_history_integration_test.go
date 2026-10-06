//go:build integration

package repository

import (
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAstraGatewayHistoryPersistsAndFilters(t *testing.T) {
	client := testEntClient(t)
	repo := &settingRepository{client: client}
	host := fmt.Sprintf("chat.gateway.unified-%d.api.openai.com", time.Now().UnixNano())
	t.Cleanup(func() {
		_, _ = client.ExecContext(t.Context(), `DELETE FROM astra_gateway_history WHERE gateway=$1`, host)
	})
	now := time.Now().UTC().Truncate(time.Microsecond)
	row := service.AstraGatewayHistoryRecord{Gateway: host, SourceAccountID: 299, LastSeen: now, LastReason: "qualified", LastAnswer: "21"}
	require.NoError(t, repo.RecordAstraGateway(t.Context(), row, true))
	page, err := repo.ListAstraGateways(t.Context(), host, true, 0, 20)
	require.NoError(t, err)
	require.Empty(t, page.Items, "source success alone is not a validated target")
	row.TargetAccountID = 300
	row.LastReason = "target_probe_passed"
	require.NoError(t, repo.RecordAstraGateway(t.Context(), row, true))
	row.LastSeen = now.Add(time.Second)
	row.LastReason = "target_probe_degraded"
	row.LastAnswer = "29"
	require.NoError(t, repo.RecordAstraGateway(t.Context(), row, false))
	// A fresh repository instance sees the persisted history, including a later failure.
	fresh := &settingRepository{client: client}
	page, err = fresh.ListAstraGateways(t.Context(), host, true, 0, 20)
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.EqualValues(t, 1, page.UniqueGateways)
	got := page.Items[0]
	require.EqualValues(t, 1, got.Passes)
	require.EqualValues(t, 1, got.Failures)
	require.Equal(t, "29", got.LastAnswer)
	require.NotNil(t, got.LastPass)
	require.NotNil(t, got.LastFailure)
	// Delayed observations cannot overwrite the latest result.
	row.LastSeen = now
	row.LastReason = "target_probe_passed"
	row.LastAnswer = "21"
	require.NoError(t, repo.RecordAstraGateway(t.Context(), row, true))
	page, err = fresh.ListAstraGateways(t.Context(), host, true, 0, 20)
	require.NoError(t, err)
	require.Equal(t, "29", page.Items[0].LastAnswer)
	require.EqualValues(t, 2, page.Items[0].Passes)
	page, err = fresh.ListAstraGateways(t.Context(), host, false, 0, 1)
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.EqualValues(t, 2, page.Total)
	page, err = fresh.ListAstraGateways(t.Context(), "%", false, 0, 20)
	require.NoError(t, err)
	require.Empty(t, page.Items)
	row.Gateway = "secret-cookie-value"
	require.Error(t, repo.RecordAstraGateway(t.Context(), row, true))
}

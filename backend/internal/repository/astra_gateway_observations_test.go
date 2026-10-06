package repository

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAstraGatewayObservationsDeduplicateHostAndRetainFailures(t *testing.T) {
	pool := &codexGatewayPinUpstream{}
	host := "chat.gateway.unified-196.api.openai.com"
	now := time.Now()
	saved := []service.AstraGatewayHistoryRecord{}
	pool.historyRecorder = func(row service.AstraGatewayHistoryRecord, _ bool) { saved = append(saved, row) }
	for i, pass := range []bool{true, false} {
		claims, _ := json.Marshal(map[string]string{"host": host, "nonce": fmt.Sprint(i)})
		cookie := "header." + base64.RawURLEncoding.EncodeToString(claims) + ".signature"
		pool.recordSource(int64(299+i), now, &codexGatewayRoute{cookie: http.Cookie{Value: cookie}, expires: now.Add(time.Minute)}, pass)
	}
	require.Len(t, pool.gateways, 1)
	node := pool.gatewayObservations()[0]
	require.EqualValues(t, 2, node.Samples)
	require.EqualValues(t, 1, node.RepeatedHits)
	require.EqualValues(t, 1, node.SourcePasses)
	require.EqualValues(t, 1, node.SourceFailures)
	require.Equal(t, []int64{299, 300}, node.SourceAccountIDs)
	require.Equal(t, host, pool.statuses[300].Gateway)
	require.Len(t, saved, 2)
	encoded, err := json.Marshal(saved)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "signature")
	for i := 0; i < 300; i++ {
		pool.observeGatewaySource(fmt.Sprintf("chat.gateway.unified-%d.api.openai.com", i), 299, true, now.Add(time.Duration(i)*time.Second))
	}
	require.Len(t, pool.gateways, maxAstraObservedGateways)
}

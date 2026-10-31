package service

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type issue603NetworkStore struct {
	OpsRepository
	points []OpsNetworkPoint
}

func (s *issue603NetworkStore) InsertNetworkSample(_ context.Context, p OpsNetworkPoint) error {
	s.points = append(s.points, p)
	return nil
}
func (s *issue603NetworkStore) ReadNetworkSeries(context.Context, time.Time, time.Time) (*OpsNetworkSeries, error) {
	return &OpsNetworkSeries{Points: s.points}, nil
}

func TestIssue603CollectorNetworkFixturesAndUnknownBoundaries(t *testing.T) {
	tx, out, retrans := uint64(1000), uint64(100), uint64(1)
	name := "synthetic0"
	denied := false
	read := func(path string) ([]byte, error) {
		if denied {
			return nil, os.ErrPermission
		}
		switch path {
		case "/proc/net/dev":
			return []byte(fmt.Sprintf("%s: 0 0 0 0 0 0 0 0 %d 0 0 0 0 0 0 0\nlo: 0 0 0 0 0 0 0 0 999999 0 0 0 0 0 0 0", name, tx)), nil
		case "/proc/net/snmp":
			return []byte(fmt.Sprintf("Tcp: OutSegs RetransSegs\nTcp: %d %d\n", out, retrans)), nil
		default:
			t.Fatalf("unexpected privileged/source access: %s", path)
			return nil, os.ErrNotExist
		}
	}
	store := &issue603NetworkStore{}
	c := &OpsMetricsCollector{opsRepo: store, instanceID: "synthetic-instance", networkSampler: &hostNetworkSampler{readFile: read}}
	now := time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)
	require.NoError(t, c.collectNetwork(context.Background(), now))
	require.Equal(t, "initial_sample", store.points[0].EgressStatus)
	require.Nil(t, store.points[0].EgressMbps)
	tx += 7500000
	out += 1000
	retrans += 10
	require.NoError(t, c.collectNetwork(context.Background(), now.Add(time.Minute)))
	p := store.points[1]
	require.InDelta(t, 1, *p.EgressMbps, .001)
	require.InDelta(t, 1, *p.RetransPercent, .001)
	require.Equal(t, "not_collected", p.QdiscStatus)
	require.Nil(t, p.QdiscDrops)
	require.NoError(t, c.collectNetwork(context.Background(), now.Add(2*time.Minute)))
	require.Equal(t, 0.0, *store.points[2].EgressMbps, "measured idle zero is real")
	require.Nil(t, store.points[2].RetransPercent)
	require.Equal(t, "no_segments", store.points[2].TCPStatus)
	tx = 1
	out = 1
	retrans = 0
	require.NoError(t, c.collectNetwork(context.Background(), now.Add(3*time.Minute)))
	require.Nil(t, store.points[3].EgressMbps)
	require.Equal(t, "counter_reset", store.points[3].TCPStatus)
	name = "replacement0"
	require.NoError(t, c.collectNetwork(context.Background(), now.Add(4*time.Minute)))
	require.Nil(t, store.points[4].EgressMbps)
	require.NoError(t, c.collectNetwork(context.Background(), now.Add(10*time.Minute)))
	require.Equal(t, "uneven_window", store.points[5].EgressStatus)
	denied = true
	require.NoError(t, c.collectNetwork(context.Background(), now.Add(11*time.Minute)))
	require.Equal(t, "permission_denied", store.points[6].EgressStatus)
	require.Nil(t, store.points[6].EgressMbps)
	malformed := parseNetworkCounters(func(string) ([]byte, error) { return []byte("broken"), nil }, now)
	require.False(t, malformed.netOK)
	require.False(t, malformed.tcpOK)
	require.Equal(t, "malformed", malformed.tcpStatus)
	require.Equal(t, "unsupported", networkReadStatus(os.ErrNotExist))
}

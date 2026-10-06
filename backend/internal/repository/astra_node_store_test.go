package repository

import (
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestAstraNodeStorePrimaryBackupRotation(t *testing.T) {
	s := NewAstraNodeStore()
	now := time.Now()
	s.Upsert("one", 299, "unified-1", now.Add(2*time.Minute), now)
	s.Upsert("two", 299, "unified-2", now.Add(3*time.Minute), now.Add(time.Second))
	s.MarkTarget("one", 300, true, "target_probe_passed", now, now.Add(2*time.Minute), time.Time{})
	s.MarkTarget("two", 300, true, "target_probe_passed", now.Add(time.Second), now.Add(3*time.Minute), time.Time{})
	n, ok := s.Select(300, now.Add(2*time.Second))
	require.True(t, ok)
	require.Equal(t, "one", n.CookieValue)
	n, ok = s.Select(300, now.Add(2*time.Minute+time.Second))
	require.True(t, ok)
	require.Equal(t, "two", n.CookieValue)
	for _, entry := range s.Snapshot(now.Add(2*time.Minute + time.Second)) {
		if entry.CookieValue == "one" {
			require.Equal(t, AstraNodeExpired, entry.State)
		}
	}
}
func TestAstraNodeStoreTargetFailureNeverReady(t *testing.T) {
	s := NewAstraNodeStore()
	now := time.Now()
	s.Upsert("one", 299, "unified-1", now.Add(time.Minute), now)
	s.MarkTarget("one", 300, false, "target_probe_degraded", now, now.Add(time.Minute), now.Add(15*time.Second))
	_, ok := s.Select(300, now.Add(time.Second))
	require.False(t, ok)
	require.Equal(t, AstraNodeRejected, s.Snapshot(now)[0].State)
}

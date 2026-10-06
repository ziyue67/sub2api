package service

import (
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestAstraSchedulingHistoryBoundedSnapshot(t *testing.T) {
	h := &astraSchedulingHistory{}
	require.Empty(t, h.snapshot())
	for i := int64(1); i <= 5; i++ {
		h.add(AstraSchedulingRecord{AccountID: i, CheckedAt: time.Now(), Schedulable: i%2 == 0})
	}
	rows := h.snapshot()
	require.Len(t, rows, 3)
	require.Equal(t, int64(5), rows[0].AccountID)
	require.Equal(t, int64(3), rows[2].AccountID)
	rows[0].AccountID = 99
	require.Equal(t, int64(5), h.snapshot()[0].AccountID)
}

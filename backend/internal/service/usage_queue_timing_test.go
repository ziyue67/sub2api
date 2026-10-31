package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPR610QueueBoundaryUnknownZeroAndClamp(t *testing.T) {
	a := time.Now()
	for _, tc := range []struct {
		name                     string
		arrival, forward, legacy time.Time
		known                    bool
		queue                    time.Duration
	}{
		{"missing", time.Time{}, a, a, false, 0},
		{"future", a.Add(time.Second), a, a, false, 0},
		{"negative_legacy", a, a, a.Add(-time.Second), false, 0},
		{"zero", a, a, a, true, 0},
		{"one_hour", a, a.Add(time.Hour), a.Add(time.Hour), true, time.Hour},
		{"over_hour", a, a.Add(2 * time.Hour), a.Add(2 * time.Hour), true, time.Hour},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &OpenAIForwardResult{Duration: time.Second}
			applyOpenAIQueueTiming(r, tc.arrival, tc.forward, tc.legacy, "fixture", "fixture")
			require.Equal(t, tc.known, r.QueueWaitMeasured)
			require.Equal(t, tc.queue, r.QueueWait)
			if !tc.known {
				require.Nil(t, r.QueueTiming)
			} else {
				require.NotNil(t, r.QueueTiming)
				require.Nil(t, r.QueueTiming.ServerFirstTokenMs, "nonstream/missing first remains null")
			}
		})
	}
}

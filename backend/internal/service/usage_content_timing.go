package service

import (
	"context"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/requesttiming"
)

// One bounded batch for the current authorized page only. Diagnostic failure
// leaves truthful missing fields and never fails billing or the usage listing.
func (s *UsageService) EnrichContentTiming(ctx context.Context, logs []UsageLog) {
	reader, ok := s.usageRepo.(interface {
		ReadUsageContentTimings(context.Context, []int64) (map[int64]requesttiming.ContentObservation, error)
	})
	if !ok || len(logs) == 0 || len(logs) > 1000 {
		return
	}
	ids := make([]int64, len(logs))
	for i := range logs {
		ids[i] = logs[i].ID
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	items, err := reader.ReadUsageContentTimings(ctx, ids)
	if err != nil {
		return
	}
	for i := range logs {
		if value, ok := items[logs[i].ID]; ok {
			v := value
			logs[i].ContentTiming = &v
		}
	}
}

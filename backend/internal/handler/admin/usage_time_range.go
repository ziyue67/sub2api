package admin

import (
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
)

// parseUsageDateBoundary accepts calendar dates or explicit RFC3339 instants.
// A date-only end includes that entire local day (including DST changes);
// a timestamp end is already exclusive and must never be rounded or extended.
func parseUsageDateBoundary(value, userTZ string, end bool) (time.Time, error) {
	if instant, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return instant, nil
	}
	date, err := timezone.ParseInUserLocation("2006-01-02", value, userTZ)
	if err != nil {
		return time.Time{}, fmt.Errorf("use YYYY-MM-DD or RFC3339 with timezone: %w", err)
	}
	if end {
		date = date.AddDate(0, 0, 1)
	}
	return date, nil
}

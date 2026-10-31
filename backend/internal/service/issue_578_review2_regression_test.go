package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Inspect the actual worker contract without prescribing a queue vs field-CAS
// implementation: each value must keep its source observation across coalescing
// and a transient failure, even when responses arrive out of observation order.
func TestIssue578Review2PendingFieldObservationSurvivesRetry(t *testing.T) {
	for _, retry := range []bool{false, true} {
		t.Run(fmt.Sprint(retry), func(t *testing.T) {
			throttle := newAccountWriteThrottle(40 * time.Millisecond)
			t.Cleanup(throttle.close)
			t1 := time.Now().Add(-time.Second).UTC()
			t2, t3 := t1.Add(time.Millisecond), t1.Add(2*time.Millisecond)
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			t.Cleanup(unblock)
			var mu sync.Mutex
			calls := 0
			values := map[string]any{"codex_5h_used_percent": 100.0}
			versions := map[string]time.Time{"codex_5h_used_percent": t2}
			write := func(ctx context.Context, updates map[string]any) error {
				mu.Lock()
				calls++
				n := calls
				mu.Unlock()
				if n == 1 {
					close(entered)
					select {
					case <-release:
					case <-ctx.Done():
						return ctx.Err()
					}
					if retry {
						return errors.New("synthetic transient")
					}
				}
				mu.Lock()
				defer mu.Unlock()
				for _, key := range []string{"codex_5h_used_percent", "codex_7d_used_percent"} {
					value, exists := updates[key]
					if !exists {
						continue
					}
					observed := codexUpdatesTime(updates)
					// A field-version implementation carries this internal metadata;
					// a safe generation queue uses the packet's original timestamp.
					if fields, ok := updates["codex_usage_field_versions"].(map[string]any); ok {
						if raw, exists := fields[key]; exists {
							var ns int64
							_, err := fmt.Sscan(fmt.Sprint(raw), &ns)
							if err != nil {
								return err
							}
							observed = time.Unix(0, ns)
						}
					}
					if !observed.Before(versions[key]) {
						values[key], versions[key] = value, observed
					}
				}
				return nil
			}
			packet := func(key string, value float64, observed time.Time) map[string]any {
				return map[string]any{key: value, "codex_usage_updated_at": observed.Format(time.RFC3339Nano)}
			}
			throttle.submitCodex(nil, 578, packet("codex_5h_used_percent", 88, t1.Add(-time.Millisecond)), write)
			<-entered
			throttle.submitCodex(nil, 578, packet("codex_5h_used_percent", 89, t1), write)
			throttle.submitCodex(nil, 578, packet("codex_7d_used_percent", 27, t3), write)
			// A delayed old field must still be compared by its own observation,
			// even if another window in pending has a later timestamp.
			throttle.submitCodex(nil, 578, packet("codex_5h_used_percent", 89, t1), write)
			unblock()
			require.Eventually(t, func() bool { mu.Lock(); defer mu.Unlock(); return values["codex_7d_used_percent"] == 27.0 }, time.Second, time.Millisecond)
			mu.Lock()
			require.Equal(t, 100.0, values["codex_5h_used_percent"], "a newer partial observation must not restamp an old field during retry")
			require.LessOrEqual(t, calls, 4, "partial-window retention must keep DB attempts bounded")
			mu.Unlock()
		})
	}
}

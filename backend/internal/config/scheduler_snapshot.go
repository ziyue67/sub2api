package config

import "fmt"

const (
	SchedulerSnapshotModeAuto   = "auto"
	SchedulerSnapshotModeWriter = "writer"
	SchedulerSnapshotModeReader = "reader"
)

// WritesSchedulerSnapshots controls bulk scheduler publication, not request-time
// authentication, accounting, concurrency or account cooldown feedback. Empty/auto
// preserves the full node's behavior and makes gateway nodes cache readers.
func (c *Config) WritesSchedulerSnapshots() bool {
	if c == nil {
		return true
	}
	switch c.Runtime.SchedulerSnapshotMode {
	case "", SchedulerSnapshotModeAuto:
		return c.RunsBackgroundJobs()
	case SchedulerSnapshotModeWriter:
		return c.RunsBackgroundJobs()
	default:
		return false
	}
}

func (c *Config) validateSchedulerSnapshotMode() error {
	switch c.Runtime.SchedulerSnapshotMode {
	case "", SchedulerSnapshotModeAuto, SchedulerSnapshotModeReader:
		return nil
	case SchedulerSnapshotModeWriter:
		if !c.RunsBackgroundJobs() {
			return fmt.Errorf("runtime.scheduler_snapshot_mode writer requires runtime.role full")
		}
		return nil
	default:
		return fmt.Errorf("runtime.scheduler_snapshot_mode must be auto, writer or reader")
	}
}

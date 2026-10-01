package service

import "github.com/Wei-Shaw/sub2api/internal/config"

// Only process-wide periodic jobs use this gate. Billing/audit queues, local
// cache invalidation, connection pools, and request-triggered work stay active
// on gateway replicas. The default/full node retains the existing behavior.
func startBackgroundService(cfg *config.Config, job interface{ Start() }) {
	if cfg.RunsBackgroundJobs() {
		job.Start()
	}
}

package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Explicit scopes avoid scanning the whole shared scheduler on every probe.
type SchedulerReadinessConfig struct {
	Enabled       bool     `mapstructure:"enabled"`
	Buckets       []string `mapstructure:"buckets"`
	MaxAgeSeconds int      `mapstructure:"max_age_seconds"`
}

func (c SchedulerReadinessConfig) MaxAge() time.Duration {
	if c.MaxAgeSeconds == 0 {
		return 900 * time.Second
	}
	return time.Duration(c.MaxAgeSeconds) * time.Second
}

func (c *Config) validateSchedulerReadiness() error {
	r := c.Runtime.SchedulerReadiness
	if r.MaxAgeSeconds < 0 || r.MaxAgeSeconds > 86400 {
		return fmt.Errorf("runtime.scheduler_readiness.max_age_seconds must be between 0 and 86400")
	}
	if !r.Enabled {
		return nil
	}
	if c.Runtime.Role != RuntimeRoleGateway || c.WritesSchedulerSnapshots() || c.Runtime.RegionalSchedulerFile != "" {
		return fmt.Errorf("runtime.scheduler_readiness requires a gateway reading shared Redis snapshots")
	}
	if len(r.Buckets) == 0 || len(r.Buckets) > 16 {
		return fmt.Errorf("runtime.scheduler_readiness.buckets requires 1 to 16 explicit buckets")
	}
	seen := map[string]bool{}
	for _, bucket := range r.Buckets {
		parts := strings.Split(bucket, ":")
		if len(parts) != 3 || seen[bucket] {
			return fmt.Errorf("runtime.scheduler_readiness.buckets contains an invalid or duplicate bucket")
		}
		id, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil || id < 0 || strconv.FormatInt(id, 10) != parts[0] || len(parts[1]) == 0 || len(parts[1]) > 64 {
			return fmt.Errorf("runtime.scheduler_readiness.buckets contains an invalid bucket")
		}
		for _, ch := range parts[1] {
			if (ch < 'a' || ch > 'z') && (ch < '0' || ch > '9') && ch != '_' && ch != '-' {
				return fmt.Errorf("runtime.scheduler_readiness.buckets contains an invalid platform")
			}
		}
		if parts[2] != "single" && parts[2] != "forced" && (parts[2] != "mixed" || (parts[1] != "anthropic" && parts[1] != "gemini")) {
			return fmt.Errorf("runtime.scheduler_readiness.buckets contains an invalid mode")
		}
		seen[bucket] = true
	}
	return nil
}

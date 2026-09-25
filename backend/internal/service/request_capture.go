package service

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/requestcapture"
)

const (
	SettingKeyRequestCaptureEnabled       = "request_capture_enabled"
	SettingKeyRequestCaptureQuotaMiB      = "request_capture_quota_mib"
	SettingKeyRequestCaptureRetentionDays = "request_capture_retention_days"
)

func (s *SystemSettings) requestCaptureConfig() requestcapture.Config {
	c := requestcapture.Config{Enabled: s.RequestCaptureEnabled, QuotaMiB: s.RequestCaptureQuotaMiB, RetentionDays: s.RequestCaptureRetentionDays}
	if c.QuotaMiB == 0 {
		c.QuotaMiB = 1024
	}
	if c.RetentionDays == 0 {
		c.RetentionDays = 7
	}
	return c
}
func ProvideRequestCaptureManager(db *sql.DB, settings *SettingService) (*requestcapture.Manager, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	config, err := settings.GetAllSettings(ctx)
	if err != nil {
		return nil, err
	}
	dir := os.Getenv("DATA_DIR")
	if dir == "" {
		dir = "./data"
	}
	manager, err := requestcapture.New(&requestcapture.SQLStore{DB: db}, filepath.Join(dir, "request-captures"), config.requestCaptureConfig())
	if err != nil {
		return nil, err
	}
	settings.requestCapture = manager
	return manager, nil
}

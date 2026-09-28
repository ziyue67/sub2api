package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const SettingKeyPelicanShowcaseConfig = "pelican_showcase_config"

const (
	PelicanShowcaseDefaultMaxItems      = 20
	PelicanShowcaseMaxItemsLimit        = 100
	PelicanShowcaseDefaultRetentionDays = 7
	PelicanShowcaseRetentionDaysLimit   = 90
)

// PelicanShowcaseConfig bounds the gallery: MaxItems per group, plus an optional age
// limit (AutoCleanup + RetentionDays). The gallery shows the groups that have a Pelican
// group test plan and keeps its own copies, so these limits are independent of the admin
// test history. Configs saved before group tests also carry "group_ids"; it is ignored.
type PelicanShowcaseConfig struct {
	MaxItems      int  `json:"max_items"`
	AutoCleanup   bool `json:"auto_cleanup"`
	RetentionDays int  `json:"retention_days"`
}

func DefaultPelicanShowcaseConfig() PelicanShowcaseConfig {
	return PelicanShowcaseConfig{
		MaxItems:      PelicanShowcaseDefaultMaxItems,
		AutoCleanup:   true,
		RetentionDays: PelicanShowcaseDefaultRetentionDays,
	}
}

// NormalizePelicanShowcaseConfig fills zero limits with defaults and rejects out-of-range values.
func NormalizePelicanShowcaseConfig(cfg PelicanShowcaseConfig) (PelicanShowcaseConfig, error) {
	if cfg.MaxItems == 0 {
		cfg.MaxItems = PelicanShowcaseDefaultMaxItems
	}
	if cfg.MaxItems < 1 || cfg.MaxItems > PelicanShowcaseMaxItemsLimit {
		return cfg, fmt.Errorf("showcase max items must be 1–%d", PelicanShowcaseMaxItemsLimit)
	}
	if cfg.RetentionDays == 0 {
		cfg.RetentionDays = PelicanShowcaseDefaultRetentionDays
	}
	if cfg.RetentionDays < 1 || cfg.RetentionDays > PelicanShowcaseRetentionDaysLimit {
		return cfg, fmt.Errorf("showcase retention must be 1–%d days", PelicanShowcaseRetentionDaysLimit)
	}
	return cfg, nil
}

// A missing setting means "never configured" and yields the defaults.
// Corrupt JSON is an error, so callers fail closed instead of deleting snapshots.
func parsePelicanShowcaseConfig(raw string) (PelicanShowcaseConfig, error) {
	if strings.TrimSpace(raw) == "" {
		return DefaultPelicanShowcaseConfig(), nil
	}
	var cfg PelicanShowcaseConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return cfg, fmt.Errorf("invalid pelican showcase config JSON")
	}
	return NormalizePelicanShowcaseConfig(cfg)
}

// retentionCutoff returns the oldest generation time still shown, or zero when
// auto cleanup is off (only the per-group count limit applies).
func (cfg PelicanShowcaseConfig) retentionCutoff(now time.Time) time.Time {
	if !cfg.AutoCleanup {
		return time.Time{}
	}
	return now.Add(-time.Duration(cfg.RetentionDays) * 24 * time.Hour)
}

// PelicanShowcaseRuntime is the gallery switch plus its limits, read on every use.
type PelicanShowcaseRuntime struct {
	Enabled bool
	Config  PelicanShowcaseConfig
}

var errPelicanShowcaseSettingsUnavailable = errors.New("pelican showcase settings unavailable")

// GetPelicanShowcaseRuntime reads the switch and config straight from the settings store.
// Errors are returned rather than defaulted: publishing, the user gallery and cleanup all
// skip on error, so an unreadable or corrupt config never widens exposure or deletes data.
func (s *SettingService) GetPelicanShowcaseRuntime(ctx context.Context) (PelicanShowcaseRuntime, error) {
	if s == nil || s.settingRepo == nil {
		return PelicanShowcaseRuntime{}, errPelicanShowcaseSettingsUnavailable
	}
	vals, err := s.settingRepo.GetMultiple(ctx, []string{SettingKeyPelicanShowcaseEnabled, SettingKeyPelicanShowcaseConfig})
	if err != nil {
		return PelicanShowcaseRuntime{}, err
	}
	cfg, err := parsePelicanShowcaseConfig(vals[SettingKeyPelicanShowcaseConfig])
	if err != nil {
		return PelicanShowcaseRuntime{}, err
	}
	return PelicanShowcaseRuntime{Enabled: vals[SettingKeyPelicanShowcaseEnabled] == "true", Config: cfg}, nil
}

// UpdatePelicanShowcaseSettings saves the gallery switch and limits from the Smart Ops
// page. Only these two keys are written, so it never touches other system settings.
func (s *SettingService) UpdatePelicanShowcaseSettings(ctx context.Context, enabled bool, cfg PelicanShowcaseConfig) (PelicanShowcaseRuntime, error) {
	if s == nil || s.settingRepo == nil {
		return PelicanShowcaseRuntime{}, errPelicanShowcaseSettingsUnavailable
	}
	normalized, err := NormalizePelicanShowcaseConfig(cfg)
	if err != nil {
		return PelicanShowcaseRuntime{}, infraerrors.BadRequest("INVALID_PELICAN_SHOWCASE", err.Error())
	}
	raw, err := json.Marshal(normalized)
	if err != nil {
		return PelicanShowcaseRuntime{}, err
	}
	if err := s.settingRepo.SetMultiple(ctx, map[string]string{
		SettingKeyPelicanShowcaseEnabled: strconv.FormatBool(enabled),
		SettingKeyPelicanShowcaseConfig:  string(raw),
	}); err != nil {
		return PelicanShowcaseRuntime{}, err
	}
	return PelicanShowcaseRuntime{Enabled: enabled, Config: normalized}, nil
}

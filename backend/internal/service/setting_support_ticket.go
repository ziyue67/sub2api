package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

const SettingKeySupportTicketConfig = "support_ticket_config"

const (
	SupportTicketMaxCategories     = 20
	SupportTicketCategoryMaxRunes  = 32
	SupportTicketDefaultMaxOpen    = 5
	SupportTicketMaxOpenLimit      = 50
	SupportTicketNoticeMaxRunes    = 500
	supportTicketDefaultCategories = "账户与充值,API 使用问题,模型与渠道,建议与反馈,其他"
)

// SupportTicketConfig holds the admin choices for the ticket form: the categories
// users pick from (none means the form has no category), how many unclosed
// tickets one user may hold, and a note shown above the form.
type SupportTicketConfig struct {
	Categories     []string `json:"categories"`
	MaxOpenPerUser int      `json:"max_open_per_user"`
	Notice         string   `json:"notice"`
}

func DefaultSupportTicketConfig() SupportTicketConfig {
	return SupportTicketConfig{
		Categories:     strings.Split(supportTicketDefaultCategories, ","),
		MaxOpenPerUser: SupportTicketDefaultMaxOpen,
	}
}

// NormalizeSupportTicketConfig trims names, drops empty and repeated categories,
// fills a zero limit with the default and rejects out-of-range values.
func NormalizeSupportTicketConfig(cfg SupportTicketConfig) (SupportTicketConfig, error) {
	categories := make([]string, 0, len(cfg.Categories))
	seen := make(map[string]struct{}, len(cfg.Categories))
	for _, raw := range cfg.Categories {
		name := cleanSupportTicketLine(raw)
		if name == "" {
			continue
		}
		if utf8.RuneCountInString(name) > SupportTicketCategoryMaxRunes {
			return cfg, fmt.Errorf("ticket category names must be at most %d characters", SupportTicketCategoryMaxRunes)
		}
		key := strings.ToLower(name)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		categories = append(categories, name)
	}
	if len(categories) > SupportTicketMaxCategories {
		return cfg, fmt.Errorf("at most %d ticket categories", SupportTicketMaxCategories)
	}
	cfg.Categories = categories
	if cfg.MaxOpenPerUser == 0 {
		cfg.MaxOpenPerUser = SupportTicketDefaultMaxOpen
	}
	if cfg.MaxOpenPerUser < 1 || cfg.MaxOpenPerUser > SupportTicketMaxOpenLimit {
		return cfg, fmt.Errorf("open tickets per user must be 1–%d", SupportTicketMaxOpenLimit)
	}
	cfg.Notice = cleanSupportTicketText(cfg.Notice)
	if utf8.RuneCountInString(cfg.Notice) > SupportTicketNoticeMaxRunes {
		return cfg, fmt.Errorf("the ticket notice must be at most %d characters", SupportTicketNoticeMaxRunes)
	}
	return cfg, nil
}

// A missing setting means "never configured" and yields the defaults.
func parseSupportTicketConfig(raw string) (SupportTicketConfig, error) {
	if strings.TrimSpace(raw) == "" {
		return DefaultSupportTicketConfig(), nil
	}
	var cfg SupportTicketConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return cfg, errors.New("invalid support ticket config JSON")
	}
	return NormalizeSupportTicketConfig(cfg)
}

// SupportTicketRuntime is the feature switch plus its config, read on every use.
type SupportTicketRuntime struct {
	Enabled bool
	Config  SupportTicketConfig
}

var errSupportTicketSettingsUnavailable = errors.New("support ticket settings unavailable")

// GetSupportTicketRuntime reads the switch and config straight from the settings
// store. Errors are returned so callers can fail closed.
func (s *SettingService) GetSupportTicketRuntime(ctx context.Context) (SupportTicketRuntime, error) {
	if s == nil || s.settingRepo == nil {
		return SupportTicketRuntime{}, errSupportTicketSettingsUnavailable
	}
	vals, err := s.settingRepo.GetMultiple(ctx, []string{SettingKeySupportTicketEnabled, SettingKeySupportTicketConfig})
	if err != nil {
		return SupportTicketRuntime{}, err
	}
	cfg, err := parseSupportTicketConfig(vals[SettingKeySupportTicketConfig])
	if err != nil {
		return SupportTicketRuntime{}, err
	}
	return SupportTicketRuntime{Enabled: vals[SettingKeySupportTicketEnabled] == "true", Config: cfg}, nil
}

// cleanSupportTicketText normalizes user text for storage: invalid UTF-8 is
// replaced, line endings become \n, and control characters other than line
// breaks and tabs are dropped (PostgreSQL rejects NUL in TEXT).
func cleanSupportTicketText(raw string) string {
	raw = strings.ReplaceAll(strings.ToValidUTF8(raw, "\uFFFD"), "\r\n", "\n")
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		switch {
		case r == '\r':
			return '\n'
		case r == '\n' || r == '\t':
			return r
		case unicode.IsControl(r):
			return -1
		}
		return r
	}, raw))
}

// cleanSupportTicketLine is cleanSupportTicketText for one-line fields: line
// breaks and tabs become single spaces.
func cleanSupportTicketLine(raw string) string {
	return strings.Join(strings.Fields(cleanSupportTicketText(raw)), " ")
}

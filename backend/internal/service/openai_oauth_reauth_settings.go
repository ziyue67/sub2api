package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// An empty engine preserves pre-upgrade account choices until an administrator
// explicitly selects a global engine. Concurrency alone must not reroute secrets.
func (s *OpenAIOAuthReauthService) GetRuntimeSettings(ctx context.Context) (OpenAIOAuthReauthRuntimeSettings, error) {
	cfg := OpenAIOAuthReauthRuntimeSettings{WorkerConcurrency: 3}
	if n, err := strconv.Atoi(os.Getenv("OPENAI_REAUTH_CONCURRENCY")); err == nil && n >= 1 && n <= 16 {
		cfg.WorkerConcurrency = n
	}
	if s == nil || s.settings == nil {
		return cfg, nil
	}
	raw, err := s.settings.GetValue(ctx, openAIOAuthReauthRuntimeSettingsKey)
	if err != nil && !errors.Is(err, ErrSettingNotFound) {
		return cfg, infraerrors.ServiceUnavailable("OPENAI_REAUTH_SETTINGS_UNAVAILABLE", "Re-login settings are unavailable")
	}
	if strings.TrimSpace(raw) != "" {
		if json.Unmarshal([]byte(raw), &cfg) != nil {
			return cfg, infraerrors.ServiceUnavailable("OPENAI_REAUTH_SETTINGS_INVALID", "Saved re-login settings are invalid")
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal([]byte(raw), &fields) != nil || fields == nil {
			return cfg, infraerrors.ServiceUnavailable("OPENAI_REAUTH_SETTINGS_INVALID", "Saved re-login settings are invalid")
		}
		value, present := fields["worker_concurrency"]
		cfg.ConcurrencyConfigured = present
		if present && string(value) == "null" {
			return cfg, infraerrors.ServiceUnavailable("OPENAI_REAUTH_SETTINGS_INVALID", "Saved re-login settings are invalid")
		}
	}
	if err := validateReauthRuntimeSettings(cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func validateReauthRuntimeSettings(cfg OpenAIOAuthReauthRuntimeSettings) error {
	if cfg.Engine != "" && cfg.Engine != OpenAIOAuthReauthEngineLocal && cfg.Engine != OpenAIOAuthReauthEngineSessionStudio {
		return infraerrors.BadRequest("OPENAI_REAUTH_ENGINE_INVALID", "Invalid re-login engine")
	}
	if cfg.WorkerConcurrency < 1 || cfg.WorkerConcurrency > 16 {
		return infraerrors.BadRequest("OPENAI_REAUTH_CONCURRENCY_INVALID", "Worker concurrency must be between 1 and 16")
	}
	return nil
}

func (s *OpenAIOAuthReauthService) SaveRuntimeSettings(ctx context.Context, cfg OpenAIOAuthReauthRuntimeSettings) (OpenAIOAuthReauthRuntimeSettings, error) {
	if err := validateReauthRuntimeSettings(cfg); err != nil {
		return cfg, err
	}
	if s == nil || s.settings == nil {
		return cfg, infraerrors.ServiceUnavailable("OPENAI_REAUTH_SETTINGS_UNAVAILABLE", "Re-login settings are unavailable")
	}
	if cfg.Engine == OpenAIOAuthReauthEngineSessionStudio {
		if _, _, err := s.sessionStudioConfig(ctx); err != nil {
			return cfg, err
		}
	}
	previous, err := s.GetRuntimeSettings(ctx)
	if err != nil {
		return cfg, err
	}
	if previous.Engine != "" && cfg.Engine == "" {
		return cfg, infraerrors.BadRequest("OPENAI_REAUTH_ENGINE_REQUIRED", "Select a re-login engine")
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return cfg, err
	}
	if err = s.settings.Set(ctx, openAIOAuthReauthRuntimeSettingsKey, string(raw)); err != nil {
		return cfg, infraerrors.ServiceUnavailable("OPENAI_REAUTH_SETTINGS_SAVE_FAILED", "Failed to save re-login settings")
	}
	return cfg, nil
}

func effectiveReauthEngine(mode, accountEngine, globalEngine string) string {
	if globalEngine == "" {
		return normalizedReauthEngine(accountEngine)
	}
	if mode == OpenAIOAuthReauthModePasswordTOTP {
		return globalEngine
	}
	return OpenAIOAuthReauthEngineLocal
}

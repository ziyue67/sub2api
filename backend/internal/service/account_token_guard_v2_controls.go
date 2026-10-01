package service

import (
	"context"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

type AccountTokenGuardV2Switches struct {
	Enabled            bool `json:"enabled"`
	AutoReloginEnabled bool `json:"auto_relogin_enabled"`
}

func (s *AccountTokenGuardV2Service) UpdateSwitches(ctx context.Context, id int64, enabled, autoRelogin *bool) (*AccountTokenGuardV2Switches, error) {
	if enabled == nil && autoRelogin == nil {
		return nil, infraerrors.BadRequest("TOKEN_GUARD_V2_SWITCH_REQUIRED", "Provide an automation switch")
	}
	repo, ok := s.repo.(interface {
		UpdateSwitches(context.Context, int64, *bool, *bool) error
	})
	if !ok {
		return nil, infraerrors.ServiceUnavailable("TOKEN_GUARD_V2_SWITCH_UNAVAILABLE", "Automation switches are unavailable")
	}
	if err := repo.UpdateSwitches(ctx, id, enabled, autoRelogin); err != nil {
		return nil, err
	}
	record, err := s.repo.GetAccount(ctx, id)
	if err != nil {
		return nil, err
	}
	if record == nil {
		return nil, infraerrors.NotFound("TOKEN_GUARD_V2_NOT_FOUND", "Monitored account not found")
	}
	return &AccountTokenGuardV2Switches{Enabled: record.Enabled, AutoReloginEnabled: record.AutoReloginEnabled}, nil
}

func (s *AccountTokenGuardV2Service) GetRuntimeSettings(ctx context.Context) (OpenAIOAuthReauthRuntimeSettings, error) {
	return s.reauth.GetRuntimeSettings(ctx)
}

func (s *AccountTokenGuardV2Service) SaveRuntimeSettings(ctx context.Context, cfg OpenAIOAuthReauthRuntimeSettings) (OpenAIOAuthReauthRuntimeSettings, error) {
	return s.reauth.SaveRuntimeSettings(ctx, cfg)
}

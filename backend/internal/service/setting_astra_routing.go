package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/google/uuid"
)

const astraRoutingSettingKey = "astra_routing_experiment_v1"

func (s *SettingService) GetAstraRouting(ctx context.Context) (config.AstraRoutingSettings, error) {
	value, err := s.settingRepo.GetValue(ctx, astraRoutingSettingKey)
	if errors.Is(err, ErrSettingNotFound) || (err == nil && value == "") {
		if s.cfg == nil {
			return config.AstraRoutingSettings{}, nil
		}
		return config.AstraRoutingSettings{CookiePool: s.cfg.Gateway.CodexGatewayPin, WSSession: s.cfg.Gateway.CodexWSAnchor}, nil
	}
	if err != nil {
		return config.AstraRoutingSettings{}, err
	}
	var result config.AstraRoutingSettings
	if err = json.Unmarshal([]byte(value), &result); err != nil {
		return result, fmt.Errorf("invalid Astra routing settings")
	}
	return result, result.Validate()
}

// SetAstraRouting prepares account dependencies atomically before publishing
// the new configuration. A failed save never starts model requests.
func (s *SettingService) SetAstraRouting(ctx context.Context, value config.AstraRoutingSettings) (config.AstraRoutingSettings, error) {
	var err error
	value, err = config.ResolveAstraDependencies(value)
	if err != nil {
		return value, err
	}
	if value.WSSession.Enabled && s.cfg != nil {
		ws := s.cfg.Gateway.OpenAIWS
		if ws.ForceHTTP || !ws.Enabled || !ws.OAuthEnabled || !ws.ResponsesWebsocketsV2 {
			return value, fmt.Errorf("astra_global_ws_disabled")
		}
	}
	s.astraRoutingMu.Lock()
	previous, readErr := s.GetAstraRouting(ctx)
	if readErr != nil {
		s.astraRoutingMu.Unlock()
		return value, readErr
	}
	schedulingOnly := config.AstraRouteSettingsEqual(previous, value)
	value.Revision = uuid.NewString()
	if schedulingOnly {
		value.Revision = previous.Revision
	}
	if writer, ok := s.settingRepo.(interface {
		SetAstraRoutingWithAccounts(context.Context, string, config.AstraRoutingSettings) error
	}); ok {
		err = writer.SetAstraRoutingWithAccounts(ctx, astraRoutingSettingKey, value)
	} else {
		var raw []byte
		raw, err = json.Marshal(value)
		if err == nil {
			err = s.settingRepo.Set(ctx, astraRoutingSettingKey, string(raw))
		}
	}
	if err != nil {
		s.astraRoutingMu.Unlock()
		return value, err
	}
	s.astraRoutingCache = &value
	s.astraRoutingExpires = time.Now().Add(5 * time.Second)
	callback := s.astraRoutingOnSaved
	s.astraRoutingMu.Unlock()
	if callback != nil && !schedulingOnly {
		callback(value)
	}
	return value, nil
}
func (s *SettingService) SetAstraRoutingOnSaved(callback func(config.AstraRoutingSettings)) {
	s.astraRoutingMu.Lock()
	defer s.astraRoutingMu.Unlock()
	s.astraRoutingOnSaved = callback
}
func (s *SettingService) astraRoutingRuntime(ctx context.Context) config.AstraRoutingSettings {
	s.astraRoutingMu.Lock()
	defer s.astraRoutingMu.Unlock()
	if s.astraRoutingCache != nil && time.Now().Before(s.astraRoutingExpires) {
		return *s.astraRoutingCache
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	value, err := s.GetAstraRouting(ctx)
	if err != nil {
		if s.astraRoutingCache != nil {
			return *s.astraRoutingCache
		}
		return config.AstraRoutingSettings{}
	}
	s.astraRoutingCache = &value
	s.astraRoutingExpires = time.Now().Add(5 * time.Second)
	return value
}

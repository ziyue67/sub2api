package config

import (
	"context"
	"fmt"
	"reflect"
)

type AstraRoutingSettings struct {
	SchedulingMode     string                `json:"scheduling_mode"`
	SchedulingGroupIDs []int64               `json:"scheduling_group_ids"`
	AccountScheduling  bool                  `json:"account_scheduling"`
	CookiePool         CodexGatewayPinConfig `json:"cookie_pool"`
	WSSession          CodexWSAnchorConfig   `json:"ws_session"`
	Revision           string                `json:"revision"`
}
type astraRoutingLoader struct {
	load func(context.Context) AstraRoutingSettings
}

func (c *Config) SetAstraRoutingLoader(load func(context.Context) AstraRoutingSettings) {
	c.astraRoutingLoader.Store(&astraRoutingLoader{load: load})
}
func (c *Config) AstraRouting(ctx context.Context) AstraRoutingSettings {
	if c == nil {
		return AstraRoutingSettings{}
	}
	if loader := c.astraRoutingLoader.Load(); loader != nil {
		return loader.load(ctx)
	}
	return AstraRoutingSettings{CookiePool: c.Gateway.CodexGatewayPin, WSSession: c.Gateway.CodexWSAnchor}
}
func (s AstraRoutingSettings) Validate() error {
	switch s.SchedulingMode {
	case "", "account", "model", "groups":
	default:
		return fmt.Errorf("invalid scheduling mode")
	}
	if len(s.SchedulingGroupIDs) > 100 {
		return fmt.Errorf("select at most 100 groups")
	}
	seen := map[int64]bool{}
	for _, id := range s.SchedulingGroupIDs {
		if id <= 0 || seen[id] {
			return fmt.Errorf("invalid scheduling groups")
		}
		seen[id] = true
	}
	if s.AccountScheduling && s.SchedulingMode == "groups" && len(s.SchedulingGroupIDs) == 0 {
		return fmt.Errorf("select scheduling groups")
	}
	if err := s.CookiePool.Validate(); err != nil {
		return err
	}
	return s.WSSession.Validate()
}

func (c *Config) HasAstraRoutingLoader() bool { return c != nil && c.astraRoutingLoader.Load() != nil }

func (s AstraRoutingSettings) EffectiveSchedulingMode() string {
	if s.SchedulingMode == "" {
		return "account"
	}
	return s.SchedulingMode
}
func AstraRouteSettingsEqual(a, b AstraRoutingSettings) bool {
	a.AccountScheduling, b.AccountScheduling = false, false
	a.SchedulingMode, b.SchedulingMode = "", ""
	a.SchedulingGroupIDs, b.SchedulingGroupIDs = nil, nil
	a.Revision, b.Revision = "", ""
	return reflect.DeepEqual(a, b)
}

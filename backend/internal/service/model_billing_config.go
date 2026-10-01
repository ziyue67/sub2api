package service

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"regexp"
	"strings"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

// Model billing changes customer token charges, not published prices or account costs.
type ModelBillingConfig struct {
	Enabled bool               `json:"enabled"`
	Rules   []ModelBillingRule `json:"rules"`
}

type ModelBillingRule struct {
	Model      string  `json:"model"`
	Multiplier float64 `json:"multiplier"`
}

func DefaultModelBillingConfig() ModelBillingConfig {
	return ModelBillingConfig{Rules: []ModelBillingRule{{Model: "gpt-6-luna*", Multiplier: 10}}}
}

var modelBillingPattern = regexp.MustCompile(`^[a-zA-Z0-9_./:-]+\*?$`)

func validateModelBillingConfig(c ModelBillingConfig) error {
	bad := func(message string) error { return infraerrors.BadRequest("MODEL_BILLING_INVALID", message) }
	if len(c.Rules) > 100 || c.Enabled && len(c.Rules) == 0 {
		return bad("select 1–100 model billing rules before enabling")
	}
	seen := make(map[string]bool, len(c.Rules))
	for _, rule := range c.Rules {
		model := strings.ToLower(strings.TrimSpace(rule.Model))
		if !modelBillingPattern.MatchString(model) || len(model) > 200 || seen[model] {
			return bad("model names must be unique, non-empty and at most 200 bytes; only a trailing * wildcard is allowed")
		}
		if math.IsNaN(rule.Multiplier) || math.IsInf(rule.Multiplier, 0) || rule.Multiplier < 1 || rule.Multiplier > 1000 {
			return bad("model billing multipliers must be between 1 and 1000")
		}
		seen[model] = true
	}
	return nil
}

// Exact names win; otherwise the longest prefix wins. Rules never compound.
func (c ModelBillingConfig) multiplier(model string) float64 {
	if !c.Enabled {
		return 1
	}
	model = strings.ToLower(strings.TrimSpace(model))
	multiplier, longest := 1.0, 0
	for _, rule := range c.Rules {
		pattern := strings.ToLower(strings.TrimSpace(rule.Model))
		if pattern == model {
			return rule.Multiplier
		}
		if strings.HasSuffix(pattern, "*") {
			prefix := strings.TrimSuffix(pattern, "*")
			if len(prefix) > longest && strings.HasPrefix(model, prefix) {
				multiplier, longest = rule.Multiplier, len(prefix)
			}
		}
	}
	return multiplier
}

func applyModelBillingMultiplier(cost *CostBreakdown, cfg ModelBillingConfig, model string) {
	if cost == nil || cost.BillingMode != "" && cost.BillingMode != string(BillingModeToken) {
		return
	}
	multiplier := cfg.multiplier(model)
	if multiplier == 1 {
		return
	}
	cost.ActualCost *= multiplier
	cost.modelBillingMultiplier = multiplier
}

func costModelBillingMultiplier(cost *CostBreakdown) float64 {
	if cost == nil || cost.modelBillingMultiplier == 0 {
		return 1
	}
	return cost.modelBillingMultiplier
}

type modelBillingContextKey struct{}

func withModelBillingConfig(ctx context.Context, settings *SettingService) context.Context {
	return context.WithValue(ctx, modelBillingContextKey{}, settings.modelBillingConfigForUsage(ctx))
}

type modelBillingConfigCache struct {
	mu      sync.Mutex
	config  ModelBillingConfig
	expires time.Time
}

// Cache per service, including failures, to avoid querying settings for every token bill.
// A refresh failure preserves the last valid policy; a cold failure leaves surcharges off.
func (s *SettingService) modelBillingConfigForUsage(ctx context.Context) ModelBillingConfig {
	if cfg, ok := ctx.Value(modelBillingContextKey{}).(ModelBillingConfig); ok {
		return cfg
	}
	if s == nil || s.settingRepo == nil {
		return ModelBillingConfig{}
	}
	cache := &s.modelBillingCache
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if time.Now().Before(cache.expires) {
		return cache.config
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyOAuthAutoConfig)
	cfg := struct {
		ModelBilling ModelBillingConfig `json:"model_billing"`
	}{DefaultModelBillingConfig()}
	if errors.Is(err, ErrSettingNotFound) {
		err = nil
	}
	if err == nil && raw != "" {
		err = json.Unmarshal([]byte(raw), &cfg)
	}
	if err == nil {
		err = validateModelBillingConfig(cfg.ModelBilling)
	}
	if err == nil {
		cache.config = cfg.ModelBilling
	} else {
		logger.LegacyPrintf("service.billing", "Model billing configuration refresh failed; retaining the last valid policy")
	}
	cache.expires = time.Now().Add(15 * time.Second)
	return cache.config
}

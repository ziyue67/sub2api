package service

import (
	"context"
	"maps"
	"math"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/typesafe"
)

const (
	ContentModerationEngineOpenAI   = "openai"
	ContentModerationEngineTypeSafe = "typesafe"
)

// CategorySettings applies only to the two optional TypeSafe categories.
// The original thirteen categories retain their existing always-enabled behavior.
type ContentModerationCategorySetting struct {
	Enabled bool `json:"enabled"`
}

func mergeModerationCategorySettings(engine string, base, override map[string]ContentModerationCategorySetting) map[string]ContentModerationCategorySetting {
	out := map[string]ContentModerationCategorySetting{}
	if engine == ContentModerationEngineTypeSafe {
		for _, category := range []string{"jailbreak", "control_bypass"} {
			out[category] = base[category]
			if setting, ok := override[category]; ok {
				out[category] = setting
			}
		}
	}
	return out
}

func moderationCategoryEnabled(engine string, settings map[string]ContentModerationCategorySetting, category string) bool {
	if category == "jailbreak" || category == "control_bypass" {
		return engine == ContentModerationEngineTypeSafe && settings[category].Enabled
	}
	return true
}

func validateModerationThresholds(engine string, thresholds *map[string]float64) error {
	if thresholds != nil {
		for _, category := range ContentModerationCategories(engine) {
			if value, ok := (*thresholds)[category]; ok && (math.IsNaN(value) || math.IsInf(value, 0)) {
				return infraerrors.BadRequest("INVALID_CONTENT_MODERATION_THRESHOLD", "审计阈值必须为有限数值")
			}
		}
	}
	return nil
}

type ContentModerationEngineConfig struct {
	BaseURL          string                                      `json:"base_url"`
	Model            string                                      `json:"model"`
	ProxyID          *int64                                      `json:"proxy_id,omitempty"`
	APIKeyBaseURLs   map[string]string                           `json:"api_key_base_urls,omitempty"`
	APIKeys          []string                                    `json:"api_keys,omitempty"`
	TimeoutMS        int                                         `json:"timeout_ms"`
	RetryCount       int                                         `json:"retry_count"`
	Thresholds       map[string]float64                          `json:"thresholds"`
	CategorySettings map[string]ContentModerationCategorySetting `json:"category_settings"`
}

type ContentModerationKeyURLUpdate struct {
	KeyHash         string  `json:"key_hash"`
	BaseURL         string  `json:"base_url"`
	ExpectedBaseURL *string `json:"expected_base_url"`
}

type UpdateContentModerationEngineInput struct {
	KeyURLUpdates      []ContentModerationKeyURLUpdate              `json:"key_url_updates"`
	BaseURL            *string                                      `json:"base_url"`
	Model              *string                                      `json:"model"`
	ProxyID            *int64                                       `json:"proxy_id"`
	APIKey             *string                                      `json:"api_key"`
	APIKeys            *[]string                                    `json:"api_keys"`
	APIKeysMode        string                                       `json:"api_keys_mode"`
	DeleteAPIKeyHashes *[]string                                    `json:"delete_api_key_hashes"`
	ClearAPIKey        bool                                         `json:"clear_api_key"`
	TimeoutMS          *int                                         `json:"timeout_ms"`
	RetryCount         *int                                         `json:"retry_count"`
	Thresholds         *map[string]float64                          `json:"thresholds"`
	CategorySettings   *map[string]ContentModerationCategorySetting `json:"category_settings"`
}

type ContentModerationEngineMeta struct {
	CategorySettings  map[string]ContentModerationCategorySetting `json:"category_settings,omitempty"`
	MatchedCategories []string                                    `json:"matched_categories,omitempty"`
	Engine            string                                      `json:"engine"`
	Model             string                                      `json:"model"`
	RulesVersion      string                                      `json:"rules_version"`
	SkippedImages     int                                         `json:"skipped_images"`
}

func moderationEngine(engine string) string {
	if engine == "" {
		return ContentModerationEngineOpenAI
	}
	return engine
}

func validModerationEngine(engine string) bool {
	return engine == ContentModerationEngineOpenAI || engine == ContentModerationEngineTypeSafe
}

func moderationEngineDefaults(engine string) *ContentModerationEngineConfig {
	p := &ContentModerationEngineConfig{BaseURL: defaultContentModerationBaseURL, Model: defaultContentModerationModel, TimeoutMS: 3000, RetryCount: 2, Thresholds: ContentModerationDefaultThresholds(engine), CategorySettings: mergeModerationCategorySettings(engine, nil, nil)}
	if engine == ContentModerationEngineTypeSafe {
		p.BaseURL, p.Model = typesafe.DefaultBaseURL, typesafe.JevLatestModel
	}
	return p
}

func (cfg *ContentModerationConfig) engineProfile(engine string) *ContentModerationEngineConfig {
	var p ContentModerationEngineConfig
	if engine == ContentModerationEngineTypeSafe {
		if cfg.TypeSafe == nil {
			return moderationEngineDefaults(engine)
		}
		p = *cfg.TypeSafe
		defaults := moderationEngineDefaults(engine)
		if strings.TrimSpace(p.BaseURL) == "" {
			p.BaseURL = defaults.BaseURL
		}
		if strings.TrimSpace(p.Model) == "" {
			p.Model = defaults.Model
		}
	} else {
		p = ContentModerationEngineConfig{BaseURL: cfg.BaseURL, Model: cfg.Model, ProxyID: cfg.ProxyID, APIKeys: cfg.apiKeys(), TimeoutMS: cfg.TimeoutMS, RetryCount: cfg.RetryCount, Thresholds: cfg.Thresholds, CategorySettings: cfg.CategorySettings}
	}
	p.APIKeyBaseURLs = cloneModerationKeyURLs(p.APIKeyBaseURLs)
	p.ProxyID = cloneInt64Ptr(p.ProxyID)
	p.APIKeys = append([]string(nil), p.APIKeys...)
	p.Thresholds = mergeContentModerationThresholds(ContentModerationDefaultThresholds(engine), p.Thresholds)
	p.CategorySettings = mergeModerationCategorySettings(engine, nil, p.CategorySettings)
	return &p
}

func (cfg *ContentModerationConfig) applyEngineProfile(p *ContentModerationEngineConfig) {
	cfg.BaseURL, cfg.Model, cfg.ProxyID = p.BaseURL, p.Model, cloneInt64Ptr(p.ProxyID)
	cfg.APIKey, cfg.APIKeys = "", append([]string(nil), p.APIKeys...)
	cfg.APIKeyBaseURLs = cloneModerationKeyURLs(p.APIKeyBaseURLs)
	cfg.TimeoutMS, cfg.RetryCount = p.TimeoutMS, p.RetryCount
	cfg.Thresholds = cloneFloatMap(p.Thresholds)
	cfg.CategorySettings = maps.Clone(p.CategorySettings)
}

func (cfg *ContentModerationConfig) effectiveEngine(engine string) *ContentModerationConfig {
	out := cloneContentModerationConfig(cfg)
	out.Engine = moderationEngine(engine)
	out.applyEngineProfile(cfg.engineProfile(out.Engine))
	return out
}

func (s *ContentModerationService) updateEngineProfile(ctx context.Context, cfg *ContentModerationConfig, engine string, input UpdateContentModerationEngineInput) error {
	if !validModerationEngine(engine) {
		return infraerrors.BadRequest("INVALID_CONTENT_MODERATION_ENGINE", "内容审计引擎无效")
	}
	p := cfg.engineProfile(engine)
	defaults := moderationEngineDefaults(engine)
	if input.BaseURL != nil {
		p.BaseURL = strings.TrimSpace(*input.BaseURL)
		if p.BaseURL == "" {
			p.BaseURL = defaults.BaseURL
		}
	}
	if input.Model != nil {
		p.Model = strings.TrimSpace(*input.Model)
		if p.Model == "" {
			p.Model = defaults.Model
		}
	}
	if input.ProxyID != nil {
		p.ProxyID = cloneInt64Ptr(input.ProxyID)
		if *input.ProxyID <= 0 {
			p.ProxyID = nil
		}
	}
	if input.TimeoutMS != nil {
		p.TimeoutMS = *input.TimeoutMS
	}
	if input.RetryCount != nil {
		p.RetryCount = *input.RetryCount
	}
	if err := validateModerationThresholds(engine, input.Thresholds); err != nil {
		return err
	}
	if input.CategorySettings != nil {
		p.CategorySettings = mergeModerationCategorySettings(engine, p.CategorySettings, *input.CategorySettings)
	}
	if input.Thresholds != nil {
		p.Thresholds = mergeContentModerationThresholds(defaults.Thresholds, *input.Thresholds)
	}
	if input.ClearAPIKey {
		p.APIKeys = []string{}
	} else {
		mode := normalizeContentModerationAPIKeysMode(input.APIKeysMode)
		if input.DeleteAPIKeyHashes != nil && mode != contentModerationAPIKeysModeReplace {
			p.APIKeys = deleteModerationAPIKeysByHash(p.APIKeys, *input.DeleteAPIKeyHashes, engine)
		}
		if input.APIKeys != nil {
			if mode == contentModerationAPIKeysModeReplace {
				p.APIKeys = normalizeModerationAPIKeys(*input.APIKeys)
			} else {
				p.APIKeys = normalizeModerationAPIKeys(append(p.APIKeys, *input.APIKeys...))
			}
		}
		if input.APIKey != nil {
			p.APIKeys = normalizeModerationAPIKeys(append(p.APIKeys, *input.APIKey))
		}
	}
	if len(input.KeyURLUpdates) > 0 {
		if engine != ContentModerationEngineTypeSafe {
			return infraerrors.BadRequest("INVALID_KEY_URL_ENGINE", "仅 TypeSafe 支持逐 Key URL")
		}
		known := map[string]bool{}
		for _, key := range p.APIKeys {
			known[scopedModerationKeyHash(key, engine)] = true
		}
		if p.APIKeyBaseURLs == nil {
			p.APIKeyBaseURLs = map[string]string{}
		}
		for _, change := range input.KeyURLUpdates {
			if !known[change.KeyHash] {
				return infraerrors.New(409, "MODERATION_KEY_CHANGED", "Key 已变更，请刷新后重试")
			}
			old := p.BaseURL
			if base := p.APIKeyBaseURLs[change.KeyHash]; base != "" {
				old = base
			}
			if change.ExpectedBaseURL == nil || strings.TrimRight(strings.TrimSpace(*change.ExpectedBaseURL), "/") != strings.TrimRight(strings.TrimSpace(old), "/") {
				return infraerrors.New(409, "MODERATION_KEY_URL_CHANGED", "该 Key URL 已变更，请刷新后重试")
			}
			base := strings.TrimRight(strings.TrimSpace(change.BaseURL), "/")
			if base == "" {
				return infraerrors.BadRequest("INVALID_CONTENT_MODERATION_BASE_URL", "Key URL 不能为空")
			}
			if _, err := typesafe.SystemOneURL(base); err != nil {
				return infraerrors.BadRequest("INVALID_CONTENT_MODERATION_BASE_URL", "Key URL 无效")
			}
			p.APIKeyBaseURLs[change.KeyHash] = base
		}
	}
	// Remove orphan overrides when credentials are explicitly removed/replaced.
	for hash := range p.APIKeyBaseURLs {
		found := false
		for _, key := range p.APIKeys {
			if scopedModerationKeyHash(key, engine) == hash {
				found = true
				break
			}
		}
		if !found {
			delete(p.APIKeyBaseURLs, hash)
		}
	}
	if engine == ContentModerationEngineTypeSafe {
		if _, err := typesafe.SystemOneURL(p.BaseURL); err != nil {
			return infraerrors.BadRequest("INVALID_CONTENT_MODERATION_BASE_URL", "审计 Base URL 无效")
		}
	}
	check := cloneContentModerationConfig(cfg)
	check.Engine = engine
	check.applyEngineProfile(p)
	if err := s.validateConfig(ctx, check, engine); err != nil {
		return err
	}
	p = &ContentModerationEngineConfig{BaseURL: check.BaseURL, Model: check.Model, ProxyID: check.ProxyID, APIKeys: check.APIKeys, APIKeyBaseURLs: check.APIKeyBaseURLs, TimeoutMS: check.TimeoutMS, RetryCount: check.RetryCount, Thresholds: check.Thresholds, CategorySettings: check.CategorySettings}
	if engine == ContentModerationEngineTypeSafe {
		cfg.TypeSafe = p
	} else {
		cfg.applyEngineProfile(p)
	}
	return nil
}

func (s *ContentModerationService) engineConfigView(cfg *ContentModerationConfig) *ContentModerationConfigView {
	view := s.configView(cfg.effectiveEngine(cfg.Engine))
	view.EngineConfigs = map[string]*ContentModerationConfigView{}
	for _, engine := range []string{ContentModerationEngineOpenAI, ContentModerationEngineTypeSafe} {
		view.EngineConfigs[engine] = s.configView(cfg.effectiveEngine(engine))
	}
	return view
}

func scopedModerationKeyHash(key string, engine ...string) string {
	if len(engine) > 0 && engine[0] == ContentModerationEngineTypeSafe {
		return moderationAPIKeyHash("typesafe\x00" + key)
	}
	return moderationAPIKeyHash(key)
}

func (cfg *ContentModerationConfig) keyBaseURL(key string) string {
	if cfg.Engine == ContentModerationEngineTypeSafe {
		if base := cfg.APIKeyBaseURLs[scopedModerationKeyHash(key, cfg.Engine)]; base != "" {
			return base
		}
	}
	return cfg.BaseURL
}

func (s *ContentModerationService) configAPIKeyStatuses(cfg *ContentModerationConfig) []ContentModerationAPIKeyStatus {
	keys := cfg.apiKeys()
	rows := s.apiKeyStatuses(keys, cfg.Engine)
	if cfg.Engine == ContentModerationEngineTypeSafe {
		for i, key := range keys {
			rows[i].BaseURL = cfg.keyBaseURL(key)
		}
	}
	return rows
}

var ErrContentModerationConfigConflict = infraerrors.New(409, "MODERATION_CONFIG_CHANGED", "配置已被其他操作修改，请刷新后重试")

func cloneModerationKeyURLs(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	out := make(map[string]string, len(source))
	for key, value := range source {
		out[key] = value
	}
	return out
}

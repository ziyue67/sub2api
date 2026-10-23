package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const SettingKeySecurityPenalty = "security_penalty_config"

// Penalties restrict gateway admission only. They never change users.status.
type SecurityPenaltyConfig struct {
	Enabled            bool    `json:"enabled"`
	ObserveOnly        bool    `json:"observe_only"`
	Policy             string  `json:"policy"`
	Threshold          int     `json:"threshold"`
	WindowHours        int     `json:"window_hours"`
	DurationHours      int     `json:"duration_hours"`
	TargetGroupID      *int64  `json:"target_group_id"`
	AllGroups          bool    `json:"all_groups"`
	GroupIDs           []int64 `json:"group_ids"`
	ExemptUserIDs      []int64 `json:"exempt_user_ids"`
	Version            int64   `json:"version"`
	RiskControlEnabled bool    `json:"risk_control_enabled"`
	trustedUserIDs     []int64
}

func DefaultSecurityPenaltyConfig() SecurityPenaltyConfig {
	return SecurityPenaltyConfig{ObserveOnly: true, Policy: "session_block", Threshold: 3, WindowHours: 720, DurationHours: 24, AllGroups: true, GroupIDs: []int64{}, ExemptUserIDs: []int64{}, Version: 1}
}

func validateSecurityPenaltyConfig(c SecurityPenaltyConfig) error {
	if c.Policy != "session_block" && c.Policy != "user_block" && c.Policy != "session_route_then_block" {
		return infraerrors.BadRequest("security_penalty_invalid_policy", "处罚策略无效")
	}
	if c.Threshold < 1 || c.Threshold > 1000 || c.WindowHours < 1 || c.WindowHours > 87600 || c.DurationHours < 1 || c.DurationHours > 87600 {
		return infraerrors.BadRequest("security_penalty_invalid_limits", "阈值须为 1–1000，时间须为 1–87600 小时")
	}
	if c.Enabled && c.Policy == "session_route_then_block" && (c.TargetGroupID == nil || *c.TargetGroupID <= 0) {
		return infraerrors.BadRequest("security_penalty_target_required", "两级处罚需要指定受限分组")
	}
	if !c.AllGroups && len(c.GroupIDs) == 0 {
		return infraerrors.BadRequest("security_penalty_groups_required", "请选择适用分组")
	}
	if len(c.GroupIDs) > 1000 || len(c.ExemptUserIDs) > 1000 {
		return infraerrors.BadRequest("security_penalty_invalid_scope", "分组或豁免用户数量过多")
	}
	for _, ids := range [][]int64{c.GroupIDs, c.ExemptUserIDs} {
		for _, id := range ids {
			if id <= 0 {
				return infraerrors.BadRequest("security_penalty_invalid_scope", "ID 必须为正整数")
			}
		}
	}
	return nil
}

type SecurityPenaltyState struct {
	UserID          int64      `json:"user_id"`
	Scope           string     `json:"scope"`
	SubjectKey      string     `json:"subject_key"`
	Policy          string     `json:"policy"`
	Count           int        `json:"count"`
	WindowStartedAt time.Time  `json:"window_started_at"`
	BlockedUntil    *time.Time `json:"blocked_until"`
	RouteUntil      *time.Time `json:"route_until"`
	UpdatedAt       time.Time  `json:"updated_at"`
	Action          string     `json:"action"`
	Reason          string     `json:"reason"`
}

type SecurityPenaltyViolation struct {
	UserID     int64
	APIKeyID   int64
	GroupID    *int64
	RequestID  string
	SessionKey string
	Source     string
	Reason     string
	FullPrompt string
	Model      string
	Endpoint   string
}

type SecurityPenaltyAdmission struct {
	Blocked       bool
	TargetGroupID *int64
}

type SecurityPenaltyService struct {
	db       *sql.DB
	settings SettingRepository
	mu       sync.Mutex
	cached   SecurityPenaltyConfig
	expires  time.Time
}

func NewSecurityPenaltyService(db *sql.DB, settings SettingRepository, keys *APIKeyService) *SecurityPenaltyService {
	s := &SecurityPenaltyService{db: db, settings: settings}
	if keys != nil {
		keys.securityPenalties = s
	}
	return s
}

func (s *SecurityPenaltyService) GetConfig(ctx context.Context) (SecurityPenaltyConfig, error) {
	if s == nil {
		return DefaultSecurityPenaltyConfig(), nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if time.Now().Before(s.expires) {
		return s.cached, nil
	}
	values, err := s.settings.GetMultiple(ctx, []string{SettingKeySecurityPenalty, SettingKeyRiskControlEnabled, SettingKeyCyberPolicyUserAllowlist})
	if err != nil {
		return SecurityPenaltyConfig{}, err
	}
	cfg := DefaultSecurityPenaltyConfig()
	if raw := values[SettingKeySecurityPenalty]; raw != "" {
		if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
			return SecurityPenaltyConfig{}, errors.New("invalid security penalty config")
		}
	}
	if err := validateSecurityPenaltyConfig(cfg); err != nil {
		return SecurityPenaltyConfig{}, err
	}
	cfg.RiskControlEnabled = values[SettingKeyRiskControlEnabled] == "true"
	globalExempt, err := ParseCyberPolicyUserAllowlist(values[SettingKeyCyberPolicyUserAllowlist])
	if err != nil {
		return SecurityPenaltyConfig{}, err
	}
	// Global trusted-user exemptions apply to both legacy and new penalties.
	for id := range globalExempt {
		cfg.trustedUserIDs = append(cfg.trustedUserIDs, id)
	}
	s.cached = cfg
	s.expires = time.Now().Add(time.Second)
	return cfg, nil
}

func (s *SecurityPenaltyService) SaveConfig(ctx context.Context, cfg SecurityPenaltyConfig) (SecurityPenaltyConfig, error) {
	if err := validateSecurityPenaltyConfig(cfg); err != nil {
		return SecurityPenaltyConfig{}, err
	}
	if cfg.Enabled && cfg.Policy == "session_route_then_block" && cfg.TargetGroupID != nil {
		var active bool
		if err := s.db.QueryRowContext(ctx, "SELECT status='active' AND deleted_at IS NULL FROM groups WHERE id=$1", *cfg.TargetGroupID).Scan(&active); err != nil || !active {
			return SecurityPenaltyConfig{}, infraerrors.BadRequest("security_penalty_invalid_target", "受限分组不存在或未启用")
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SecurityPenaltyConfig{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var raw string
	if err := tx.QueryRowContext(ctx, "SELECT value FROM settings WHERE key=$1 FOR UPDATE", SettingKeySecurityPenalty).Scan(&raw); err != nil {
		return SecurityPenaltyConfig{}, err
	}
	old := DefaultSecurityPenaltyConfig()
	if err := json.Unmarshal([]byte(raw), &old); err != nil {
		return SecurityPenaltyConfig{}, err
	}
	if old.Version != cfg.Version {
		return SecurityPenaltyConfig{}, infraerrors.Conflict("security_penalty_config_conflict", "配置已更新，请刷新后重试")
	}
	cfg.Version++
	cfg.RiskControlEnabled = false
	data, err := json.Marshal(cfg)
	if err != nil {
		return SecurityPenaltyConfig{}, err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE settings SET value=$2,updated_at=NOW() WHERE key=$1", SettingKeySecurityPenalty, string(data)); err != nil {
		return SecurityPenaltyConfig{}, err
	}
	if err = tx.Commit(); err != nil {
		return SecurityPenaltyConfig{}, err
	}
	s.mu.Lock()
	s.expires = time.Time{}
	s.mu.Unlock()
	return s.GetConfig(ctx)
}

func (c SecurityPenaltyConfig) IncludesGroup(groupID *int64) bool {
	if c.AllGroups {
		return true
	}
	for _, id := range c.GroupIDs {
		if groupID != nil && id == *groupID {
			return true
		}
	}
	return false
}

func (c SecurityPenaltyConfig) Applies(userID int64) bool {
	if !c.Enabled || !c.RiskControlEnabled || userID <= 0 {
		return false
	}
	for _, ids := range [][]int64{c.ExemptUserIDs, c.trustedUserIDs} {
		for _, id := range ids {
			if id == userID {
				return false
			}
		}
	}
	return true
}

func penaltySubject(cfg SecurityPenaltyConfig, sessionKey string) (string, string) {
	if cfg.Policy == "user_block" {
		return "user", "user"
	}
	if sessionKey != "" {
		return "session", sessionKey
	}
	return "request", ""
}

func penaltyAction(cfg SecurityPenaltyConfig, state SecurityPenaltyState, now time.Time) string {
	if state.Count <= 0 {
		return "none"
	}
	if state.BlockedUntil != nil && now.Before(*state.BlockedUntil) {
		return "block"
	}
	if now.Sub(state.WindowStartedAt) >= time.Duration(cfg.WindowHours)*time.Hour || (state.BlockedUntil != nil && !now.Before(*state.BlockedUntil)) {
		return "none"
	}
	if state.BlockedUntil != nil && now.Before(*state.BlockedUntil) {
		return "block"
	}
	if cfg.Policy == "session_route_then_block" && state.Count > 0 && state.RouteUntil != nil && now.Before(*state.RouteUntil) {
		return "route"
	}
	return "record"
}

func (s *SecurityPenaltyService) Admission(ctx context.Context, userID int64, sessionKey string) (SecurityPenaltyAdmission, error) {
	cfg, err := s.GetConfig(ctx)
	if err != nil {
		return SecurityPenaltyAdmission{}, err
	}
	if !cfg.Applies(userID) || cfg.ObserveOnly {
		return SecurityPenaltyAdmission{}, nil
	}
	scope, key := penaltySubject(cfg, sessionKey)
	if scope == "request" {
		return SecurityPenaltyAdmission{}, nil
	}
	state, err := scanPenaltyState(s.db.QueryRowContext(ctx, "SELECT "+penaltyStateColumns+" FROM security_penalty_subjects WHERE user_id=$1 AND scope=$2 AND subject_key=$3 AND policy=$4", userID, scope, key, cfg.Policy))
	if errors.Is(err, sql.ErrNoRows) {
		return SecurityPenaltyAdmission{}, nil
	}
	if err != nil {
		return SecurityPenaltyAdmission{}, err
	}
	switch penaltyAction(cfg, state, time.Now()) {
	case "block":
		return SecurityPenaltyAdmission{Blocked: true}, nil
	case "route":
		return SecurityPenaltyAdmission{TargetGroupID: cfg.TargetGroupID}, nil
	default:
		return SecurityPenaltyAdmission{}, nil
	}
}

// An active gateway punishment supersedes the legacy user-status autoban.
// Check also clones the legacy configuration with its status autoban disabled
// so asynchronous legacy record workers preserve the login boundary.
type securityPenaltyContextKey struct{}

func WithSecurityPenaltyEnforcement(ctx context.Context) context.Context {
	return context.WithValue(ctx, securityPenaltyContextKey{}, true)
}
func securityPenaltyOwnsEnforcement(ctx context.Context) bool {
	value, _ := ctx.Value(securityPenaltyContextKey{}).(bool)
	return value
}

func (s *APIKeyService) SecurityPenaltyConfig(ctx context.Context) (SecurityPenaltyConfig, error) {
	if s == nil || s.securityPenalties == nil {
		return DefaultSecurityPenaltyConfig(), nil
	}
	return s.securityPenalties.GetConfig(ctx)
}

func (s *APIKeyService) SecurityPenaltyAdmission(ctx context.Context, userID int64, sessionKey string) (SecurityPenaltyAdmission, error) {
	if s == nil || s.securityPenalties == nil {
		return SecurityPenaltyAdmission{}, nil
	}
	return s.securityPenalties.Admission(ctx, userID, sessionKey)
}

func securityPenaltyReason(v string) string {
	runes := []rune(strings.TrimSpace(v))
	if len(runes) > 1000 {
		runes = runes[:1000]
	}
	return string(runes)
}

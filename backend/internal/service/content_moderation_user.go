package service

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// Independent exemptions apply only to their content-moderation layer.
// They never bypass upstream safety stops, authentication or other hard gates.
type ModerationUserState struct {
	UserID         int64      `json:"user_id"`
	Version        int64      `json:"version"`
	Generation     int64      `json:"generation"`
	AutoBanExempt  bool       `json:"auto_ban_exempt"`
	KeywordExempt  bool       `json:"keyword_exempt"`
	APIAuditExempt bool       `json:"api_audit_exempt"`
	EmailExempt    bool       `json:"email_exempt"`
	OwnedDisable   bool       `json:"owned_disable"`
	Status         string     `json:"status"`
	CurrentCount   int        `json:"current_count"`
	Reason         string     `json:"reason"`
	ActorID        *int64     `json:"actor_id"`
	UpdatedAt      *time.Time `json:"updated_at"`
}

type ModerationUserAction struct {
	Action         string `json:"action"`
	Version        int64  `json:"version"`
	Exempt         *bool  `json:"auto_ban_exempt"`
	KeywordExempt  *bool  `json:"keyword_exempt"`
	APIAuditExempt *bool  `json:"api_audit_exempt"`
	EmailExempt    *bool  `json:"email_exempt"`
	Reason         string `json:"reason"`
	Confirm        bool   `json:"confirm"`
	ActorID        int64  `json:"-"`
}

type ModerationHit struct {
	UserID       int64
	Version      int64
	RequestID    string
	Action       string
	Since        time.Time
	AutoBan      bool
	Threshold    int
	ExcludeCyber bool
}

type ModerationHitResult struct {
	Accepted     bool
	Count        int
	BanApplied   bool
	OwnedDisable bool
}

type ModerationUserRepository interface {
	GetModerationUser(context.Context, int64, time.Time, bool) (*ModerationUserState, error)
	ListModerationUsers(context.Context, int, int, string, int64, time.Time, bool) ([]ModerationUserState, int64, error)
	ManageModerationUser(context.Context, int64, ModerationUserAction) error
	ApplyModerationHit(context.Context, ModerationHit) (ModerationHitResult, error)
}

func (s *ContentModerationService) moderationUserRepository() (ModerationUserRepository, error) {
	if s != nil {
		if repo, ok := s.repo.(ModerationUserRepository); ok {
			return repo, nil
		}
	}
	return nil, infraerrors.ServiceUnavailable("MODERATION_USER_STATE_UNAVAILABLE", "Content moderation user state unavailable")
}

func (s *ContentModerationService) GetModerationUser(ctx context.Context, userID int64) (*ModerationUserState, error) {
	if userID <= 0 {
		return nil, infraerrors.BadRequest("INVALID_USER_ID", "Invalid user ID")
	}
	repo, err := s.moderationUserRepository()
	if err != nil {
		return nil, err
	}
	cfg, err := s.GetConfig(ctx)
	if err != nil {
		return nil, err
	}
	return repo.GetModerationUser(ctx, userID, time.Now().Add(-time.Duration(cfg.ViolationWindowHours)*time.Hour), cfg.CyberPolicyExcludeFromBanCount)
}

func validModerationUserFilter(state string) bool {
	switch state {
	case "all", "exempt", "enforced", "auto_ban_exempt", "keyword_exempt", "api_audit_exempt", "email_exempt":
		return true
	default:
		return false
	}
}

func (s *ContentModerationService) ListModerationUsers(ctx context.Context, page, size int, state string, userID int64) ([]ModerationUserState, int64, error) {
	if page < 1 || page > 1000000 || size < 1 || size > 100 || !validModerationUserFilter(state) || userID < 0 {
		return nil, 0, infraerrors.BadRequest("INVALID_MODERATION_FILTER", "Invalid page, size or state")
	}
	repo, err := s.moderationUserRepository()
	if err != nil {
		return nil, 0, err
	}
	cfg, err := s.GetConfig(ctx)
	if err != nil {
		return nil, 0, err
	}
	return repo.ListModerationUsers(ctx, page, size, state, userID, time.Now().Add(-time.Duration(cfg.ViolationWindowHours)*time.Hour), cfg.CyberPolicyExcludeFromBanCount)
}

func (s *ContentModerationService) ManageModerationUser(ctx context.Context, userID int64, in ModerationUserAction) (*ModerationUserState, error) {
	in.Reason = strings.TrimSpace(in.Reason)
	if userID <= 0 || in.ActorID <= 0 || in.Version < 0 || !in.Confirm || in.Reason == "" || utf8.RuneCountInString(in.Reason) > 500 {
		return nil, infraerrors.BadRequest("INVALID_MODERATION_ACTION", "Confirmation, version and a reason (1–500 characters) are required")
	}
	if in.Action != "unban" && in.Action != "reset" && in.Action != "exemption" {
		return nil, infraerrors.BadRequest("INVALID_MODERATION_ACTION", "Invalid action")
	}
	if (in.Action == "exemption") != (in.Exempt != nil || in.KeywordExempt != nil || in.APIAuditExempt != nil || in.EmailExempt != nil) {
		return nil, infraerrors.BadRequest("INVALID_MODERATION_ACTION", "Exemption requires at least one explicit boolean; other actions cannot change exemptions")
	}
	repo, err := s.moderationUserRepository()
	if err != nil {
		return nil, err
	}
	if err = repo.ManageModerationUser(ctx, userID, in); err != nil {
		return nil, err
	}
	if checked, ok := s.authCacheInvalidator.(interface {
		InvalidateAuthCacheByUserIDChecked(context.Context, int64) error
	}); ok {
		if err := checked.InvalidateAuthCacheByUserIDChecked(ctx, userID); err != nil {
			return nil, infraerrors.ServiceUnavailable("MODERATION_CACHE_INVALIDATION_FAILED", "Action committed, but authentication cache invalidation failed. Reload state and retry cache invalidation before verifying recovery.")
		}
	} else if s.authCacheInvalidator != nil {
		s.authCacheInvalidator.InvalidateAuthCacheByUserID(ctx, userID)
	}
	// A concurrent later action may already be visible: always return real state.
	return s.GetModerationUser(ctx, userID)
}

// Capture before any external or asynchronous work. A missing version fails closed
// for account mutations while the original content review continues normally.
func (s *ContentModerationService) CaptureModerationUserVersion(ctx context.Context, id int64) *int64 {
	state := s.captureModerationUserState(ctx, id)
	if state == nil {
		return nil
	}
	return &state.Version
}

// Read directly rather than from the auth/runtime cache: revocation must affect
// the next request. Failure grants no exemption, matching legacy enforcement.
func (s *ContentModerationService) captureModerationUserState(ctx context.Context, id int64) *ModerationUserState {
	repo, err := s.moderationUserRepository()
	if err != nil || id <= 0 {
		return nil
	}
	state, err := repo.GetModerationUser(ctx, id, time.Now(), false)
	if err != nil {
		return nil
	}
	return state
}

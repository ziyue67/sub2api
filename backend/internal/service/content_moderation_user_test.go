package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type moderationRecoveryRepo struct {
	*contentModerationTestRepo
	mu                      sync.Mutex
	state                   ModerationUserState
	getErr, errorAfterWrite error
	managed                 []ModerationUserAction
	hits                    []ModerationHit
	result                  ModerationHitResult
	user                    *User
	readCount               int
}

func (r *moderationRecoveryRepo) GetModerationUser(_ context.Context, id int64, _ time.Time, _ bool) (*ModerationUserState, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.readCount++
	if r.getErr != nil {
		return nil, r.getErr
	}
	if len(r.managed) > 0 && r.errorAfterWrite != nil {
		return nil, r.errorAfterWrite
	}
	state := r.state
	state.UserID = id
	return &state, nil
}
func (r *moderationRecoveryRepo) ListModerationUsers(context.Context, int, int, string, int64, time.Time, bool) ([]ModerationUserState, int64, error) {
	return []ModerationUserState{r.state}, 1, nil
}
func (r *moderationRecoveryRepo) ManageModerationUser(_ context.Context, _ int64, in ModerationUserAction) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.managed = append(r.managed, in)
	r.state.Version++
	switch in.Action {
	case "unban":
		r.state.Status = StatusActive
		r.state.OwnedDisable = false
		if r.user != nil {
			r.user.Status = StatusActive
		}
	case "reset":
		r.state.Generation++
		r.state.CurrentCount = 0
	case "exemption":
		if in.Exempt != nil {
			r.state.AutoBanExempt = *in.Exempt
		}
		if in.KeywordExempt != nil {
			r.state.KeywordExempt = *in.KeywordExempt
		}
		if in.APIAuditExempt != nil {
			r.state.APIAuditExempt = *in.APIAuditExempt
		}
		if in.EmailExempt != nil {
			r.state.EmailExempt = *in.EmailExempt
		}
	}
	return nil
}
func (r *moderationRecoveryRepo) ApplyModerationHit(ctx context.Context, in ModerationHit) (ModerationHitResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hits = append(r.hits, in)
	if r.user != nil {
		count, err := r.CountFlaggedByUserSince(ctx, in.UserID, in.Since, in.ExcludeCyber)
		if err != nil {
			return ModerationHitResult{}, err
		}
		count++
		ban := in.AutoBan && count >= in.Threshold && r.user.Status == StatusActive && !r.user.IsAdmin() && !r.state.AutoBanExempt
		if ban {
			r.user.Status = StatusDisabled
		}
		return ModerationHitResult{Accepted: true, Count: count, BanApplied: ban, OwnedDisable: ban}, nil
	}
	return r.result, nil
}

func TestModerationRecoveryUnconfirmedUnbanFailsClosed(t *testing.T) {
	userRepo := &contentModerationTestUserRepo{user: &User{ID: 306, Status: StatusDisabled}}
	svc := NewContentModerationService(nil, &contentModerationTestRepo{}, nil, nil, userRepo, nil, nil, nil)
	_, err := svc.UnbanUser(context.Background(), 306)
	require.Error(t, err)
	require.Equal(t, StatusDisabled, userRepo.user.Status)
	require.Empty(t, userRepo.updated)
}

func TestModerationRecoveryValidationDoesNotWrite(t *testing.T) {
	for _, in := range []ModerationUserAction{
		{Action: "reset", ActorID: 1, Reason: "review", Version: 0},
		{Action: "reset", Confirm: true, Reason: "review", Version: 0},
		{Action: "exemption", ActorID: 1, Confirm: true, Reason: "review", Version: 0},
		{Action: "unban", ActorID: 1, Confirm: true, Reason: "", Version: 0},
	} {
		r := &moderationRecoveryRepo{contentModerationTestRepo: &contentModerationTestRepo{}}
		s := &ContentModerationService{repo: r}
		_, err := s.ManageModerationUser(context.Background(), 306, in)
		require.Error(t, err)
		require.Empty(t, r.managed)
	}
}
func TestModerationRecoveryActionsReadBackAndInvalidate(t *testing.T) {
	r := &moderationRecoveryRepo{contentModerationTestRepo: &contentModerationTestRepo{}, state: ModerationUserState{Status: StatusDisabled, CurrentCount: 10, OwnedDisable: true, Version: 2}}
	cache := &contentModerationTestAuthCacheInvalidator{}
	s := &ContentModerationService{repo: r, settingRepo: &contentModerationTestSettingRepo{}, authCacheInvalidator: cache}
	state, err := s.ManageModerationUser(context.Background(), 306, ModerationUserAction{Action: "reset", ActorID: 1, Version: 2, Confirm: true, Reason: "reviewed"})
	require.NoError(t, err)
	require.Equal(t, 0, state.CurrentCount)
	require.Equal(t, StatusDisabled, state.Status)
	require.True(t, state.OwnedDisable)
	require.Equal(t, []int64{306}, cache.userIDs)
	r.errorAfterWrite = errors.New("readback failed")
	_, err = s.ManageModerationUser(context.Background(), 306, ModerationUserAction{Action: "unban", ActorID: 1, Version: 3, Confirm: true, Reason: "reviewed"})
	require.ErrorContains(t, err, "readback failed")
}
func TestModerationRecoveryHitFencingAndNotificationDedup(t *testing.T) {
	r := &moderationRecoveryRepo{contentModerationTestRepo: &contentModerationTestRepo{}, result: ModerationHitResult{Accepted: false}}
	s := &ContentModerationService{repo: r}
	id := int64(306)
	v := int64(4)
	log := &ContentModerationLog{UserID: &id, Flagged: true, Action: ContentModerationActionKeywordBlock, RequestID: "same-request", moderationVersion: &v}
	require.False(t, s.applyFlaggedAccountSideEffects(context.Background(), defaultContentModerationConfig(), log))
	require.True(t, log.suppressNotification)
	require.Len(t, r.hits, 1)
	require.Equal(t, int64(4), r.hits[0].Version)
}

// This invokes the actual login service after a source-scoped restore. Password
// and TOTP fields are retained, and a wrong password still fails authentication.
func TestModerationRecoveryRestorePreservesLoginSecurity(t *testing.T) {
	user := &User{ID: 306, Email: "synthetic@example.test", Status: StatusDisabled, TotpEnabled: true, TokenVersion: 7}
	require.NoError(t, user.SetPassword("synthetic-passphrase"))
	repo := &moderationRecoveryRepo{contentModerationTestRepo: &contentModerationTestRepo{}, state: ModerationUserState{Status: StatusDisabled, OwnedDisable: true}, user: user}
	userRepo := &recoveryLoginUserRepo{contentModerationTestUserRepo: contentModerationTestUserRepo{user: user}}
	auth := &AuthService{userRepo: userRepo, cfg: &config.Config{JWT: config.JWTConfig{Secret: "test-only-local-signing-secret", ExpireHour: 1}}}
	_, _, err := auth.Login(context.Background(), user.Email, "synthetic-passphrase")
	require.ErrorIs(t, err, ErrUserNotActive)
	hash := user.PasswordHash
	s := &ContentModerationService{repo: repo, settingRepo: &contentModerationTestSettingRepo{}}
	_, err = s.ManageModerationUser(context.Background(), 306, ModerationUserAction{Action: "unban", ActorID: 1, Confirm: true, Reason: "verified"})
	require.NoError(t, err)
	require.Equal(t, hash, user.PasswordHash)
	require.True(t, user.TotpEnabled)
	require.Equal(t, int64(7), user.TokenVersion)
	_, _, err = auth.Login(context.Background(), user.Email, "wrong-password")
	require.ErrorIs(t, err, ErrInvalidCredentials)
	token, returned, err := auth.Login(context.Background(), user.Email, "synthetic-passphrase")
	require.NoError(t, err)
	require.NotEmpty(t, token)
	require.True(t, returned.TotpEnabled)
}

type recoveryLoginUserRepo struct{ contentModerationTestUserRepo }

func (r *recoveryLoginUserRepo) GetByEmail(context.Context, string) (*User, error) {
	return r.user, nil
}

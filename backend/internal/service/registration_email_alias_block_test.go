//go:build unit

package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestIsRegistrationEmailAlias(t *testing.T) {
	for _, tc := range []struct {
		email   string
		blocked bool
	}{
		{"sa.m.ple@gmail.com", true},
		{"  Sa.Mple@GoogleMail.COM ", true},
		{"sample+tag@gmail.com", true},
		{"sample+tag@outlook.com", true},
		{"sample+tag@example.com", true},
		{"sample@gmail.com.", true},
		{"first.last@example.com.", true},
		{"sample@gmail.com", false},
		{"sample@googlemail.com", false},
		{"first.last@example.com", false},
		{"first.last@outlook.com", false},
		{"first.last@gmail.com.example.com", false},
		{"+alice@example.com", false},
		{"invalid", false},
	} {
		t.Run(tc.email, func(t *testing.T) {
			require.Equal(t, tc.blocked, IsRegistrationEmailAlias(tc.email))
		})
	}
}

func TestRegistrationEmailAliasBlockDefaults(t *testing.T) {
	for _, tc := range []struct {
		name, value string
		repoErr     error
		enabled     bool
	}{
		{name: "missing on existing deployment", enabled: true},
		{name: "enabled", value: "true", enabled: true},
		{name: "disabled", value: "false", enabled: false},
		{name: "invalid", value: "invalid", enabled: true},
		{name: "read failure", repoErr: errors.New("database unavailable"), enabled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			values := map[string]string{}
			if tc.value != "" {
				values[SettingKeyRegistrationEmailAliasBlockEnabled] = tc.value
			}
			svc := NewSettingService(&settingRepoStub{values: values, err: tc.repoErr}, &config.Config{})
			require.Equal(t, tc.enabled, svc.IsRegistrationEmailAliasBlockEnabled(context.Background()))
			if tc.repoErr == nil {
				settings, err := NewSettingService(&settingGetAllRepoStub{values: values}, &config.Config{}).GetAllSettings(context.Background())
				require.NoError(t, err)
				require.Equal(t, tc.enabled, settings.RegistrationEmailAliasBlockEnabled)
			}
		})
	}
}

func TestRegistrationEmailAliasBlockBeforeVerificationAndCreation(t *testing.T) {
	ctx := context.Background()
	for _, email := range []string{"sa.m.ple@gmail.com", "sample+tag@example.com", "sample@gmail.com."} {
		t.Run(email, func(t *testing.T) {
			// No mail/queue service or user repository: rejection must happen before
			// sending mail, verifying codes, looking up duplicates, or creating users.
			svc := newAuthService(nil, map[string]string{
				SettingKeyRegistrationEnabled: "true",
				SettingKeyEmailVerifyEnabled:  "true",
			}, nil, nil)
			_, _, err := svc.RegisterWithVerification(ctx, email, "password", "", "", "", "")
			require.ErrorIs(t, err, ErrEmailAliasNotAllowed)
			require.ErrorIs(t, svc.SendVerifyCode(ctx, email), ErrEmailAliasNotAllowed)
			_, err = svc.SendVerifyCodeAsync(ctx, email)
			require.ErrorIs(t, err, ErrEmailAliasNotAllowed)
			formatCheckedError := ErrEmailAliasNotAllowed
			if strings.HasSuffix(email, ".") {
				// OAuth parses addresses before policy checks and already rejects
				// trailing domain dots as invalid. No mail or account is created.
				formatCheckedError = ErrEmailVerifyRequired
			}
			_, err = svc.SendPendingOAuthVerifyCode(ctx, email)
			require.ErrorIs(t, err, formatCheckedError)
			_, _, err = svc.RegisterOAuthEmailAccount(ctx, email, "password", "123456", "", "oidc")
			require.ErrorIs(t, err, ErrEmailAliasNotAllowed)
			_, _, err = svc.RegisterVerifiedOAuthEmailAccount(ctx, email, "password", "", "google")
			require.ErrorIs(t, err, formatCheckedError)
			_, err = svc.createEmailOAuthUser(ctx, email, "sample", "google", "", "")
			require.ErrorIs(t, err, ErrEmailAliasNotAllowed)
		})
	}
}

func TestRegistrationEmailAliasBlockDisabledAndNormalDots(t *testing.T) {
	for _, tc := range []struct{ email, setting string }{
		{"sa.m.ple@gmail.com", "false"},
		{"sample+tag@example.com", "false"},
		{"sample@gmail.com.", "false"},
		{"first.last@example.com", "true"},
		{"sample@gmail.com", "true"},
	} {
		t.Run(tc.email, func(t *testing.T) {
			repo := &userRepoStub{nextID: 11}
			svc := newAuthService(repo, map[string]string{
				SettingKeyRegistrationEnabled:                "true",
				SettingKeyRegistrationEmailAliasBlockEnabled: tc.setting,
			}, nil, nil)
			_, user, err := svc.Register(context.Background(), tc.email, "password")
			require.NoError(t, err)
			require.Equal(t, tc.email, user.Email)
			require.Equal(t, 1, repo.guardedCreates)
		})
	}
}

func TestRegistrationEmailAliasBlockDoesNotBlockExistingLogins(t *testing.T) {
	ctx := context.Background()
	repo := &userRepoStub{}
	svc := newAuthService(repo, map[string]string{SettingKeyRegistrationEnabled: "true"}, nil, nil)
	hash, err := svc.HashPassword("password")
	require.NoError(t, err)
	repo.user = &User{ID: 11, Email: "sa.m.ple@gmail.com", PasswordHash: hash, Status: StatusActive}
	_, user, err := svc.Login(ctx, repo.user.Email, "password")
	require.NoError(t, err)
	require.Equal(t, repo.user, user)
	_, user, err = svc.LoginOrRegisterOAuth(ctx, repo.user.Email, "sample")
	require.NoError(t, err)
	require.Equal(t, repo.user, user)
	svc.refreshTokenCache = &refreshTokenCacheStub{}
	_, user, err = svc.LoginOrRegisterOAuthWithTokenPair(ctx, repo.user.Email, "sample", "", "", "oidc")
	require.NoError(t, err)
	require.Equal(t, repo.user, user)
	require.Empty(t, repo.created)
}

func TestRegistrationEmailAliasBlockLegacyOAuthAndGuard(t *testing.T) {
	ctx := context.Background()
	repo := &userRepoStub{}
	svc := newAuthService(repo, map[string]string{SettingKeyRegistrationEnabled: "true"}, nil, nil)
	svc.refreshTokenCache = &refreshTokenCacheStub{}
	_, _, err := svc.LoginOrRegisterOAuth(ctx, "sa.m.ple@gmail.com", "sample")
	require.ErrorIs(t, err, ErrEmailAliasNotAllowed)
	_, _, err = svc.LoginOrRegisterOAuthWithTokenPair(ctx, "sa.m.ple@gmail.com", "sample", "", "", "oidc")
	require.ErrorIs(t, err, ErrEmailAliasNotAllowed)
	require.ErrorIs(t, svc.createUserWithRegistrationEmailGuard(ctx, &User{Email: "sa.m.ple@gmail.com"}), ErrEmailAliasNotAllowed)
	require.Empty(t, repo.created)
}

package service

import (
	"context"
	"errors"
	"time"
)

// SessionSecurityStore is implemented by the shared Redis refresh cache. User
// generations are monotonic (not wall-clock based), so logout and a new login
// in the same second cannot invalidate each other. Existing test/legacy cache
// adapters remain source compatible; production must use the Redis adapter.
type SessionSecurityStore interface {
	GetSessionVersion(context.Context, int64) (int64, error)
	RevokeUserSessions(context.Context, int64) error
	RevokeSession(context.Context, string, time.Duration) error
	IsSessionRevoked(context.Context, string) (bool, error)
	GetRotatedRefreshToken(context.Context, string) (*RefreshTokenData, error)
	ConsumeRefreshToken(context.Context, string) (bool, error)
}

func (s *AuthService) sessionSecurityStore() SessionSecurityStore {
	store, _ := s.refreshTokenCache.(SessionSecurityStore)
	return store
}

func (s *AuthService) sessionVersion(ctx context.Context, userID int64) (int64, error) {
	if store := s.sessionSecurityStore(); store != nil {
		version, err := store.GetSessionVersion(ctx, userID)
		if err != nil {
			return 0, ErrServiceUnavailable
		}
		return version, nil
	}
	return 0, nil
}

// IsAccessSessionRevoked must be checked after signature/user verification by
// every access-token consumer, independently of optional IP/UA session binding.
func (s *AuthService) IsAccessSessionRevoked(ctx context.Context, claims *JWTClaims) (bool, error) {
	if claims == nil {
		return true, nil
	}
	store := s.sessionSecurityStore()
	if store == nil {
		return false, nil
	}
	version, err := store.GetSessionVersion(ctx, claims.UserID)
	if err != nil {
		return false, ErrServiceUnavailable
	}
	if version != claims.SessionVersion {
		return true, nil
	}
	if claims.SessionID == "" {
		return false, nil
	} // old access-only tokens use user-wide revocation
	revoked, err := store.IsSessionRevoked(ctx, claims.SessionID)
	if err != nil {
		return false, ErrServiceUnavailable
	}
	return revoked, nil
}

func (s *AuthService) checkRefreshSession(ctx context.Context, data *RefreshTokenData) error {
	revoked, err := s.IsAccessSessionRevoked(ctx, &JWTClaims{UserID: data.UserID, SessionID: data.FamilyID, SessionVersion: data.SessionVersion})
	if err != nil {
		return err
	}
	if revoked {
		return ErrTokenRevoked
	}
	return nil
}

func (s *AuthService) revocationTTL() time.Duration {
	ttl := time.Duration(s.GetAccessTokenExpiresIn()) * time.Second
	if refresh := time.Duration(s.cfg.JWT.RefreshTokenExpireDays) * 24 * time.Hour; refresh > ttl {
		ttl = refresh
	}
	return ttl + time.Minute
}

func (s *AuthService) revokeRotatedRefresh(ctx context.Context, hash string) error {
	store := s.sessionSecurityStore()
	if store == nil {
		return ErrRefreshTokenInvalid
	}
	data, err := store.GetRotatedRefreshToken(ctx, hash)
	if errors.Is(err, ErrRefreshTokenNotFound) {
		return ErrRefreshTokenInvalid
	}
	if err != nil {
		return ErrServiceUnavailable
	}
	if err = s.RevokeSessionFamily(ctx, data.FamilyID); err != nil {
		return ErrServiceUnavailable
	}
	return ErrRefreshTokenReused
}

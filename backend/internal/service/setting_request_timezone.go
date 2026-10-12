package service

import (
	"context"
	"errors"
	"strings"
	"time"
)

type cachedOpenAIRequestTimezoneEnabled struct {
	value     bool
	expiresAt int64
}

const openAIRequestTimezoneEnabledCacheTTL = 5 * time.Second

// GetOpenAIRequestTimezoneEnabled reads the request timezone switch (default off).
func (s *SettingService) GetOpenAIRequestTimezoneEnabled(ctx context.Context, fallback bool) bool {
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Err() != nil {
		return fallback
	}
	if s == nil || s.settingRepo == nil {
		return fallback
	}
	if cached, ok := s.openAIRequestTimezoneEnabledCache.Load().(*cachedOpenAIRequestTimezoneEnabled); ok && cached != nil {
		if time.Now().UnixNano() < cached.expiresAt {
			return cached.value
		}
	}
	resultCh := s.openAIRequestTimezoneEnabledSF.DoChan(SettingKeyOpenAIRequestTimezoneEnabled, func() (any, error) {
		snapshot := s.openAIRequestTimezoneEnabledCache.Load()
		if cached, ok := snapshot.(*cachedOpenAIRequestTimezoneEnabled); ok && cached != nil {
			if time.Now().UnixNano() < cached.expiresAt {
				return cached.value, nil
			}
		}
		dbCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		value, err := s.settingRepo.GetValue(dbCtx, SettingKeyOpenAIRequestTimezoneEnabled)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err != nil && !errors.Is(err, ErrSettingNotFound) {
			if cached, ok := s.openAIRequestTimezoneEnabledCache.Load().(*cachedOpenAIRequestTimezoneEnabled); ok && cached != nil {
				return cached.value, nil
			}
			return fallback, nil
		}
		enabled := fallback
		if err == nil && strings.TrimSpace(value) != "" {
			enabled = value == "true"
		}
		// An in-flight pre-write read must not overwrite invalidation or a
		// newer value and re-enable rewriting after an administrator disables it.
		s.openAIRequestTimezoneEnabledCache.CompareAndSwap(snapshot, &cachedOpenAIRequestTimezoneEnabled{
			value:     enabled,
			expiresAt: time.Now().Add(openAIRequestTimezoneEnabledCacheTTL).UnixNano(),
		})
		return enabled, nil
	})
	select {
	case <-ctx.Done():
		return fallback
	case result := <-resultCh:
		if v, ok := result.Val.(bool); ok && result.Err == nil {
			return v
		}
		return fallback
	}
}

func (s *SettingService) InvalidateOpenAIRequestTimezoneEnabledCache() {
	if s == nil {
		return
	}
	s.openAIRequestTimezoneEnabledSF.Forget(SettingKeyOpenAIRequestTimezoneEnabled)
	s.openAIRequestTimezoneEnabledCache.Store(&cachedOpenAIRequestTimezoneEnabled{expiresAt: 0})
}

func (s *OpenAIGatewayService) normalizeRequestTimezone(ctx context.Context, account *Account, body []byte, transport string) []byte {
	if account == nil || !account.IsOpenAI() || s == nil || !s.settingService.GetOpenAIRequestTimezoneEnabled(ctx, false) {
		return body
	}
	return normalizeOpenAIRequestLocale(ctx, account, body, transport)
}

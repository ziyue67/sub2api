package service

import (
	"context"
	"errors"
	"strings"
	"time"
)

type cachedOpenAIRequestTimezoneAuto struct {
	value     bool
	expiresAt int64
}

const openAIRequestTimezoneAutoCacheTTL = 5 * time.Second

// GetOpenAIRequestTimezoneAuto defaults missing/empty settings to true.
// fallback is used only when settings cannot be read.
func (s *SettingService) GetOpenAIRequestTimezoneAuto(ctx context.Context, fallback bool) bool {
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Err() != nil {
		return fallback
	}
	if s == nil || s.settingRepo == nil {
		return fallback
	}
	if cached, ok := s.openAIRequestTimezoneAutoCache.Load().(*cachedOpenAIRequestTimezoneAuto); ok && cached != nil {
		if time.Now().UnixNano() < cached.expiresAt {
			return cached.value
		}
	}
	resultCh := s.openAIRequestTimezoneAutoSF.DoChan(SettingKeyOpenAIRequestTimezoneAuto, func() (any, error) {
		snapshot := s.openAIRequestTimezoneAutoCache.Load()
		if cached, ok := snapshot.(*cachedOpenAIRequestTimezoneAuto); ok && cached != nil {
			if time.Now().UnixNano() < cached.expiresAt {
				return cached.value, nil
			}
		}
		dbCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		value, err := s.settingRepo.GetValue(dbCtx, SettingKeyOpenAIRequestTimezoneAuto)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err != nil && !errors.Is(err, ErrSettingNotFound) {
			if cached, ok := s.openAIRequestTimezoneAutoCache.Load().(*cachedOpenAIRequestTimezoneAuto); ok && cached != nil {
				return cached.value, nil
			}
			return fallback, nil
		}
		enabled := true
		if err == nil && strings.TrimSpace(value) != "" {
			enabled = value == "true"
		}
		// An in-flight pre-write read must not overwrite invalidation or a
		// newer value and re-enable rewriting after an administrator disables it.
		s.openAIRequestTimezoneAutoCache.CompareAndSwap(snapshot, &cachedOpenAIRequestTimezoneAuto{
			value:     enabled,
			expiresAt: time.Now().Add(openAIRequestTimezoneAutoCacheTTL).UnixNano(),
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

func (s *SettingService) InvalidateOpenAIRequestTimezoneAutoCache() {
	if s == nil {
		return
	}
	s.openAIRequestTimezoneAutoSF.Forget(SettingKeyOpenAIRequestTimezoneAuto)
	s.openAIRequestTimezoneAutoCache.Store(&cachedOpenAIRequestTimezoneAuto{expiresAt: 0})
}

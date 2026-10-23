package service

import "context"

// InvalidateAuthCacheByKey 清除指定 API Key 的认证缓存
func (s *APIKeyService) InvalidateAuthCacheByKey(ctx context.Context, key string) {
	if key == "" {
		return
	}
	cacheKey := s.authCacheKey(key)
	s.deleteAuthCache(ctx, cacheKey)
}

// InvalidateAuthCacheByUserID 清除用户相关的 API Key 认证缓存
func (s *APIKeyService) InvalidateAuthCacheByUserID(ctx context.Context, userID int64) {
	if userID <= 0 {
		return
	}
	keys, err := s.apiKeyRepo.ListKeysByUserID(ctx, userID)
	if err != nil {
		return
	}
	s.deleteAuthCacheByKeys(ctx, keys)
}

// InvalidateAuthCacheByGroupID 清除分组相关的 API Key 认证缓存
func (s *APIKeyService) InvalidateAuthCacheByGroupID(ctx context.Context, groupID int64) {
	if groupID <= 0 {
		return
	}
	keys, err := s.apiKeyRepo.ListKeysByGroupID(ctx, groupID)
	if err != nil {
		return
	}
	s.deleteAuthCacheByKeys(ctx, keys)
}

func (s *APIKeyService) deleteAuthCacheByKeys(ctx context.Context, keys []string) {
	if len(keys) == 0 {
		return
	}
	for _, key := range keys {
		if key == "" {
			continue
		}
		s.deleteAuthCache(ctx, s.authCacheKey(key))
	}
}

// InvalidateAuthCacheByUserIDChecked reports failures to administrative recovery
// callers: a committed status change must not be presented as cache-confirmed.
func (s *APIKeyService) InvalidateAuthCacheByUserIDChecked(ctx context.Context, userID int64) error {
	keys, err := s.apiKeyRepo.ListKeysByUserID(ctx, userID)
	if err != nil {
		return err
	}
	for _, key := range keys {
		if key == "" {
			continue
		}
		cacheKey := s.authCacheKey(key)
		if s.authCacheL1 != nil {
			s.authCacheL1.Del(cacheKey)
		}
		if s.authNegativeCacheL1 != nil {
			s.authNegativeCacheL1.Del(cacheKey)
		}
		if s.cache != nil {
			if err = s.cache.DeleteAuthCache(ctx, cacheKey); err != nil {
				return err
			}
			if err = s.cache.PublishAuthCacheInvalidation(ctx, cacheKey); err != nil {
				return err
			}
		}
	}
	return nil
}

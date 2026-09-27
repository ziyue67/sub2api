package service

import "context"

// UsageFilterOption contains labels only, never credentials or account settings.
type UsageFilterOption struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

func (s *UsageService) OwnUsageFilterOptions(ctx context.Context, userID int64, kind, query string, includeErrors bool) ([]UsageFilterOption, error) {
	if repo, ok := s.usageRepo.(interface {
		OwnUsageFilterOptions(context.Context, int64, string, string, bool) ([]UsageFilterOption, error)
	}); ok {
		return repo.OwnUsageFilterOptions(ctx, userID, kind, query, includeErrors)
	}
	return []UsageFilterOption{}, nil
}

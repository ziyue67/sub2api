package service

import (
	"context"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"slices"
	"time"
)

type ProfitOperationsSnapshot struct {
	UpstreamOperationsSnapshot
	Currency string `json:"currency"`
}

type ProfitOperationsRepository interface {
	ReadProfitOperations(context.Context, UpstreamOperationsFilter) (*ProfitOperationsSnapshot, error)
}

func ValidateProfitOperationsFilter(f UpstreamOperationsFilter) error {
	if !slices.Contains([]string{"requests", "standard_charge", "request_income", "estimated_cost", "estimated_profit", "margin", "name"}, f.Sort) {
		return ErrUpstreamOperationsValidation
	}
	f.Sort = "requests"
	return ValidateUpstreamOperationsFilter(f)
}
func (s *ChannelService) ProfitOperations(ctx context.Context, f UpstreamOperationsFilter) (*ProfitOperationsSnapshot, error) {
	if err := ValidateProfitOperationsFilter(f); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	repo, ok := s.repo.(ProfitOperationsRepository)
	if !ok {
		return nil, infraerrors.ServiceUnavailable("PROFIT_OPERATIONS_UNAVAILABLE", "Profit operations data source unavailable")
	}
	return repo.ReadProfitOperations(ctx, f)
}

// OperationsCostMultiplier distinguishes the scheduling default from a saved
// estimate, and corrupt legacy metadata from an explicit zero cost.
func OperationsCostMultiplier(a *Account) (*float64, string) {
	if a == nil {
		return nil, "account_missing"
	}
	raw, present := a.Extra[AccountCostMultiplierExtraKey]
	if !present || raw == nil {
		v := DefaultAccountCostMultiplier
		return &v, "current_default_0.1"
	}
	v, ok := accountCostMultiplierNumber(raw)
	if !ok {
		return nil, "invalid_multiplier"
	}
	return &v, "current_saved_multiplier"
}

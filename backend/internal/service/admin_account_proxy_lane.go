package service

import (
	"context"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// ErrAccountProxyLanesUnavailable is returned when the running repository was
// built before the account-proxy-lanes migration or does not expose the
// optional lane capability.
// It is deliberately a 503 rather than a generic 500 so the admin UI can
// distinguish a rolling-upgrade window from invalid lane input.
var ErrAccountProxyLanesUnavailable = infraerrors.ServiceUnavailable(
	"ACCOUNT_PROXY_LANES_UNAVAILABLE",
	"account proxy lane management is unavailable until the account-proxy-lanes migration is applied",
)

// laneRepository returns the optional persistence capability without widening
// AccountRepository.  This keeps all existing read-only repository stubs
// source-compatible while making an unsupported deployment explicit.
func (s *adminServiceImpl) laneRepository() (AccountProxyLaneRepository, error) {
	if s == nil || s.accountRepo == nil {
		return nil, ErrAccountProxyLanesUnavailable
	}
	repo, ok := s.accountRepo.(AccountProxyLaneRepository)
	if !ok || repo == nil {
		return nil, ErrAccountProxyLanesUnavailable
	}
	return repo, nil
}

func (s *adminServiceImpl) validateLaneAccount(ctx context.Context, accountID int64) (*Account, error) {
	if accountID <= 0 {
		return nil, infraerrors.BadRequest("INVALID_ACCOUNT_ID", "account id must be positive")
	}
	if s == nil || s.accountRepo == nil {
		return nil, ErrAccountProxyLanesUnavailable
	}
	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if account == nil {
		return nil, ErrAccountNotFound
	}
	return account, nil
}

func (s *adminServiceImpl) validateAndHydrateLane(ctx context.Context, accountID int64, lane *AccountProxyLane) (*AccountProxyLane, error) {
	if lane == nil {
		return nil, infraerrors.BadRequest("INVALID_ACCOUNT_PROXY_LANE", "lane payload is required")
	}
	copyLane := lane.Normalize()
	copyLane.AccountID = accountID
	if err := copyLane.ValidateForAccount(accountID); err != nil {
		return nil, infraerrors.BadRequest("INVALID_ACCOUNT_PROXY_LANE", err.Error())
	}
	if copyLane.ProxyID != nil {
		if s == nil || s.proxyRepo == nil {
			return nil, ErrAccountProxyLanesUnavailable
		}
		proxy, err := s.proxyRepo.GetByID(ctx, *copyLane.ProxyID)
		if err != nil {
			return nil, err
		}
		if proxy == nil {
			return nil, ErrProxyNotFound
		}
		if !proxy.IsActive() || proxy.IsExpired(time.Now()) {
			return nil, infraerrors.BadRequest("ACCOUNT_PROXY_LANE_PROXY_UNAVAILABLE", "proxy is inactive or expired")
		}
		copyLane.Proxy = proxy
	}
	return &copyLane, nil
}

// ListAccountProxyLanes implements AccountProxyLaneAdminService.
func (s *adminServiceImpl) ListAccountProxyLanes(ctx context.Context, accountID int64) ([]AccountProxyLane, error) {
	if _, err := s.validateLaneAccount(ctx, accountID); err != nil {
		return nil, err
	}
	repo, err := s.laneRepository()
	if err != nil {
		return nil, err
	}
	lanes, err := repo.ListProxyLanes(ctx, accountID)
	if err != nil {
		return nil, err
	}
	return sanitizeAccountProxyLanes(lanes), nil
}

// CreateAccountProxyLane implements AccountProxyLaneAdminService.
func (s *adminServiceImpl) CreateAccountProxyLane(ctx context.Context, accountID int64, lane *AccountProxyLane) (*AccountProxyLane, error) {
	if _, err := s.validateLaneAccount(ctx, accountID); err != nil {
		return nil, err
	}
	repo, err := s.laneRepository()
	if err != nil {
		return nil, err
	}
	normalized, err := s.validateAndHydrateLane(ctx, accountID, lane)
	if err != nil {
		return nil, err
	}
	// Enforce the same case-insensitive name/proxy uniqueness rules used by
	// bulk validation against rows already persisted for this account.  The
	// database's historical (account_id,name) index is case-sensitive, so
	// relying on it alone would allow visually duplicate lanes ("Edge"/"edge").
	if err := s.validateLaneUniqueness(ctx, repo, accountID, normalized, 0); err != nil {
		return nil, err
	}
	if err := repo.CreateProxyLane(ctx, normalized); err != nil {
		return nil, translateAccountProxyLaneWriteError(err)
	}
	sanitized := sanitizeAccountProxyLane(*normalized)
	return &sanitized, nil
}

// UpdateAccountProxyLane implements AccountProxyLaneAdminService.
func (s *adminServiceImpl) UpdateAccountProxyLane(ctx context.Context, accountID, laneID int64, lane *AccountProxyLane) (*AccountProxyLane, error) {
	if accountID <= 0 || laneID <= 0 {
		return nil, infraerrors.BadRequest("INVALID_ACCOUNT_PROXY_LANE_ID", "account and lane ids must be positive")
	}
	if _, err := s.validateLaneAccount(ctx, accountID); err != nil {
		return nil, err
	}
	repo, err := s.laneRepository()
	if err != nil {
		return nil, err
	}
	normalized, err := s.validateAndHydrateLane(ctx, accountID, lane)
	if err != nil {
		return nil, err
	}
	normalized.ID = laneID
	if err := s.validateLaneUniqueness(ctx, repo, accountID, normalized, laneID); err != nil {
		return nil, err
	}
	if err := repo.UpdateProxyLane(ctx, normalized); err != nil {
		return nil, translateAccountProxyLaneWriteError(err)
	}
	sanitized := sanitizeAccountProxyLane(*normalized)
	return &sanitized, nil
}

// validateLaneUniqueness checks a candidate against persisted lanes while
// excluding the row being updated.  It deliberately runs before the write so
// callers receive a stable 409/400 error instead of a raw driver message.
func (s *adminServiceImpl) validateLaneUniqueness(ctx context.Context, repo AccountProxyLaneRepository, accountID int64, candidate *AccountProxyLane, excludeID int64) error {
	if repo == nil || candidate == nil {
		return nil
	}
	existing, err := repo.ListProxyLanes(ctx, accountID)
	if err != nil {
		return err
	}
	combined := make([]AccountProxyLane, 0, len(existing)+1)
	for _, lane := range existing {
		if excludeID > 0 && lane.ID == excludeID {
			continue
		}
		combined = append(combined, lane)
	}
	combined = append(combined, *candidate)
	if err := ValidateAccountProxyLanes(accountID, combined); err != nil {
		return infraerrors.Conflict("ACCOUNT_PROXY_LANE_CONFLICT", err.Error())
	}
	return nil
}

// DeleteAccountProxyLane implements AccountProxyLaneAdminService.
func (s *adminServiceImpl) DeleteAccountProxyLane(ctx context.Context, accountID, laneID int64) error {
	if accountID <= 0 || laneID <= 0 {
		return infraerrors.BadRequest("INVALID_ACCOUNT_PROXY_LANE_ID", "account and lane ids must be positive")
	}
	if _, err := s.validateLaneAccount(ctx, accountID); err != nil {
		return err
	}
	repo, err := s.laneRepository()
	if err != nil {
		return err
	}
	lanes, err := repo.ListProxyLanes(ctx, accountID)
	if err != nil {
		return err
	}
	found := false
	for _, lane := range lanes {
		if lane.ID == laneID {
			found = true
			break
		}
	}
	if !found {
		return ErrAccountProxyLaneNotFound
	}
	if err := repo.DeleteProxyLane(ctx, accountID, laneID); err != nil {
		return translateAccountProxyLaneWriteError(err)
	}
	return nil
}

func translateAccountProxyLaneWriteError(err error) error {
	if err == nil {
		return nil
	}
	// PostgreSQL's unique/FK/check errors are intentionally not exposed as raw
	// driver strings.  Keep the actionable message while mapping common cases
	// to stable HTTP statuses; the handler's response envelope remains uniform.
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, `relation "account_proxy_lanes" does not exist`),
		strings.Contains(msg, "no such table") && strings.Contains(msg, "account_proxy_lanes"):
		return ErrAccountProxyLanesUnavailable
	case strings.Contains(msg, "duplicate key"), strings.Contains(msg, "unique constraint"):
		return infraerrors.Conflict("ACCOUNT_PROXY_LANE_CONFLICT", "lane name or proxy is already mounted on this account").WithCause(err)
	case strings.Contains(msg, "foreign key"), strings.Contains(msg, "violates check constraint"):
		return infraerrors.BadRequest("INVALID_ACCOUNT_PROXY_LANE", "lane references an invalid account or proxy").WithCause(err)
	case err == ErrAccountNotFound:
		return ErrAccountProxyLaneNotFound
	default:
		return err
	}
}

// sanitizeAccountProxyLane strips proxy credentials before an admin response.
// The scheduler still receives the full proxy object from the repository; only
// the HTTP representation is redacted.
func sanitizeAccountProxyLane(lane AccountProxyLane) AccountProxyLane {
	if lane.Proxy != nil {
		proxy := *lane.Proxy
		proxy.Username = ""
		proxy.Password = ""
		lane.Proxy = &proxy
	}
	return lane
}

func sanitizeAccountProxyLanes(lanes []AccountProxyLane) []AccountProxyLane {
	if lanes == nil {
		return []AccountProxyLane{}
	}
	out := make([]AccountProxyLane, len(lanes))
	for i := range lanes {
		out[i] = sanitizeAccountProxyLane(lanes[i])
	}
	return out
}

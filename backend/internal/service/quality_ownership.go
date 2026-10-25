package service

import (
	"context"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const QualityScopeConflictReason = "QUALITY_ISOLATION_SCOPE_CONFLICT"

func QualityScopeConflict(message string) error {
	return infraerrors.Conflict(QualityScopeConflictReason, message)
}

// Only enumerations, business IDs and opaque digests cross this boundary.
// No raw limit, account Extra, credential or prompt is a public field.
type QualityOwnershipEntry struct {
	Kind               string   `json:"kind"`
	Model              string   `json:"model,omitempty"`
	GroupID            int64    `json:"group_id,omitempty"`
	Status             string   `json:"status"`
	ExpectedPresent    bool     `json:"expected_present"`
	CurrentPresent     bool     `json:"current_present"`
	ExpectedOwner      string   `json:"expected_owner"`
	CurrentOwner       string   `json:"current_owner"`
	CurrentOwnerRuleID int64    `json:"current_owner_rule_id,omitempty"`
	ExpectedDigest     string   `json:"expected_digest"`
	CurrentDigest      string   `json:"current_digest"`
	ChangedFields      []string `json:"changed_fields"`
	CanRelinquish      bool     `json:"can_relinquish"`
}
type QualityOwnershipPreview struct {
	PlanID              int64                   `json:"plan_id"`
	AccountID           int64                   `json:"account_id"`
	RuleVersion         string                  `json:"rule_version"`
	AccountVersion      string                  `json:"account_version"`
	Fence               string                  `json:"fence"`
	Action              string                  `json:"action"`
	Models              []string                `json:"models"`
	GroupIDs            []int64                 `json:"group_ids"`
	Entries             []QualityOwnershipEntry `json:"entries"`
	RelinquishableCount int                     `json:"relinquishable_count"`
	OwnershipRetained   bool                    `json:"ownership_retained"`
	RestrictionsChanged bool                    `json:"restrictions_changed"`
}
type QualityOwnershipRequest struct {
	Fence   string `json:"fence"`
	Confirm bool   `json:"confirm"`
}
type QualityOwnershipRepository interface {
	QualityOwnership(context.Context, int64, *QualityOwnershipRequest) (*QualityOwnershipPreview, error)
}

func (s *ScheduledTestService) QualityOwnership(ctx context.Context, id int64, request *QualityOwnershipRequest) (*QualityOwnershipPreview, error) {
	repo, ok := s.planRepo.(QualityOwnershipRepository)
	if !ok {
		return nil, infraerrors.ServiceUnavailable("QUALITY_OWNERSHIP_UNAVAILABLE", "quality ownership inspection is unavailable")
	}
	if request != nil && (!request.Confirm || len(request.Fence) != 64) {
		return nil, infraerrors.BadRequest("QUALITY_OWNERSHIP_CONFIRM_REQUIRED", "preview and explicitly confirm ownership reconciliation")
	}
	return repo.QualityOwnership(ctx, id, request)
}

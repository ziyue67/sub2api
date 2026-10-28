package service

import (
	"context"
	"strings"

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

// Model removals are bounded to one configured model per confirmation. The
// isolation key can differ from the requested model through account mapping.
type QualityModelRemoval struct {
	Model          string `json:"model"`
	IsolationModel string `json:"isolation_model"`
	Ready          bool   `json:"ready"`
	Effect         string `json:"effect"`
	BlockedReason  string `json:"blocked_reason,omitempty"`
}
type QualityOwnershipPreview struct {
	ModelRemovals        []QualityModelRemoval   `json:"model_removals"`
	PlanID               int64                   `json:"plan_id"`
	AccountID            int64                   `json:"account_id"`
	RuleVersion          string                  `json:"rule_version"`
	AccountVersion       string                  `json:"account_version"`
	Fence                string                  `json:"fence"`
	Action               string                  `json:"action"`
	Models               []string                `json:"models"`
	GroupIDs             []int64                 `json:"group_ids"`
	Entries              []QualityOwnershipEntry `json:"entries"`
	RelinquishableCount  int                     `json:"relinquishable_count"`
	OwnershipRetained    bool                    `json:"ownership_retained"`
	RestoreReady         bool                    `json:"restore_ready"`
	RestoreBlockedReason string                  `json:"restore_blocked_reason,omitempty"`
	RestrictionsChanged  bool                    `json:"restrictions_changed"`
}
type QualityOwnershipRequest struct {
	RemoveModel bool   `json:"-"`
	Model       string `json:"model"`
	ReasonCode  string `json:"reason_code"`
	Fence       string `json:"fence"`
	Confirm     bool   `json:"confirm"`
	Restore     bool   `json:"-"`
	Delete      bool   `json:"-"`
}
type QualityOwnershipRepository interface {
	QualityOwnership(context.Context, int64, *QualityOwnershipRequest) (*QualityOwnershipPreview, error)
}

func (s *ScheduledTestService) QualityOwnership(ctx context.Context, id int64, request *QualityOwnershipRequest) (*QualityOwnershipPreview, error) {
	if request != nil && (request.Restore || request.Delete || request.RemoveModel) {
		if _, observer := ObserverGroupIDs(ctx); observer {
			return nil, ErrObserverScope
		}
	}
	repo, ok := s.planRepo.(QualityOwnershipRepository)
	if !ok {
		return nil, infraerrors.ServiceUnavailable("QUALITY_OWNERSHIP_UNAVAILABLE", "quality ownership inspection is unavailable")
	}
	if request != nil && (!request.Confirm || len(request.Fence) != 64) {
		return nil, infraerrors.BadRequest("QUALITY_OWNERSHIP_CONFIRM_REQUIRED", "preview and explicitly confirm ownership reconciliation")
	}
	if request != nil && request.RemoveModel && (strings.TrimSpace(request.Model) == "" || len(request.Model) > 100 ||
		(request.ReasonCode != "orphan_marker_recovery" && request.ReasonCode != "retire_model")) {
		return nil, infraerrors.BadRequest("QUALITY_MODEL_REMOVAL_REASON_REQUIRED", "select one model and an explicit removal reason")
	}
	return repo.QualityOwnership(ctx, id, request)
}

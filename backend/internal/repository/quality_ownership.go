package repository

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func qualityDigest(value any) string {
	data, _ := json.Marshal(value)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
func qualityEntryOwner(raw json.RawMessage, planID int64) (string, int64) {
	if len(raw) == 0 || string(raw) == "null" {
		return "absent", 0
	}
	var entry map[string]json.RawMessage
	if json.Unmarshal(raw, &entry) != nil || entry == nil {
		return "unrecognized", 0
	}
	var reason string
	if json.Unmarshal(entry["reason"], &reason) != nil || strings.TrimSpace(reason) == "" {
		return "unrecognized", 0
	}
	id, err := strconv.ParseInt(strings.TrimPrefix(reason, "quality_rule:"), 10, 64)
	if strings.HasPrefix(reason, "quality_rule:") && err == nil && id > 0 {
		if id == planID {
			return "this_rule", id
		}
		return "other_rule", id
	}
	return "external", 0
}
func canonicalQualityEntry(raw json.RawMessage) any {
	var value any
	if len(raw) > 0 {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		_ = decoder.Decode(&value)
	}
	return value
}
func qualityChangedFields(expected, current json.RawMessage) []string {
	a, b := map[string]json.RawMessage{}, map[string]json.RawMessage{}
	_ = json.Unmarshal(expected, &a)
	_ = json.Unmarshal(current, &b)
	names := map[string]bool{}
	for k := range a {
		names[k] = true
	}
	for k := range b {
		names[k] = true
	}
	changes := map[string]bool{}
	for k := range names {
		if qualityDigest(canonicalQualityEntry(a[k])) != qualityDigest(canonicalQualityEntry(b[k])) || (len(a[k]) == 0) != (len(b[k]) == 0) {
			switch k {
			case "reason", "isolated_at", "rate_limited_at", "rate_limit_reset_at", "priority", "created_at", "allowed_models", "group_id", "account_id":
				changes[k] = true
			default:
				changes["other_fields"] = true
			}
		}
	}
	out := []string{}
	for k := range changes {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
func modelOwnershipEntry(model string, expected, current json.RawMessage, id int64) service.QualityOwnershipEntry {
	owner, ownerID := qualityEntryOwner(current, id)
	expectedOwner, _ := qualityEntryOwner(expected, id)
	entry := service.QualityOwnershipEntry{Kind: "model", Model: model, ExpectedPresent: len(expected) > 0, CurrentPresent: owner != "absent", ExpectedOwner: "this_rule", CurrentOwner: owner, CurrentOwnerRuleID: ownerID,
		ExpectedDigest: qualityDigest(canonicalQualityEntry(expected)), CurrentDigest: qualityDigest(canonicalQualityEntry(current)), ChangedFields: qualityChangedFields(expected, current)}
	if expectedOwner != "this_rule" {
		entry.ExpectedOwner = expectedOwner
	}
	switch {
	case expectedOwner != "this_rule":
		entry.Status = "unrecognized"
	case owner == "absent":
		entry.Status = "missing"
		entry.CanRelinquish = true
	case qualityDigest(canonicalQualityEntry(expected)) == qualityDigest(canonicalQualityEntry(current)):
		entry.Status = "managed"
	case owner == "other_rule" || owner == "external":
		entry.Status = "external_owner"
		entry.CanRelinquish = true
	case owner == "this_rule":
		entry.Status = "changed_same_owner"
	default:
		entry.Status = "unrecognized"
	}
	return entry
}

// All operations rebuild consent under plan -> account -> state locks. Only
// explicit restoration/removal may change account restrictions; reconciliation
// continues to retain live restrictions.
func (r *scheduledTestPlanRepository) QualityOwnership(ctx context.Context, id int64, request *service.QualityOwnershipRequest) (*service.QualityOwnershipPreview, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `SET LOCAL lock_timeout='2s'`); err != nil {
		return nil, err
	}
	var accountID int64
	var ruleVersion, accountVersion time.Time
	var running sql.NullTime
	var config, rawState, liveRaw, credentials []byte
	var primaryModel string
	var accountStatus, accountPlatform, accountType string
	var accountExpires sql.NullTime
	err = tx.QueryRowContext(ctx, `SELECT account_id,updated_at,running_until,pelican_config,model_id FROM scheduled_test_plans WHERE id=$1 FOR UPDATE`, id).Scan(&accountID, &ruleVersion, &running, &config, &primaryModel)
	if err == sql.ErrNoRows {
		if request != nil && request.Delete {
			return &service.QualityOwnershipPreview{PlanID: id}, tx.Commit()
		}
		return nil, infraerrors.NotFound("QUALITY_PLAN_NOT_FOUND", "quality rule not found")
	}
	if err != nil {
		return nil, err
	}
	var cfg service.PelicanTestConfig
	if json.Unmarshal(config, &cfg) != nil || cfg.Quality == nil || cfg.Quality.Action == service.QualityActionOpenAITwoFAAdmission {
		return nil, infraerrors.BadRequest("QUALITY_OWNERSHIP_UNSUPPORTED", "ownership inspection requires an editable quality rule")
	}
	err = tx.QueryRowContext(ctx, `SELECT updated_at,COALESCE(extra->'quality_model_isolation','{}'::jsonb),status,expires_at,credentials,platform,type FROM accounts WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, accountID).Scan(&accountVersion, &liveRaw, &accountStatus, &accountExpires, &credentials, &accountPlatform, &accountType)
	if err == sql.ErrNoRows {
		return nil, infraerrors.NotFound("QUALITY_ACCOUNT_NOT_FOUND", "account not found")
	}
	if err != nil {
		return nil, err
	}
	err = tx.QueryRowContext(ctx, `SELECT state FROM account_quality_states WHERE plan_id=$1 FOR UPDATE`, id).Scan(&rawState)
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	var state qualityState
	if len(rawState) > 0 {
		decoder := json.NewDecoder(bytes.NewReader(rawState))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&state) != nil {
			return nil, infraerrors.Conflict("QUALITY_OWNERSHIP_UNSUPPORTED_STATE", "ownership contains unrecognized state; retain it for review")
		}
	}
	// Never drop a legacy restriction simply because it is in a recognizable
	// JSON field outside the supported composite Models/Groups ownership layout.
	if state.PersistentModels || len(state.ModelApplied) > 0 || len(state.ModelRateLimits) > 0 || len(state.Removed) > 0 || state.PreviousConcurrency != nil || state.AppliedConcurrency != nil || state.RecoveryTarget != nil || len(state.BPSApplied) > 0 || len(state.BPSPrevious) > 0 ||
		(state.Groups != nil && (state.Groups.Action != "remove_groups" || len(state.Groups.ModelApplied) > 0 || len(state.Groups.ModelRateLimits) > 0 || state.Groups.Models != nil || state.Groups.Groups != nil)) ||
		(state.Models != nil && (state.Models.Models != nil || state.Models.Groups != nil || len(state.Models.Removed) > 0)) {
		return nil, infraerrors.Conflict("QUALITY_OWNERSHIP_UNSUPPORTED_STATE", "retain unsupported ownership layout for review")
	}
	if state.Action != "" && state.Action != service.QualityActionIsolateModels {
		return nil, infraerrors.Conflict("QUALITY_OWNERSHIP_UNSUPPORTED_STATE", "only persistent model isolation supports reconciliation")
	}
	live := map[string]json.RawMessage{}
	if json.Unmarshal(liveRaw, &live) != nil {
		return nil, infraerrors.Conflict("QUALITY_OWNERSHIP_UNSUPPORTED_STATE", "isolation entries are not readable")
	}
	// Lock membership rows before comparison; account FOR UPDATE prevents new
	// FK-backed memberships while these row locks fence deletes/edits.
	groupRows, lockErr := tx.QueryContext(ctx, `SELECT group_id FROM account_groups WHERE account_id=$1 ORDER BY group_id FOR UPDATE`, accountID)
	if lockErr != nil {
		return nil, lockErr
	}
	for groupRows.Next() {
		var groupID int64
		if err = groupRows.Scan(&groupID); err != nil {
			_ = groupRows.Close()
			return nil, err
		}
	}
	if err = groupRows.Err(); err != nil {
		_ = groupRows.Close()
		return nil, err
	}
	_ = groupRows.Close()
	groups, err := qualityGroups(ctx, tx, accountID)
	if err != nil {
		return nil, err
	}
	var currentGroups []qualityGroup
	if json.Unmarshal(groups, &currentGroups) != nil {
		return nil, fmt.Errorf("invalid group membership state")
	}
	view := &service.QualityOwnershipPreview{PlanID: id, AccountID: accountID, RuleVersion: ruleVersion.UTC().Format(time.RFC3339Nano), AccountVersion: accountVersion.UTC().Format(time.RFC3339Nano), Action: state.Action, Models: append([]string{}, state.IsolationModels...), GroupIDs: append([]int64{}, state.IsolationGroupIDs...), Entries: []service.QualityOwnershipEntry{}}
	if state.Models != nil {
		// Legacy concurrency/cooldown ownership is not silently folded into this API.
		if !state.Models.PersistentModels || state.Models.PreviousConcurrency != nil || state.Models.AppliedConcurrency != nil || state.Models.RecoveryTarget != nil || len(state.Models.BPSApplied) > 0 || len(state.Models.BPSPrevious) > 0 {
			return nil, infraerrors.Conflict("QUALITY_OWNERSHIP_UNSUPPORTED_STATE", "retain legacy concurrency ownership for review")
		}
		keys := map[string]bool{}
		for model := range state.Models.ModelApplied {
			keys[model] = true
		}
		for model := range state.Models.ModelRateLimits {
			keys[model] = true
		}
		names := []string{}
		for model := range keys {
			names = append(names, model)
		}
		sort.Strings(names)
		for _, model := range names {
			entry := modelOwnershipEntry(model, state.Models.ModelApplied[model], live[model], id)
			if len(state.Models.ModelApplied[model]) == 0 || !json.Valid(state.Models.ModelRateLimits[model]) || canonicalQualityEntry(state.Models.ModelRateLimits[model]) != nil {
				entry.Status = "unrecognized"
				entry.CanRelinquish = false
			}
			view.Entries = append(view.Entries, entry)
		}
	}
	// A removed membership still absent in a live group remains our restriction.
	// An externally recreated membership is not evidence of model restoration.
	// Group snapshots are preview-only: dropping them while retaining the target
	// scope could let the next failed round remove a manually recreated membership.
	if state.Groups != nil {
		for _, expected := range state.Groups.Removed {
			var exists bool
			if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM (SELECT id FROM groups WHERE id=$1 AND deleted_at IS NULL FOR SHARE) locked_group)`, expected.GroupID).Scan(&exists); err != nil {
				return nil, err
			}
			var current *qualityGroup
			for i := range currentGroups {
				if currentGroups[i].GroupID == expected.GroupID {
					current = &currentGroups[i]
					break
				}
			}
			entry := service.QualityOwnershipEntry{Kind: "group", GroupID: expected.GroupID, Status: "managed", ExpectedPresent: false, CurrentPresent: current != nil, ExpectedOwner: "this_rule", CurrentOwner: "this_rule", ExpectedDigest: qualityDigest(expected), CurrentDigest: qualityDigest(current), ChangedFields: []string{}}
			if !exists {
				entry.Status = "group_missing"
				entry.CurrentOwner = "absent"
				entry.CanRelinquish = false
				entry.ChangedFields = []string{"group_exists"}
			}
			if current != nil {
				entry.Status = "external_membership_present"
				entry.CurrentOwner = "external"
				entry.CanRelinquish = false
				expectedJSON, _ := json.Marshal(expected)
				currentJSON, _ := json.Marshal(current)
				entry.ChangedFields = append([]string{"membership_present"}, qualityChangedFields(expectedJSON, currentJSON)...)
			}
			// Another rule's saved removal is an ownership conflict even when the
			// membership remains absent. Never restore over its independent restriction.
			var otherOwner int64
			ownerErr := tx.QueryRowContext(ctx, `SELECT p.id FROM scheduled_test_plans p JOIN account_quality_states s ON s.plan_id=p.id
    WHERE p.account_id=$1 AND p.id<>$2 AND (
     EXISTS(SELECT 1 FROM jsonb_array_elements(COALESCE(NULLIF(s.state->'removed','null'::jsonb),'[]'::jsonb)) e WHERE (e->>'group_id')::bigint=$3) OR
     EXISTS(SELECT 1 FROM jsonb_array_elements(COALESCE(NULLIF(s.state->'groups'->'removed','null'::jsonb),'[]'::jsonb)) e WHERE (e->>'group_id')::bigint=$3)) ORDER BY p.id LIMIT 1`, accountID, id, expected.GroupID).Scan(&otherOwner)
			if ownerErr != nil && ownerErr != sql.ErrNoRows {
				return nil, ownerErr
			}
			if ownerErr == nil {
				entry.Status = "external_owner"
				entry.CurrentOwner = "other_rule"
				entry.CurrentOwnerRuleID = otherOwner
				entry.CanRelinquish = false
				entry.ChangedFields = append(entry.ChangedFields, "ownership")
			}

			view.Entries = append(view.Entries, entry)
		}
	}
	plan := &service.ScheduledTestPlan{ID: id, AccountID: accountID, ModelID: primaryModel, PelicanConfig: &cfg}
	account := &service.Account{Platform: accountPlatform, Type: accountType}
	if len(credentials) > 0 && json.Unmarshal(credentials, &account.Credentials) != nil {
		return nil, infraerrors.Conflict("QUALITY_OWNERSHIP_UNSUPPORTED_STATE", "model mapping is not readable")
	}
	relevant := map[string]bool{}
	for _, model := range service.QualityModels(plan) {
		relevant[model] = true
		relevant[account.GetMappedModel(model)] = true
	}
	seen := map[string]bool{}
	for _, entry := range view.Entries {
		if entry.Kind == "model" {
			seen[entry.Model] = true
		}
	}
	// Inspect actual unrecorded data too, including unknown/no-owner and foreign
	// markers on configured keys. A same-owner orphan must never disappear from
	// deletion/restoration guards. All raw fields remain internal.
	for model, raw := range live {
		owner, _ := qualityEntryOwner(raw, id)
		if !seen[model] && (owner == "this_rule" || relevant[model]) {
			entry := modelOwnershipEntry(model, nil, raw, id)
			entry.Status = "unrecognized"
			if owner == "this_rule" {
				entry.Status = "orphan_same_owner"
			}
			entry.CanRelinquish = false
			view.Entries = append(view.Entries, entry)
		}
	}
	sort.Slice(view.Entries, func(i, j int) bool {
		a, b := view.Entries[i], view.Entries[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Model != b.Model {
			return a.Model < b.Model
		}
		return a.GroupID < b.GroupID
	})

	view.RestoreReady = state.Action == service.QualityActionIsolateModels
	for _, entry := range view.Entries {
		if entry.Status != "managed" && (entry.Kind != "model" || entry.Status != "missing") {
			view.RestoreReady = false
			view.RestoreBlockedReason = "ownership_conflict"
		}
	}
	if accountStatus != "active" || accountExpires.Valid && !accountExpires.Time.After(time.Now()) {
		view.RestoreReady = false
		view.RestoreBlockedReason = "account_unavailable"
	}
	if running.Valid && running.Time.After(time.Now()) {
		view.RestoreReady = false
		view.RestoreBlockedReason = "rule_running"
	}

	for _, entry := range view.Entries {
		if entry.CanRelinquish {
			view.RelinquishableCount++
		} else {
			view.OwnershipRetained = true
		}
	}
	view.ModelRemovals = qualityModelRemovals(plan, account, &state, live, running, accountStatus, accountExpires)
	// Fence includes full internal expected/current snapshots, not their redacted
	// projection. Hidden/unrecognized fields and lease changes invalidate consent.
	view.Fence = qualityDigest([]any{id, accountID, ruleVersion, accountVersion, running, canonicalQualityEntry(config), canonicalQualityEntry(rawState), canonicalQualityEntry(liveRaw), canonicalQualityEntry(groups), accountStatus, accountExpires, accountPlatform, accountType, primaryModel, qualityDigest(canonicalQualityEntry(credentials)), view.Entries, view.ModelRemovals})
	if request == nil {
		return view, tx.Commit()
	}
	if !request.Confirm || subtle.ConstantTimeCompare([]byte(request.Fence), []byte(view.Fence)) != 1 {
		return nil, infraerrors.Conflict("QUALITY_OWNERSHIP_PREVIEW_CHANGED", "ownership changed; preview again before confirming")
	}
	if request.RemoveModel {
		return removeQualityOwnedModel(ctx, tx, plan, &state, rawState, live, ruleVersion, view, request)
	}
	if request.Delete {
		// The separate deletion confirmation is fenced too: an edit or new
		// ownership between restoration and deletion cannot consume old consent.
		if state.Action != "" || state.Models != nil || state.Groups != nil || len(view.Entries) > 0 {
			return nil, service.QualityScopeConflict("review remaining ownership before deleting this rule")
		}
		if running.Valid && running.Time.After(time.Now()) {
			return nil, infraerrors.Conflict("QUALITY_OWNERSHIP_PREVIEW_CHANGED", "rule is running; preview again before deleting")
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM scheduled_test_plans WHERE id=$1`, id); err != nil {
			return nil, err
		}
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		view.Fence = ""
		return view, nil
	}

	if request.Restore {
		if !view.RestoreReady {
			return nil, infraerrors.Conflict("QUALITY_OWNERSHIP_RESTORE_BLOCKED", "restore is blocked; review ownership, account availability and running tasks before confirming")
		}
		beforeGroups := groups
		var removed []qualityGroup
		if state.Groups != nil {
			removed = state.Groups.Removed
			conflict, restoreErr := restoreQualityGroups(ctx, tx, accountID, state.Groups)
			if restoreErr != nil {
				return nil, restoreErr
			}
			if conflict {
				return nil, infraerrors.Conflict("QUALITY_OWNERSHIP_RESTORE_BLOCKED", "group ownership changed; preview again")
			}
		}
		for _, entry := range view.Entries {
			if entry.Kind != "model" || entry.Status != "managed" {
				continue
			}
			if _, err = tx.ExecContext(ctx, `UPDATE accounts SET extra=extra #- ARRAY['quality_model_isolation',$2],updated_at=clock_timestamp() WHERE id=$1`, accountID, entry.Model); err != nil {
				return nil, err
			}
		}
		if _, err = tx.ExecContext(ctx, `UPDATE accounts SET extra=extra-'quality_model_isolation' WHERE id=$1 AND extra->'quality_model_isolation'='{}'::jsonb`, accountID); err != nil {
			return nil, err
		}
		if len(removed) > 0 {
			if err = qualityGroupsChanged(ctx, tx, accountID, beforeGroups, removed); err != nil {
				return nil, err
			}
		} else if _, err = tx.ExecContext(ctx, `INSERT INTO scheduler_outbox(event_type,account_id,payload) VALUES('account_changed',$1,'{}')`, accountID); err != nil {
			return nil, err
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM account_quality_states WHERE plan_id=$1`, id); err != nil {
			return nil, err
		}
		// Pausing prevents a subsequent round from reapplying restrictions between
		// this explicit restore and the separately retryable guarded deletion.
		if _, err = tx.ExecContext(ctx, `UPDATE scheduled_test_plans SET enabled=false,updated_at=GREATEST(clock_timestamp(),updated_at+interval '1 microsecond') WHERE id=$1`, id); err != nil {
			return nil, err
		}
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		view.Fence = ""
		view.RestrictionsChanged = true
		return view, nil
	}

	for _, entry := range view.Entries {
		if entry.Status == "unrecognized" || entry.Status == "orphan_same_owner" {
			return nil, infraerrors.Conflict("QUALITY_OWNERSHIP_UNSUPPORTED_STATE", "retain unrecognized ownership for review")
		}
	}
	if view.RelinquishableCount == 0 {
		return view, tx.Commit()
	}
	for _, entry := range view.Entries {
		if !entry.CanRelinquish {
			continue
		}
		if entry.Kind == "model" {
			delete(state.Models.ModelApplied, entry.Model)
			delete(state.Models.ModelRateLimits, entry.Model)
		}
	}
	if state.Groups != nil && len(state.Groups.Removed) == 0 {
		state.Groups = nil
	}
	if state.Models != nil && len(state.Models.ModelApplied) == 0 && len(state.Models.ModelRateLimits) == 0 {
		state.Models = nil
	}
	if state.Groups == nil && state.Models == nil {
		if _, err = tx.ExecContext(ctx, `DELETE FROM account_quality_states WHERE plan_id=$1`, id); err != nil {
			return nil, err
		}
	} else if err = qualityUpsertState(ctx, tx, id, state); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE scheduled_test_plans SET updated_at=GREATEST(clock_timestamp(),updated_at+interval '1 microsecond') WHERE id=$1`, id); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	// Require a fresh preview for any further action, rather than issuing a new
	// fence from a snapshot taken before the transaction committed.
	view.Fence = ""
	return view, nil
}

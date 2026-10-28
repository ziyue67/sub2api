package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// No original value can be reconstructed for an orphan. Only the complete,
// standard marker written by persistent isolation is safe to remove. Unknown
// fields could encode independent restrictions and must remain for review.
func exclusiveQualityMarker(raw json.RawMessage, id int64) bool {
	owner, _ := qualityEntryOwner(raw, id)
	if owner != "this_rule" {
		return false
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || len(fields) != 2 {
		return false
	}
	var reason, stamp string
	if json.Unmarshal(fields["reason"], &reason) != nil || reason != fmt.Sprintf("quality_rule:%d", id) || json.Unmarshal(fields["isolated_at"], &stamp) != nil {
		return false
	}
	_, err := time.Parse(time.RFC3339Nano, stamp)
	return err == nil
}

func qualityModelRemovals(plan *service.ScheduledTestPlan, account *service.Account, state *qualityState, live map[string]json.RawMessage, running sql.NullTime, status string, expires sql.NullTime) []service.QualityModelRemoval {
	out := []service.QualityModelRemoval{}
	if plan.PelicanConfig.Quality.Action != service.QualityActionIsolateModels {
		return out
	}
	models := service.QualityModels(plan)
	for _, model := range models {
		key := account.GetMappedModel(model)
		entry := service.QualityModelRemoval{Model: model, IsolationModel: key, Ready: true, Effect: "remove_target_only"}
		block := func(reason string) { entry.Ready = false; entry.BlockedReason = reason }
		var expected, previous json.RawMessage
		if state.Models != nil {
			expected = state.Models.ModelApplied[key]
			previous = state.Models.ModelRateLimits[key]
		}
		owner, _ := qualityEntryOwner(live[key], plan.ID)
		switch {
		case owner == "absent":
			entry.Effect = "remove_missing_target"
			if len(expected) > 0 {
				if !exclusiveQualityMarker(expected, plan.ID) {
					block("unrecognized_snapshot")
				}
			}
		case owner != "this_rule":
			block("not_owned")
		case !exclusiveQualityMarker(live[key], plan.ID):
			block("additional_or_unrecognized_restriction")
		case len(expected) == 0:
			entry.Effect = "remove_orphan_marker_only"
		case !exclusiveQualityMarker(expected, plan.ID):
			block("unrecognized_snapshot")
		case qualityDigest(canonicalQualityEntry(expected)) != qualityDigest(canonicalQualityEntry(live[key])):
			block("changed_same_owner")
		default:
			entry.Effect = "remove_matching_marker"
		}
		// Persistent isolation can only have captured an absent previous marker.
		// An absent snapshot is supported; a captured non-null value is not invented
		// or erased. Legacy cooldown/concurrency layouts were rejected by preview.
		if len(previous) > 0 && (!json.Valid(previous) || canonicalQualityEntry(previous) != nil) {
			block("previous_restriction")
		}
		if key != model && len(live[model]) > 0 {
			block("ambiguous_mapping")
		}
		for _, peer := range models {
			if peer != model && account.GetMappedModel(peer) == key {
				block("shared_mapping")
			}
		}
		if !slices.ContainsFunc(models, func(peer string) bool { return peer != model }) {
			block("last_model")
		}
		if state.Action != service.QualityActionIsolateModels || checkQualityIsolationScope(plan, state) != nil {
			block("scope_conflict")
		}
		if status != "active" || expires.Valid && !expires.Time.After(time.Now()) {
			block("account_unavailable")
		}
		if running.Valid && running.Time.After(time.Now()) {
			block("rule_running")
		}
		out = append(out, entry)
	}
	return out
}

// This operation contracts exactly one configured target and its corresponding
// ownership under the same locks as the rebuilt preview. Updating both the
// durable scope and rule version fences stale claims/outcomes without canceling
// or replaying the worker's request. No other policy or historical result changes.
func removeQualityOwnedModel(ctx context.Context, tx *sql.Tx, plan *service.ScheduledTestPlan, state *qualityState, rawState []byte, live map[string]json.RawMessage, version time.Time, view *service.QualityOwnershipPreview, request *service.QualityOwnershipRequest) (*service.QualityOwnershipPreview, error) {
	var selected *service.QualityModelRemoval
	for i := range view.ModelRemovals {
		if view.ModelRemovals[i].Model == request.Model {
			selected = &view.ModelRemovals[i]
			break
		}
	}
	if selected == nil || !selected.Ready {
		return nil, infraerrors.Conflict("QUALITY_MODEL_REMOVAL_BLOCKED", "model removal is blocked; review the current preview")
	}
	models := slices.DeleteFunc(slices.Clone(service.QualityModels(plan)), func(model string) bool { return model == request.Model })
	if len(models) == 0 {
		return nil, infraerrors.Conflict("QUALITY_MODEL_REMOVAL_BLOCKED", "at least one tested model must remain")
	}
	encoded, _ := json.Marshal(models)
	// Patch JSON in place: preserve every unrelated field, counter and snapshot.
	if len(rawState) == 0 || state.Action != service.QualityActionIsolateModels {
		return nil, service.QualityScopeConflict("model isolation scope is not recorded")
	}
	if _, err := tx.ExecContext(ctx, `UPDATE account_quality_states SET state=jsonb_set(
  state #- ARRAY['models','model_applied',$2] #- ARRAY['models','model_rate_limits',$2],
  '{isolation_models}',$3::jsonb) WHERE plan_id=$1`, plan.ID, selected.IsolationModel, string(encoded)); err != nil {
		return nil, err
	}
	changed := qualityEntryPresent(live[selected.IsolationModel])
	if changed {
		if _, err := tx.ExecContext(ctx, `UPDATE accounts SET extra=extra #- ARRAY['quality_model_isolation',$2],
   updated_at=GREATEST(clock_timestamp(),updated_at+interval '1 microsecond') WHERE id=$1`, plan.AccountID, selected.IsolationModel); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE accounts SET extra=extra-'quality_model_isolation' WHERE id=$1 AND extra->'quality_model_isolation'='{}'::jsonb`, plan.AccountID); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO scheduler_outbox(event_type,account_id,payload) VALUES('account_changed',$1,'{}')`, plan.AccountID); err != nil {
			return nil, err
		}
	}
	primary := plan.ModelID
	if primary == request.Model {
		primary = models[0]
	}
	result, err := tx.ExecContext(ctx, `UPDATE scheduled_test_plans SET model_id=$2,
  pelican_config=jsonb_set(pelican_config,'{model_ids}',$3::jsonb),
  updated_at=GREATEST(clock_timestamp(),updated_at+interval '1 microsecond')
  WHERE id=$1 AND updated_at=$4 AND (running_until IS NULL OR running_until<=clock_timestamp())`, plan.ID, primary, string(encoded), version)
	if err != nil {
		return nil, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if count != 1 {
		return nil, infraerrors.Conflict("QUALITY_OWNERSHIP_PREVIEW_CHANGED", "rule version or lease changed; preview again")
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	view.Fence = ""
	view.RestrictionsChanged = changed
	return view, nil
}

func qualityEntryPresent(raw json.RawMessage) bool { return len(raw) > 0 && string(raw) != "null" }

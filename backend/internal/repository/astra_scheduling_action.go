package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type astraActionState struct {
	Mode         string                 `json:"mode"`
	GroupIDs     []int64                `json:"group_ids,omitempty"`
	Removed      []qualityGroup         `json:"removed,omitempty"`
	Memberships  []astraGroupMembership `json:"memberships,omitempty"`
	Mapping      json.RawMessage        `json:"mapping,omitempty"`
	AfterMapping json.RawMessage        `json:"after_mapping,omitempty"`
	Version      time.Time              `json:"version"`
}

// Settings fence, mutation, ownership and cache invalidation commit together.
func (r *accountRepository) ApplyAstraScheduling(ctx context.Context, id int64, s config.AstraRoutingSettings, ready bool) (service.AstraSchedulingActionResult, error) {
	result := service.AstraSchedulingActionResult{Allowed: ready, Action: s.EffectiveSchedulingMode()}
	db, ok := r.sql.(interface {
		BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error)
	})
	if !ok {
		return result, errors.New("astra_scheduling_transaction_unavailable")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback() }()
	var raw []byte
	if err = tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key='astra_routing_experiment_v1' FOR SHARE`).Scan(&raw); err != nil {
		return result, err
	}
	var actual config.AstraRoutingSettings
	if err = json.Unmarshal(raw, &actual); err != nil {
		return result, err
	}
	if !actual.AccountScheduling || !reflect.DeepEqual(actual, s) || !slices.Contains(s.CookiePool.TargetAccountIDs, id) {
		return result, errors.New("configuration_changed")
	}
	var mapping, extraRaw []byte
	var sched, eligible bool
	var version time.Time
	err = tx.QueryRowContext(ctx, `SELECT credentials->'model_mapping',COALESCE(extra,'{}'::jsonb),schedulable,updated_at,
 status='active' AND platform='openai' AND type IN ('oauth','setup-token') AND parent_account_id IS NULL AND (expires_at IS NULL OR expires_at>NOW())
 FROM accounts WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, id).Scan(&mapping, &extraRaw, &sched, &version, &eligible)
	if err != nil {
		return result, err
	}
	ready = ready && eligible
	result.Allowed = ready
	var extra map[string]any
	if err = json.Unmarshal(extraRaw, &extra); err != nil {
		return result, err
	}
	var state astraActionState
	err = tx.QueryRowContext(ctx, `SELECT state FROM astra_scheduling_states WHERE account_id=$1`, id).Scan(&raw)
	if err != nil && err != sql.ErrNoRows {
		return result, err
	}
	if err == nil {
		if err = json.Unmarshal(raw, &state); err != nil {
			return result, err
		}
	}
	groups, err := astraSchedulingGroups(ctx, tx, id)
	if err != nil {
		return result, err
	}
	groupIDs := []int64{}
	for _, g := range groups {
		groupIDs = append(groupIDs, g.GroupID)
	}
	switchMode := state.Mode != "" && (state.Mode != result.Action || (state.Mode == "groups" && !reflect.DeepEqual(state.GroupIDs, s.SchedulingGroupIDs)))
	stateDirty := false
	if state.Mode == "groups" && switchMode {
		var changed bool
		changed, _, err = reconcileAstraGroups(ctx, tx, id, &state, groups, false, true)
		if err != nil {
			return result, err
		}
		result.Changed = changed
		groupIDs = mergeGroupIDs(groupIDs, state.GroupIDs)
		state = astraActionState{}
		stateDirty = true
		groups, err = astraSchedulingGroups(ctx, tx, id)
		if err != nil {
			return result, err
		}
	}
	if state.Mode != "" && state.Mode != "groups" && (ready || switchMode) {
		switch state.Mode {
		case "account":
			if !eligible || sched || !version.Equal(state.Version) {
				return result, errors.New("astra_restore_conflict")
			}
			if _, err = tx.ExecContext(ctx, `UPDATE accounts SET schedulable=true WHERE id=$1`, id); err != nil {
				return result, err
			}
			sched = true
		case "model":
			if extra["astra_model_disabled"] != true || !astraJSONEqual(mapping, state.AfterMapping) {
				return result, errors.New("astra_restore_conflict")
			}
			if len(state.Mapping) == 0 {
				_, err = tx.ExecContext(ctx, `UPDATE accounts SET credentials=credentials-'model_mapping',extra=extra-'astra_model_disabled'-'astra_model_blocked_keys'-'astra_model_empty_mapping' WHERE id=$1`, id)
			} else {
				_, err = tx.ExecContext(ctx, `UPDATE accounts SET credentials=jsonb_set(credentials,'{model_mapping}',$2::jsonb),extra=extra-'astra_model_disabled'-'astra_model_blocked_keys'-'astra_model_empty_mapping' WHERE id=$1`, id, string(state.Mapping))
			}
			if err != nil {
				return result, err
			}
			mapping = state.Mapping
			delete(extra, "astra_model_disabled")
		}
		state = astraActionState{}
		stateDirty = true
		result.Changed = true
	}
	if result.Action == "groups" {
		if state.Mode == "" {
			state = astraActionState{Mode: "groups", GroupIDs: slices.Clone(s.SchedulingGroupIDs)}
			stateDirty = true
		}
		var changed, dirty bool
		changed, dirty, err = reconcileAstraGroups(ctx, tx, id, &state, groups, ready, false)
		if err != nil {
			return result, err
		}
		result.Changed = result.Changed || changed
		stateDirty = stateDirty || dirty
		groupIDs = mergeGroupIDs(groupIDs, state.GroupIDs)
	}
	if !ready && state.Mode == "" && result.Action != "groups" {
		next := astraActionState{Mode: result.Action, GroupIDs: s.SchedulingGroupIDs}
		change := false
		switch result.Action {
		case "account":
			if sched {
				_, err = tx.ExecContext(ctx, `UPDATE accounts SET schedulable=false WHERE id=$1`, id)
				change = true
			}
		case "model":
			if extra["astra_model_disabled"] != true {
				next.Mapping = append(json.RawMessage(nil), mapping...)
				var models map[string]string
				if len(mapping) > 0 && string(mapping) != "null" {
					if err = json.Unmarshal(mapping, &models); err != nil {
						return result, errors.New("astra_model_mapping_invalid")
					}
				}
				originalCount := len(models)
				blocked := []string{}
				for k, v := range models {
					if strings.EqualFold(k, "gpt-6-astra") || (strings.EqualFold(v, "gpt-6-astra") && !strings.HasSuffix(k, "*")) {
						blocked = append(blocked, k)
						delete(models, k)
					}
				}
				if models != nil {
					next.AfterMapping, err = json.Marshal(models)
				} else {
					next.AfterMapping = mapping
				}
				if err != nil {
					return result, err
				}
				flags, _ := json.Marshal(map[string]any{"astra_model_disabled": true, "astra_model_blocked_keys": blocked, "astra_model_empty_mapping": originalCount > 0 && len(models) == 0})
				var newMapping any
				if len(next.AfterMapping) > 0 {
					newMapping = string(next.AfterMapping)
				}
				_, err = tx.ExecContext(ctx, `UPDATE accounts SET credentials=CASE WHEN $2::jsonb IS NULL THEN credentials ELSE jsonb_set(credentials,'{model_mapping}',$2::jsonb) END,extra=COALESCE(extra,'{}'::jsonb)||$3::jsonb WHERE id=$1`, id, newMapping, string(flags))
				change = true
			}
		}
		if err != nil {
			return result, err
		}
		if change {
			state = next
			result.Changed = true
		}
	}
	if result.Changed {
		if err = tx.QueryRowContext(ctx, `UPDATE accounts SET updated_at=clock_timestamp() WHERE id=$1 RETURNING updated_at`, id).Scan(&state.Version); err != nil {
			return result, err
		}
		if err = enqueueSchedulerOutbox(ctx, tx, service.SchedulerOutboxEventAccountChanged, &id, nil, buildSchedulerGroupPayload(groupIDs)); err != nil {
			return result, err
		}
	}
	if result.Changed || stateDirty {
		if state.Mode != "" {
			raw, err = json.Marshal(state)
			if err != nil {
				return result, err
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO astra_scheduling_states(account_id,state) VALUES($1,$2) ON CONFLICT(account_id) DO UPDATE SET state=EXCLUDED.state`, id, string(raw)); err != nil {
				return result, err
			}
		} else if _, err = tx.ExecContext(ctx, `DELETE FROM astra_scheduling_states WHERE account_id=$1`, id); err != nil {
			return result, err
		}
	}
	if err = tx.Commit(); err != nil {
		return result, err
	}
	if result.Changed {
		r.syncSchedulerAccountSnapshot(ctx, id)
	}
	return result, nil
}
func astraJSON(raw []byte) []byte {
	if len(raw) == 0 {
		return []byte("null")
	}
	return raw
}

func astraJSONEqual(a, b []byte) bool {
	var left, right any
	if json.Unmarshal(astraJSON(a), &left) != nil || json.Unmarshal(astraJSON(b), &right) != nil {
		return false
	}
	return reflect.DeepEqual(left, right)
}

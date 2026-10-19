package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type qualityModelDecision struct{ model, outcome string }

// Resolve aliases once and choose the strongest evidence for each upstream
// model. A passing alias cannot release another alias's failed/unknown result.
// The same reducer is used by legacy cooldowns and persistent isolation.
func qualityModelDecisions(account *service.Account, models []string, outcomes map[string]string) []qualityModelDecision {
	rank := map[string]int{"passed": 1, "skipped": 2, "inconclusive": 3, "failed": 4}
	decisions := []qualityModelDecision{}
	indices := map[string]int{}
	for _, model := range models {
		outcome := outcomes[model]
		if rank[outcome] == 0 {
			outcome = "skipped"
		}
		key := account.GetMappedModel(model)
		if index, ok := indices[key]; ok {
			if rank[outcome] > rank[decisions[index].outcome] {
				decisions[index].outcome = outcome
			}
		} else {
			indices[key] = len(decisions)
			decisions = append(decisions, qualityModelDecision{model, outcome})
		}
	}
	// Failures are applied first so a healthy peer cannot prematurely unlock
	// account-level recovery in the legacy cooldown path.
	sort.SliceStable(decisions, func(i, j int) bool { return rank[decisions[i].outcome] > rank[decisions[j].outcome] })
	return decisions
}

func sameQualityScope[T ~string | ~int64](a, b []T) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}

// checkQualityIsolationScope is also called by plan updates under the plan lock.
// Pausing/changing schedules and recovery are safe; discarding a live isolation's
// model/group scope would leave persistent blocks without a recovery path.
func checkQualityIsolationScope(plan *service.ScheduledTestPlan, state *qualityState) error {
	q := plan.PelicanConfig
	if state.Action == service.QualityActionIsolateModels {
		if q == nil || q.Quality == nil || q.Quality.Action != service.QualityActionIsolateModels ||
			!sameQualityScope(state.IsolationModels, service.QualityModels(plan)) || !sameQualityScope(state.IsolationGroupIDs, q.Quality.RemoveGroupIDs) {
			return fmt.Errorf("restore this rule's isolated models and groups before changing its action or targets")
		}
	} else if state.Action != "" && q != nil && q.Quality != nil && q.Quality.Action == service.QualityActionIsolateModels {
		return fmt.Errorf("restore this rule's previous account action before enabling model isolation")
	}
	return nil
}

// Saving the new policy is an explicit request to replace the old account
// action. Restore only this rule's group/scheduling mutation, under the same
// plan -> account lock order as detection. Do not fabricate a passing result or
// require AutoRestore (which controls future probes, not this policy change).
func restoreQualityAccountActionForIsolation(ctx context.Context, tx *sql.Tx, plan *service.ScheduledTestPlan, state *qualityState) error {
	var status string
	err := tx.QueryRowContext(ctx, `SELECT status FROM accounts WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, plan.AccountID).Scan(&status)
	if err == sql.ErrNoRows {
		return fmt.Errorf("无法自动切换模型隔离：账号已删除")
	}
	if err != nil {
		return err
	}
	if status != "active" {
		return fmt.Errorf("无法自动切换模型隔离：账号不是正常状态，请先检查账号状态")
	}
	before, err := qualityGroups(ctx, tx, plan.AccountID)
	if err != nil {
		return err
	}
	switch state.Action {
	case "remove_groups":
		conflict, err := restoreQualityGroups(ctx, tx, plan.AccountID, state)
		if err != nil {
			return err
		}
		if conflict {
			return fmt.Errorf("无法自动切换模型隔离：原分组已删除或分组归属已被人工修改，请先检查账号分组；本次修改未保存")
		}
	case "disable_scheduling":
		result, err := tx.ExecContext(ctx, `UPDATE accounts SET schedulable=true,updated_at=clock_timestamp()
 WHERE id=$1 AND (expires_at IS NULL OR expires_at>NOW())`, plan.AccountID)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return fmt.Errorf("无法自动切换模型隔离：账号已过期，不能恢复调度；本次修改未保存")
		}
	default:
		return fmt.Errorf("unsupported previous quality action: %s", state.Action)
	}
	if err = qualityGroupsChanged(ctx, tx, plan.AccountID, before, state.Removed); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM account_quality_states WHERE plan_id=$1`, plan.ID); err != nil {
		return err
	}
	*state = qualityState{}
	return nil
}

func applyQualityIsolation(ctx context.Context, tx *sql.Tx, plan *service.ScheduledTestPlan, status string, state *qualityState) (string, error) {
	if err := checkQualityIsolationScope(plan, state); err != nil {
		return "action_conflict", nil
	}
	q := plan.PelicanConfig.Quality
	if plan.QualityModelOutcomes == nil {
		return "inconclusive", nil
	}
	account := &service.Account{}
	var credentials, rawExtra []byte
	if err := tx.QueryRowContext(ctx, `SELECT credentials,COALESCE(extra,'{}'::jsonb) FROM accounts WHERE id=$1`, plan.AccountID).Scan(&credentials, &rawExtra); err != nil {
		return "", err
	}
	if len(credentials) > 0 {
		if err := json.Unmarshal(credentials, &account.Credentials); err != nil {
			return "", err
		}
	}
	if err := json.Unmarshal(rawExtra, &account.Extra); err != nil {
		return "", err
	}
	beforeGroups, err := qualityGroups(ctx, tx, plan.AccountID)
	if err != nil {
		return "", err
	}
	models := service.QualityModels(plan)
	if state.Models != nil {
		currentKeys := map[string]bool{}
		for _, model := range models {
			currentKeys[account.GetMappedModel(model)] = true
		}
		for key := range state.Models.ModelApplied {
			if !currentKeys[key] {
				return "action_conflict", nil
			}
		}
	}
	decisions := qualityModelDecisions(account, models, plan.QualityModelOutcomes)
	allFailed, anyPassed := len(decisions) > 0, false
	for _, decision := range decisions {
		allFailed = allFailed && decision.outcome == "failed"
		anyPassed = anyPassed || decision.outcome == "passed"
	}
	if q.AutoRestore && anyPassed && status != "active" {
		return "restore_conflict", nil
	}
	if state.Models == nil {
		state.Models = &qualityState{}
	}
	state.Models.PersistentModels = true
	modelActions, action, err := transitionQualityModelRound(account, state.Models, plan.ID, models, plan.QualityModelOutcomes, time.Time{}, q.AutoRestore, time.Now(), true)
	if err != nil {
		return "", err
	}
	if action == "action_conflict" || action == "restore_conflict" {
		return action, nil
	}
	if action == "models_cooled" || action == "model_cooldown_refreshed" {
		action = "models_isolated"
	}

	groupChanged := false
	var removed []qualityGroup
	if allFailed && state.Groups == nil {
		groups := &qualityState{Action: "remove_groups"}
		if err = removeQualityGroups(ctx, tx, plan.AccountID, q.RemoveGroupIDs, groups); err != nil {
			return "", err
		}
		if len(groups.Removed) > 0 {
			state.Groups = groups
			action = "groups_removed"
			groupChanged = true
		}
	} else if anyPassed && q.AutoRestore && state.Groups != nil {
		conflict, e := restoreQualityGroups(ctx, tx, plan.AccountID, state.Groups)
		if e != nil {
			return "", e
		}
		if conflict {
			return "restore_conflict", nil
		}
		removed = state.Groups.Removed
		state.Groups = nil
		action = "groups_restored_models_isolated"
		groupChanged = true
	}
	updatedExtra, err := json.Marshal(account.Extra)
	if err != nil {
		return "", err
	}
	// Compare decoded JSON to avoid spurious writes from PostgreSQL key ordering.
	var original, updated any
	if err = json.Unmarshal(rawExtra, &original); err != nil {
		return "", err
	}
	if err = json.Unmarshal(updatedExtra, &updated); err != nil {
		return "", err
	}
	originalJSON, err := json.Marshal(original)
	if err != nil {
		return "", err
	}
	updatedJSON, err := json.Marshal(updated)
	if err != nil {
		return "", err
	}
	if string(originalJSON) != string(updatedJSON) || groupChanged {
		if _, err = tx.ExecContext(ctx, `UPDATE accounts SET extra=$2::jsonb,updated_at=GREATEST(clock_timestamp(),updated_at+interval '1 microsecond') WHERE id=$1`, plan.AccountID, string(updatedExtra)); err != nil {
			return "", err
		}
		if groupChanged {
			if state.Groups != nil {
				removed = state.Groups.Removed
			}
			if err = qualityGroupsChanged(ctx, tx, plan.AccountID, beforeGroups, removed); err != nil {
				return "", err
			}
		} else if _, err = tx.ExecContext(ctx, `INSERT INTO scheduler_outbox(event_type,account_id,payload) VALUES('account_changed',$1,'{}')`, plan.AccountID); err != nil {
			return "", err
		}
	}
	if len(state.Models.ModelRateLimits) == 0 && state.Groups == nil {
		if state.Action != "" && q.AutoRestore && anyPassed {
			action = "restored"
		} else if anyPassed {
			action = "passed"
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM account_quality_states WHERE plan_id=$1`, plan.ID); err != nil {
			return "", err
		}
	} else {
		state.Action = service.QualityActionIsolateModels
		state.IsolationModels = slices.Clone(models)
		state.IsolationGroupIDs = slices.Clone(q.RemoveGroupIDs)
		if err = qualityUpsertState(ctx, tx, plan.ID, *state); err != nil {
			return "", err
		}
	}
	plan.QualityModelActions = modelActions
	return action, nil
}

func readQualityState(ctx context.Context, tx *sql.Tx, planID int64) (qualityState, error) {
	var state qualityState
	var raw []byte
	err := tx.QueryRowContext(ctx, `SELECT state FROM account_quality_states WHERE plan_id=$1`, planID).Scan(&raw)
	if err == sql.ErrNoRows {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	err = json.Unmarshal(raw, &state)
	return state, err
}

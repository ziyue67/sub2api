package repository

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/robfig/cron/v3"
)

func cooldownEntryEqual(current any, applied json.RawMessage) bool {
	raw, err := json.Marshal(current)
	if err != nil {
		return false
	}
	var decoded any
	if json.Unmarshal(applied, &decoded) != nil {
		return false
	}
	expected, _ := json.Marshal(decoded)
	return bytes.Equal(raw, expected)
}

func cooldownEntryUntil(entry any) time.Time {
	value, ok := entry.(map[string]any)
	if !ok {
		return time.Time{}
	}
	raw, _ := value["rate_limit_reset_at"].(string)
	until, _ := time.Parse(time.RFC3339, raw)
	return until
}

// Native/manual expiry cleanup can remove the live entry before a probe runs.
// Relinquish only expired snapshots that are absent or still exactly ours.
// An active snapshot or a replacement entry remains protected by ownership.
func pruneExpiredQualityModels(account *service.Account, state *qualityState, now time.Time) {
	limits, _ := account.Extra["model_rate_limits"].(map[string]any)
	for model, applied := range state.ModelApplied {
		var entry any
		if json.Unmarshal(applied, &entry) != nil {
			continue
		}
		until := cooldownEntryUntil(entry)
		if until.IsZero() || until.After(now) {
			continue
		}
		current := limits[model]
		if current != nil && !cooldownEntryEqual(current, applied) {
			continue
		}
		delete(limits, model)
		delete(state.ModelRateLimits, model)
		delete(state.ModelApplied, model)
	}
	if len(limits) == 0 {
		delete(account.Extra, "model_rate_limits")
	}
}

// Keep ownership at entry granularity: a newer native error or manual edit wins.
func transitionQualityModels(account *service.Account, state *qualityState, planID int64, models []string, until time.Time, outcome string, restore bool, now time.Time) (string, error) {
	return transitionQualityModelsScoped(account, state, planID, models, until, outcome, restore, now, false)
}
func transitionQualityModelsScoped(account *service.Account, state *qualityState, planID int64, models []string, until time.Time, outcome string, restore bool, now time.Time, holdRecovery bool) (string, error) {
	pruneExpiredQualityModels(account, state, now)
	if outcome == "inconclusive" {
		owned := false
		for _, model := range models {
			if _, ok := state.ModelRateLimits[account.GetMappedModel(model)]; ok {
				owned = true
			}
		}
		if state.Action == "" || !owned {
			return "inconclusive", nil
		}
		outcome = "failed"
	}
	if outcome == "passed" && (state.Action == "" || !restore) {
		return "passed", nil
	}
	if account.Extra == nil {
		account.Extra = map[string]any{}
	}
	limits, _ := account.Extra["model_rate_limits"].(map[string]any)
	if limits == nil {
		limits = map[string]any{}
	}
	if outcome == "pending" {
		state.Action = service.QualityActionRemoveModel
		if state.AppliedConcurrency != nil && account.Concurrency != *state.AppliedConcurrency {
			return "action_conflict", nil
		}
		lowerQualityRecoveryConcurrency(account, state)
		return "probe_pending", nil
	}
	if outcome == "failed" {
		first := state.Action == ""
		if first {
			state.Action = service.QualityActionRemoveModel
			// A second failure while ramping must retain the original captured
			// ceiling rather than replacing it with an intermediate value.
			if state.RecoveryTarget != nil {
				target := *state.RecoveryTarget
				state.PreviousConcurrency = &target
				state.RecoveryTarget = nil
			}
		}
		if state.ModelRateLimits == nil {
			state.ModelRateLimits = map[string]json.RawMessage{}
		}
		if state.ModelApplied == nil {
			state.ModelApplied = map[string]json.RawMessage{}
		}
		targeted := map[string]bool{}
		for _, model := range models {
			targeted[account.GetMappedModel(model)] = true
		}
		for key, applied := range state.ModelApplied {
			if targeted[key] && !cooldownEntryEqual(limits[key], applied) {
				return "action_conflict", nil
			}
		}
		for _, model := range models {
			key := account.GetMappedModel(model)
			previous, err := json.Marshal(limits[key])
			if err != nil {
				return "", err
			}
			if _, owned := state.ModelRateLimits[key]; !owned {
				state.ModelRateLimits[key] = previous
			}
		}
		for key := range targeted {
			reset := until
			if previous := cooldownEntryUntil(limits[key]); previous.After(reset) {
				reset = previous
			}
			entry := map[string]any{"rate_limited_at": now.UTC().Format(time.RFC3339), "rate_limit_reset_at": reset.UTC().Format(time.RFC3339), "reason": fmt.Sprintf("quality_rule:%d", planID)}
			limits[key] = entry
			raw, err := json.Marshal(entry)
			if err != nil {
				return "", err
			}
			state.ModelApplied[key] = raw
		}
		account.Extra["model_rate_limits"] = limits
		if first || state.AppliedConcurrency == nil {
			lowerQualityRecoveryConcurrency(account, state)
		}
		if first {
			return "models_cooled", nil
		}
		return "model_cooldown_refreshed", nil
	}
	if outcome != "passed" {
		return "no_change", nil
	}
	conflict := false
	restoredCount := 0
	targeted := map[string]bool{}
	for _, model := range models {
		targeted[account.GetMappedModel(model)] = true
	}
	for key, previous := range state.ModelRateLimits {
		if !targeted[key] {
			continue
		}
		if !cooldownEntryEqual(limits[key], state.ModelApplied[key]) {
			conflict = true
			continue
		}
		var original any
		if err := json.Unmarshal(previous, &original); err != nil {
			return "", err
		}
		if cooldownEntryUntil(original).After(now) {
			limits[key] = original
		} else {
			delete(limits, key)
		}
		delete(state.ModelRateLimits, key)
		delete(state.ModelApplied, key)
		restoredCount++
	}
	if len(limits) == 0 {
		delete(account.Extra, "model_rate_limits")
	} else {
		account.Extra["model_rate_limits"] = limits
	}
	if len(state.ModelRateLimits) > 0 {
		if conflict {
			return "restore_conflict", nil
		}
		if restoredCount == 0 {
			return "passed", nil
		}
		return "models_partially_restored", nil
	}
	if holdRecovery {
		return "models_partially_restored", nil
	}
	if state.PreviousConcurrency != nil && state.AppliedConcurrency != nil {
		if account.Concurrency == *state.AppliedConcurrency {
			if state.NativeRecovery {
				target := *state.PreviousConcurrency
				state.RecoveryTarget = &target
				state.PreviousConcurrency, state.AppliedConcurrency = nil, nil
			} else {
				account.Concurrency = *state.PreviousConcurrency
				state.PreviousConcurrency, state.AppliedConcurrency = nil, nil
			}
		} else {
			conflict = true
		}
	}
	if conflict {
		return "restore_conflict", nil
	}
	if state.RecoveryTarget != nil {
		// Keep ownership until the native success ramp reaches the captured
		// pre-quarantine concurrency. The ramp, rather than this probe result,
		// performs each intermediate upgrade.
		state.Action = ""
		return "recovery_started", nil
	}
	*state = qualityState{}
	return "restored", nil
}

// The concurrency ceiling is shared by the account; model availability is not.
func lowerQualityRecoveryConcurrency(account *service.Account, state *qualityState) {
	if account.Type != service.AccountTypeOAuth {
		return
	}
	cap := state.RecoveryConcurrency
	if cap <= 0 {
		cap = 5
	}
	if state.PreviousConcurrency == nil {
		previous := account.Concurrency
		if state.RecoveryTarget != nil {
			previous = *state.RecoveryTarget
			state.RecoveryTarget = nil
		}
		state.PreviousConcurrency = &previous
	}
	applied := account.Concurrency
	if applied > cap {
		applied = cap
	}
	state.AppliedConcurrency = &applied
	account.Concurrency = applied
}

func applyQualityModelOutcome(ctx context.Context, tx *sql.Tx, plan *service.ScheduledTestPlan, outcome, status string, state qualityState) (string, error) {
	if outcome == "inconclusive" && plan.QualityModelOutcomes == nil {
		if state.Action == "" {
			return "inconclusive", nil
		}
		outcome = "failed"
	}
	if outcome == "passed" && state.Action != "" && plan.PelicanConfig.Quality.AutoRestore && status != "active" {
		return "restore_conflict", nil
	}
	account := &service.Account{}
	var credentials, extra []byte
	if err := tx.QueryRowContext(ctx, `SELECT platform,type,concurrency,COALESCE(credentials,'{}'::jsonb),COALESCE(extra,'{}'::jsonb) FROM accounts WHERE id=$1`, plan.AccountID).Scan(&account.Platform, &account.Type, &account.Concurrency, &credentials, &extra); err != nil {
		return "", err
	}
	if err := json.Unmarshal(credentials, &account.Credentials); err != nil {
		return "", err
	}
	if err := json.Unmarshal(extra, &account.Extra); err != nil {
		return "", err
	}
	now := time.Now()
	schedule, err := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow).Parse(plan.CronExpression)
	if err != nil {
		return "", err
	}
	beforeConcurrency := account.Concurrency
	restore := plan.PelicanConfig.Quality.AutoRestore || plan.PelicanConfig.Quality.TriggerOnUpstream5xx
	action := "inconclusive"
	modelActions := map[string]string{}
	if plan.QualityModelOutcomes == nil {
		action, err = transitionQualityModels(account, &state, plan.ID, plan.PelicanConfig.Quality.RemoveModels, schedule.Next(now), outcome, restore, now)
	} else {
		allConfirmed := outcome == "passed"
		// Apply failed models first, then passing models; a healthy peer cannot
		// unlock the shared recovery ramp while another model remains cooled.
		for _, verdict := range []string{"failed", "inconclusive", "passed"} {
			for _, model := range plan.PelicanConfig.Quality.RemoveModels {
				if plan.QualityModelOutcomes[model] != verdict {
					continue
				}
				modelAction, e := transitionQualityModelsScoped(account, &state, plan.ID, []string{model}, schedule.Next(now), verdict, restore, now, !allConfirmed)
				if e != nil {
					return "", e
				}
				if modelAction == "restore_conflict" || modelAction == "action_conflict" {
					return modelAction, nil
				}
				modelActions[model] = modelAction
				if modelAction != "passed" && modelAction != "inconclusive" {
					action = modelAction
				}
			}
		}
		// Model restoration is independent; the account's shared concurrency
		// recovery still requires the complete eligible round to pass.
		if allConfirmed && len(state.ModelRateLimits) == 0 && state.PreviousConcurrency != nil {
			action, err = transitionQualityModels(account, &state, plan.ID, nil, schedule.Next(now), "passed", restore, now)
		} else if allConfirmed && action == "inconclusive" && state.Action == "" {
			action = "passed"
		}
		outcome = "inconclusive"
		if len(state.ModelRateLimits) > 0 {
			outcome = "failed"
		} else if action == "recovery_started" || action == "restored" {
			outcome = "passed"
		}
	}
	if err != nil {
		return "", err
	}
	updated, err := json.Marshal(account.Extra)
	if err != nil {
		return "", err
	}
	// Database JSON order differs; compare canonical decoded content.
	var original any
	var current any
	if err := json.Unmarshal(extra, &original); err != nil {
		return "", err
	}
	if err := json.Unmarshal(updated, &current); err != nil {
		return "", err
	}
	originalRaw, _ := json.Marshal(original)
	currentRaw, _ := json.Marshal(current)
	changed := !bytes.Equal(originalRaw, currentRaw) || beforeConcurrency != account.Concurrency
	if changed {
		if _, err = tx.ExecContext(ctx, `UPDATE accounts SET extra=$2::jsonb,concurrency=$3,updated_at=GREATEST(clock_timestamp(),updated_at+interval '1 microsecond') WHERE id=$1`, plan.AccountID, string(updated), account.Concurrency); err != nil {
			return "", err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO scheduler_outbox(event_type,account_id,payload) VALUES('account_changed',$1,'{}')`, plan.AccountID); err != nil {
			return "", err
		}
	}
	if (outcome == "failed" || outcome == "pending") && plan.PelicanConfig.Quality.TriggerOnUpstream5xx && action != "inconclusive" {
		if err := blockNativeRecoveryRamp(ctx, tx, account.Extra, account.Concurrency); err != nil {
			return "", err
		}
		updated, err = json.Marshal(account.Extra)
		if err != nil {
			return "", err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE accounts SET extra=$2::jsonb,updated_at=GREATEST(clock_timestamp(),updated_at+interval '1 microsecond') WHERE id=$1`, plan.AccountID, string(updated)); err != nil {
			return "", err
		}
	}
	if outcome == "passed" && action == "recovery_started" && state.RecoveryTarget != nil {
		if err := armNativeRecoveryRamp(ctx, tx, account.Extra, account.Concurrency, *state.RecoveryTarget); err != nil {
			return "", err
		}
		updated, err = json.Marshal(account.Extra)
		if err != nil {
			return "", err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE accounts SET extra=$2::jsonb,updated_at=GREATEST(clock_timestamp(),updated_at+interval '1 microsecond') WHERE id=$1`, plan.AccountID, string(updated)); err != nil {
			return "", err
		}
	}
	if state.Action == "" && state.RecoveryTarget == nil {
		_, err = tx.ExecContext(ctx, `DELETE FROM account_quality_states WHERE plan_id=$1`, plan.ID)
	} else {
		err = qualityUpsertState(ctx, tx, plan.ID, state)
	}
	if err == nil {
		plan.QualityModelActions = modelActions
	}
	return action, err
}

// armNativeRecoveryRamp persists the captured ceiling into the existing
// auto-config JSON state. No schema change is needed and manual edits still
// invalidate the state through RecordConcurrencyResult's current-value check.
func armNativeRecoveryRamp(ctx context.Context, tx *sql.Tx, extra map[string]any, current, target int) error {
	if target < current {
		return nil
	}
	var raw string
	if err := tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=$1`, service.SettingKeyOAuthAutoConfig).Scan(&raw); err != nil {
		return nil
	}
	c := service.DefaultOAuthAutoConfig()
	if json.Unmarshal([]byte(raw), &c) != nil {
		return fmt.Errorf("decode OAuth auto-config")
	}
	if !c.UpgradeEnabled {
		return fmt.Errorf("quality recovery requires OAuth concurrency upgrades to be enabled")
	}
	max := target
	if c.MaxConcurrency < max {
		max = c.MaxConcurrency
	}
	state := service.AutoConfigConcurrencyState{Revision: c.Revision, Concurrency: current, Maximum: max, RecoveryTarget: max, PausedUntil: time.Now().UTC().Add(time.Duration(c.CooldownSeconds) * time.Second)}
	encoded, err := json.Marshal(state)
	if err != nil {
		return err
	}
	extra[service.AutoConfigConcurrencyExtraKey] = json.RawMessage(encoded)
	return nil
}

func blockNativeRecoveryRamp(ctx context.Context, tx *sql.Tx, extra map[string]any, current int) error {
	var raw string
	if err := tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=$1`, service.SettingKeyOAuthAutoConfig).Scan(&raw); err != nil {
		return nil
	}
	c := service.DefaultOAuthAutoConfig()
	if json.Unmarshal([]byte(raw), &c) != nil || !c.UpgradeEnabled {
		return nil
	}
	// RecoveryTarget=current fences every native success outcome while the
	// quality probe is pending. PausedUntil alone is insufficient because an
	// old request may complete after the cooldown window.
	state := service.AutoConfigConcurrencyState{Revision: c.Revision, Concurrency: current, Maximum: current, RecoveryTarget: current, PausedUntil: time.Now().UTC().Add(time.Duration(c.CooldownSeconds) * time.Second)}
	encoded, err := json.Marshal(state)
	if err != nil {
		return err
	}
	extra[service.AutoConfigConcurrencyExtraKey] = json.RawMessage(encoded)
	return nil
}

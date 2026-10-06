package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// Persist settings and only the required account subkeys in one transaction.
// This preserves concurrently refreshed tokens, account flags, group bindings,
// proxy assignments, cooldowns and existing mappings for other models.
func (r *settingRepository) SetAstraRoutingWithAccounts(ctx context.Context, key string, value config.AstraRoutingSettings) error {
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.Client().ExecContext(ctx, `SELECT key FROM settings WHERE key=$1 FOR UPDATE`, key); err != nil {
		return err
	}
	if value.SchedulingMode == "groups" {
		if err = lockLiveGroups(ctx, tx.Client(), value.SchedulingGroupIDs); err != nil {
			return err
		}
	}

	ids := []int64{}
	if value.CookiePool.Enabled {
		ids = append(ids, value.CookiePool.SourceAccountIDs...)
		ids = append(ids, value.CookiePool.TargetAccountIDs...)
	}
	if value.WSSession.Enabled {
		ids = append(ids, value.WSSession.AccountIDs...)
	}
	rows, readErr := tx.Client().QueryContext(ctx, `SELECT value FROM settings WHERE key=$1`, key)
	if readErr != nil {
		return readErr
	}
	if rows.Next() {
		var raw []byte
		if readErr = rows.Scan(&raw); readErr != nil {
			_ = rows.Close()
			return readErr
		}
		var previous config.AstraRoutingSettings
		if json.Unmarshal(raw, &previous) == nil && config.AstraRouteSettingsEqual(previous, value) {
			ids = nil
		}
	}
	if readErr = rows.Err(); readErr != nil {
		_ = rows.Close()
		return readErr
	}
	_ = rows.Close()
	slices.Sort(ids)
	ids = slices.Compact(ids)
	for _, id := range ids {
		ws := value.WSSession.Enabled && slices.Contains(value.WSSession.AccountIDs, id)
		// Reject malformed model mappings rather than replacing an administrator's
		// unknown data. Empty mappings keep their existing allow-all semantics.
		result, updateErr := tx.Client().ExecContext(ctx, `UPDATE accounts SET
   credentials=CASE
    WHEN COALESCE(extra->>'astra_model_disabled','false')='true' THEN credentials
    WHEN credentials->'model_mapping' IS NULL OR credentials->'model_mapping'='null'::jsonb OR credentials->'model_mapping'='{}'::jsonb THEN credentials
    ELSE jsonb_set(credentials,'{model_mapping}',(credentials->'model_mapping') || '{"gpt-6-astra":"gpt-6-astra"}'::jsonb) END,
   extra=COALESCE(extra,'{}'::jsonb) || CASE WHEN $2 THEN '{"openai_oauth_responses_websockets_v2_enabled":true,"openai_oauth_responses_websockets_v2_mode":"ctx_pool","openai_ws_force_http":false}'::jsonb ELSE '{}'::jsonb END,
   updated_at=NOW()
   WHERE id=$1 AND deleted_at IS NULL AND platform='openai' AND type IN ('oauth','setup-token') AND parent_account_id IS NULL
    AND (credentials->'model_mapping' IS NULL OR credentials->'model_mapping'='null'::jsonb OR jsonb_typeof(credentials->'model_mapping')='object')`, id, ws)
		if updateErr != nil {
			return updateErr
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return fmt.Errorf("astra_account_unavailable: %d", id)
		}
		if err := enqueueSchedulerOutbox(ctx, tx.Client(), service.SchedulerOutboxEventAccountChanged, &id, nil, nil); err != nil {
			return err
		}
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if err := (&settingRepository{client: tx.Client()}).Set(ctx, key, string(raw)); err != nil {
		return err
	}
	return tx.Commit()
}

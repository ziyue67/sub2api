package repository

import (
	"context"
	"encoding/json"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

var _ service.AccountExcelBPSRecoveryRepository = (*accountRepository)(nil)

// Compare all BPS options (including the server-owned shutdown/claim markers)
// but allow unrelated usage snapshots to change during the probe.
const excelBPSRecoveryGuardSQL = `
WHERE id = $1 AND deleted_at IS NULL AND parent_account_id IS NULL
 AND platform = 'openai' AND type = 'oauth' AND status = 'active' AND schedulable = true
 AND (auto_pause_on_expired = false OR expires_at IS NULL OR expires_at > NOW())
 AND credentials = $2::jsonb AND COALESCE(proxy_id, 0) = $4
 AND COALESCE(extra -> 'openai_excel_bps', 'false'::jsonb) <> 'true'::jsonb
 AND extra -> 'openai_excel_bps_auto_disable_on_403' = 'true'::jsonb
 AND extra -> 'openai_excel_bps_auto_recover_on_403' = 'true'::jsonb
 AND jsonb_typeof(extra -> 'openai_excel_bps_403_disabled_at') = 'string'
 AND NOT EXISTS (
  SELECT 1 FROM jsonb_object_keys(COALESCE(extra, '{}'::jsonb) || $3::jsonb) AS options(key)
  WHERE options.key ~ '^openai_excel_bps'
   AND (extra -> options.key) IS DISTINCT FROM ($3::jsonb -> options.key)
 )`

func excelBPSRecoveryArgs(account *service.Account) ([]any, error) {
	credentials, err := json.Marshal(account.Credentials)
	if err != nil {
		return nil, err
	}
	extra, err := json.Marshal(account.Extra)
	if err != nil {
		return nil, err
	}
	var proxyID int64
	if account.ProxyID != nil {
		proxyID = *account.ProxyID
	}
	return []any{account.ID, string(credentials), string(extra), proxyID}, nil
}

func (r *accountRepository) ClaimExcelBPS403Probe(ctx context.Context, account *service.Account, now time.Time) (bool, error) {
	if !account.ExcelBPS403RecoveryDue(now) {
		return false, nil
	}
	args, err := excelBPSRecoveryArgs(account)
	if err != nil {
		return false, err
	}
	args = append(args, now.UTC().Format(time.RFC3339Nano))
	result, err := r.sql.ExecContext(ctx, `UPDATE accounts
SET extra = jsonb_set(extra, '{openai_excel_bps_403_last_probe_at}', to_jsonb($5::text)), updated_at = NOW()
`+excelBPSRecoveryGuardSQL, args...)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected > 0, err
}

func (r *accountRepository) RestoreExcelBPSAfter403(ctx context.Context, account *service.Account) (bool, error) {
	if !account.IsExcelBPS403RecoveryPending() {
		return false, nil
	}
	claim, _ := account.Extra[service.ExcelBPS403LastProbeAtKey].(string)
	if _, err := time.Parse(time.RFC3339Nano, claim); err != nil {
		return false, nil
	}
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	txCtx := dbent.NewTxContext(ctx, tx)
	args, err := excelBPSRecoveryArgs(account)
	if err != nil {
		return false, err
	}
	client := clientFromContext(txCtx, r.client)
	result, err := client.ExecContext(txCtx, `UPDATE accounts
SET extra = jsonb_set(extra, '{openai_excel_bps}', 'true'::jsonb)
 - 'openai_excel_bps_403_disabled_at' - 'openai_excel_bps_403_last_probe_at', updated_at = NOW()
`+excelBPSRecoveryGuardSQL, args...)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if affected > 0 {
		if err := enqueueSchedulerOutbox(txCtx, client, service.SchedulerOutboxEventAccountChanged, &account.ID, nil, nil); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	if affected > 0 {
		r.syncSchedulerAccountSnapshot(ctx, account.ID)
	}
	return affected > 0, nil
}

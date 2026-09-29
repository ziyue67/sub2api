package repository

import "context"

// Presence, including a paused enrollment, transfers ownership of automatic
// credential recovery to Credential Operations. The legacy guard must not race
// its worker or undo an operator's pause by trying the other recovery path.
func (r *accountRepository) ListCredentialOperationsAccountIDs(ctx context.Context) ([]int64, error) {
	rows, err := r.sql.QueryContext(ctx, "SELECT account_id FROM account_token_guard_v2_accounts")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

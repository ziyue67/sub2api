//go:build unit

package service

import (
	"context"
	"maps"
	"testing"

	"github.com/stretchr/testify/require"
)

type adminExcelBPSGroupRepo struct {
	accountRepoStubForBulkUpdate
	extraUpdates []map[string]any
}

func (r *adminExcelBPSGroupRepo) UpdateExtra(_ context.Context, _ int64, updates map[string]any) error {
	r.extraUpdates = append(r.extraUpdates, maps.Clone(updates))
	return nil
}

func TestAdminExcelBPS403GroupValidationBeforeWrite(t *testing.T) {
	for _, method := range []string{"create", "update", "extra", "bulk"} {
		for _, valid := range []bool{false, true} {
			if method == "create" && valid {
				continue // Successful OAuth creation starts unrelated asynchronous privacy setup.
			}
			t.Run(method+map[bool]string{false: "/missing target", true: "/explicit leave all"}[valid], func(t *testing.T) {
				account := excelAccount()
				repo := &adminExcelBPSGroupRepo{accountRepoStubForBulkUpdate: accountRepoStubForBulkUpdate{
					getByIDAccounts: map[int64]*Account{account.ID: account}, getByIDsAccounts: []*Account{account},
				}}
				svc := &adminServiceImpl{accountRepo: repo}
				extra := map[string]any{ExcelBPSAutoMoveOn403Key: true}
				if valid {
					extra[ExcelBPS403TargetGroupIDKey] = float64(0)
				}
				ctx := context.Background()
				var err error
				switch method {
				case "create":
					// Invalid input must be rejected before persistence or OAuth side effects.
					_, err = svc.CreateAccount(ctx, &CreateAccountInput{Name: "bps-policy", Platform: PlatformOpenAI, Type: AccountTypeOAuth,
						Credentials: account.Credentials, Extra: extra, SkipDefaultGroupBind: true})
				case "update":
					_, err = svc.UpdateAccount(ctx, account.ID, &UpdateAccountInput{Extra: extra})
				case "extra":
					err = svc.UpdateAccountExtra(ctx, account.ID, extra)
				case "bulk":
					_, err = svc.BulkUpdateAccounts(ctx, &BulkUpdateAccountsInput{AccountIDs: []int64{account.ID}, Extra: extra})
				}
				if valid {
					require.NoError(t, err)
					require.Equal(t, 1, len(repo.updatedAccounts)+len(repo.extraUpdates)+repo.bulkUpdateCalls)
				} else {
					requireApplicationErrorReason(t, err, "OPENAI_EXCEL_BPS_INVALID")
					require.Nil(t, repo.createAccount)
					require.Empty(t, repo.updatedAccounts)
					require.Empty(t, repo.extraUpdates)
					require.Zero(t, repo.bulkUpdateCalls)
				}
			})
		}
	}
}

func TestAdminExcelBPS403GroupPartialUpdateKeepsExplicitTarget(t *testing.T) {
	for _, method := range []string{"extra", "bulk"} {
		account := excelAccount()
		account.Extra[ExcelBPS403TargetGroupIDKey] = float64(0)
		repo := &adminExcelBPSGroupRepo{accountRepoStubForBulkUpdate: accountRepoStubForBulkUpdate{
			getByIDAccounts: map[int64]*Account{account.ID: account}, getByIDsAccounts: []*Account{account},
		}}
		svc := &adminServiceImpl{accountRepo: repo}
		extra := map[string]any{ExcelBPSAutoMoveOn403Key: true}
		var err error
		if method == "extra" {
			err = svc.UpdateAccountExtra(context.Background(), account.ID, extra)
		} else {
			_, err = svc.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{AccountIDs: []int64{account.ID}, Extra: extra})
		}
		require.NoError(t, err)
		require.NotContains(t, account.Extra, ExcelBPSAutoMoveOn403Key, "validation cannot mutate shared account data")
	}
}

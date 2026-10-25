//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type legacyReceiptOnlyBilling struct {
	UsageBillingRepository // money methods panic if the compatibility path calls them
	confirmed              bool
	conflict               error
	checks                 []BatchImageBalanceHoldCommand
}

func (r *legacyReceiptOnlyBilling) ConfirmBatchImageBillingReceipt(_ context.Context, cmd *BatchImageBalanceHoldCommand) (bool, error) {
	r.checks = append(r.checks, *cmd)
	return r.confirmed, r.conflict
}

func TestLegacyHighPrecisionCommittedRetryOnlyConfirmsExactReceipt(t *testing.T) {
	for _, operation := range []string{"hold", "capture", "release"} {
		for _, committed := range []bool{false, true} {
			t.Run(operation+map[bool]string{false: "/uncommitted", true: "/committed"}[committed], func(t *testing.T) {
				job := testSettlingBatchImageJob("legacy-committed-retry")
				hold := 0.0761995125
				job.HoldAmount = &hold
				reader := &legacyReceiptOnlyBilling{confirmed: committed}
				var err error
				switch operation {
				case "hold":
					err = reserveBatchImageBalanceHold(context.Background(), reader, job, "original-payload")
				case "capture":
					err = captureBatchImageBalanceHold(context.Background(), reader, job, 0.0612345678, "original-payload")
				case "release":
					err = releaseBatchImageBalanceHold(context.Background(), reader, job, "original-payload")
				}
				if committed {
					require.NoError(t, err)
				} else {
					require.ErrorIs(t, err, ErrBatchImageLegacyMoneyPrecision)
				}
				require.Len(t, reader.checks, 1)
				require.Equal(t, hold, reader.checks[0].HoldAmount)
				require.Equal(t, job.UserID, reader.checks[0].UserID)
			})
		}
	}
	job := testSettlingBatchImageJob("legacy-conflict")
	hold := 0.0761995125
	job.HoldAmount = &hold
	r := &legacyReceiptOnlyBilling{conflict: ErrUsageBillingRequestConflict}
	require.ErrorIs(t, releaseBatchImageBalanceHold(context.Background(), r, job, "other-payload"), ErrUsageBillingRequestConflict)
}

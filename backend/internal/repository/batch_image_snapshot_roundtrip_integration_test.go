//go:build integration

package repository

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type snapshotSubmitAccounts struct{ account *service.Account }

func (r *snapshotSubmitAccounts) GetByID(context.Context, int64) (*service.Account, error) {
	return r.account, nil
}
func (r *snapshotSubmitAccounts) ListSchedulableByPlatform(context.Context, string) ([]service.Account, error) {
	return []service.Account{*r.account}, nil
}
func (r *snapshotSubmitAccounts) ListSchedulableByGroupIDAndPlatform(context.Context, int64, string) ([]service.Account, error) {
	return []service.Account{*r.account}, nil
}

type snapshotSubmitGroup struct{ group *service.Group }

func (r *snapshotSubmitGroup) GetByIDLite(context.Context, int64) (*service.Group, error) {
	return r.group, nil
}

type snapshotSubmitProvider struct {
	service.BatchImageProvider
	calls int
}

func (*snapshotSubmitProvider) Name() string                          { return service.BatchImageProviderGeminiAPI }
func (*snapshotSubmitProvider) SupportsAccount(*service.Account) bool { return true }
func (p *snapshotSubmitProvider) Submit(context.Context, *service.BatchImageJob, *service.Account, service.BatchImageInput) (*service.BatchProviderJob, error) {
	p.calls++
	return &service.BatchProviderJob{ProviderJobName: "synthetic/provider-job"}, nil
}

func TestBatchSubmitSnapshotDatabaseRoundTripThenSettlement(t *testing.T) {
	for _, tc := range []struct {
		rate, unit, hold float64
		balance          string
	}{
		{1.2345, 0.0761995125, 0.07619951, "99.92380049"},
		{1.2346, 0.0762118580, 0.07621186, "99.92378814"},
	} {
		ctx := context.Background()
		db, client := newBillingAuditDatabase(t)
		user := mustCreateUser(t, client, &service.User{Email: uuid.NewString() + "@example.com", PasswordHash: "hash", Balance: 100})
		price := 0.05
		group := mustCreateGroup(t, client, &service.Group{Name: "snapshot-" + uuid.NewString(), Platform: service.PlatformGemini,
			RateMultiplier: tc.rate, ImagePrice1K: &price, AllowImageGeneration: true, AllowBatchImageGeneration: true,
			BatchImageDiscountMultiplier: 1, BatchImageHoldMultiplier: 1})
		key := mustCreateApiKey(t, client, &service.APIKey{UserID: user.ID, GroupID: &group.ID, Key: "sk-mock-" + uuid.NewString(), Name: "snapshot"})
		account := mustCreateAccount(t, client, &service.Account{Name: "snapshot-" + uuid.NewString(), Platform: service.PlatformGemini,
			Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, RateMultiplier: &tc.rate})
		account.GroupIDs = []int64{group.ID}
		jobs := NewBatchImageRepository(db)
		billing := NewUsageBillingRepository(nil, db)
		provider := &snapshotSubmitProvider{}
		public := &service.BatchImagePublicService{Repo: jobs, BillingRepo: billing, AccountRepo: &snapshotSubmitAccounts{account},
			GroupRepo: &snapshotSubmitGroup{group}, ProviderRegistry: service.NewBatchImageProviderRegistry(provider), Pricing: &batchSnapshotOnlyPrice{},
			Config: &config.Config{BatchImage: config.BatchImageConfig{Enabled: true, MaxItemsPerJobDefault: 2, MaxPromptCharsPerItem: 64,
				DefaultResponseMimeType: "image/png", DefaultImageSize: "1K"}}}
		owner := service.BatchImageOwner{UserID: user.ID, APIKeyID: key.ID, GroupID: &group.ID}
		// Keep the request stable across wall-clock seconds: the service
		// generates a time-based default TaskName when the field is empty.
		req := service.BatchImageSubmitRequest{TaskName: "snapshot-roundtrip", Model: "gemini-2.5-flash-image", Provider: service.BatchImageProviderGeminiAPI,
			ImageSize: "1K", Items: []service.BatchImageSubmitItem{{CustomID: "one", Prompt: "synthetic"}}}
		idempotency := uuid.NewString()
		submitted, err := public.Submit(ctx, owner, req, idempotency)
		require.NoError(t, err)
		_, err = public.Submit(ctx, owner, req, idempotency)
		require.NoError(t, err)
		require.Equal(t, 1, provider.calls)
		// A fresh repository reads NUMERIC(20,10) prices and the exact eight-place
		// hold, rather than keeping the Submit calculation's in-memory floats.
		saved, err := NewBatchImageRepository(db).GetBatchImageJobByBatchID(ctx, submitted.ID)
		require.NoError(t, err)
		require.Equal(t, 2, saved.PricingSnapshotVersion)
		require.Equal(t, tc.hold, *saved.HoldAmount)
		require.Equal(t, tc.unit, saved.BillableUnitPrice)
		require.NoError(t, jobs.TransitionBatchImageJobStatus(ctx, saved.BatchID, service.BatchImageJobStatusIndexing, service.BatchImageTransitionOptions{}))
		_, err = db.ExecContext(ctx, "UPDATE batch_image_jobs SET success_count=1 WHERE batch_id=$1", saved.BatchID)
		require.NoError(t, err) // synthetic provider result, not a real image/model call
		require.NoError(t, jobs.TransitionBatchImageJobStatus(ctx, saved.BatchID, service.BatchImageJobStatusSettling, service.BatchImageTransitionOptions{}))
		settlement := &service.BatchImageSettlementService{Repo: NewBatchImageRepository(db), BillingRepo: billing, Pricing: &batchSnapshotOnlyPrice{}}
		for repeat := 0; repeat < 2; repeat++ {
			result, err := settlement.Settle(ctx, saved.BatchID)
			require.NoError(t, err)
			require.Equal(t, tc.hold, result.ActualCost)
			require.Equal(t, repeat > 0, result.AlreadySettled)
		}
		var balance, frozen, ownedState string
		require.NoError(t, db.QueryRowContext(ctx, "SELECT balance::text,frozen_balance::text FROM users WHERE id=$1", user.ID).Scan(&balance, &frozen))
		require.NoError(t, db.QueryRowContext(ctx, "SELECT state FROM batch_image_money_holds WHERE batch_id=$1 AND api_key_id=$2", saved.BatchID, key.ID).Scan(&ownedState))
		require.Equal(t, tc.balance, balance)
		require.Equal(t, "0.00000000", frozen)
		require.Equal(t, "captured", ownedState)
	}
}

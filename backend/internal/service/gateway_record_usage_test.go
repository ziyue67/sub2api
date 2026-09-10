//go:build unit

package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

func newGatewayRecordUsageServiceForTest(usageRepo UsageLogRepository, userRepo UserRepository, subRepo UserSubscriptionRepository) *GatewayService {
	cfg := &config.Config{}
	cfg.Default.RateMultiplier = 1.1
	return NewGatewayService(
		nil,
		nil,
		usageRepo,
		nil,
		userRepo,
		subRepo,
		nil,
		nil,
		cfg,
		nil,
		nil,
		NewBillingService(cfg, nil),
		nil,
		&BillingCacheService{},
		nil,
		nil,
		&DeferredService{},
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil, // userPlatformQuotaRepo
	)
}

func newGatewayRecordUsageServiceWithBillingRepoForTest(usageRepo UsageLogRepository, billingRepo UsageBillingRepository, userRepo UserRepository, subRepo UserSubscriptionRepository) *GatewayService {
	svc := newGatewayRecordUsageServiceForTest(usageRepo, userRepo, subRepo)
	svc.usageBillingRepo = billingRepo
	return svc
}

type openAIRecordUsageBestEffortLogRepoStub struct {
	UsageLogRepository

	bestEffortErr   error
	createErr       error
	bestEffortCalls int
	createCalls     int
	lastLog         *UsageLog
	lastCtxErr      error
}

func (s *openAIRecordUsageBestEffortLogRepoStub) CreateBestEffort(ctx context.Context, log *UsageLog) error {
	s.bestEffortCalls++
	s.lastLog = log
	s.lastCtxErr = ctx.Err()
	return s.bestEffortErr
}

func (s *openAIRecordUsageBestEffortLogRepoStub) Create(ctx context.Context, log *UsageLog) (bool, error) {
	s.createCalls++
	s.lastLog = log
	s.lastCtxErr = ctx.Err()
	return false, s.createErr
}

func TestGatewayServiceRecordUsage_BillingUsesDetachedContext(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: false, err: context.DeadlineExceeded}
	userRepo := &openAIRecordUsageUserRepoStub{}
	subRepo := &openAIRecordUsageSubRepoStub{}
	quotaSvc := &openAIRecordUsageAPIKeyQuotaStub{}
	svc := newGatewayRecordUsageServiceForTest(usageRepo, userRepo, subRepo)

	reqCtx, cancel := context.WithCancel(context.Background())
	cancel()

	err := svc.RecordUsage(reqCtx, &RecordUsageInput{
		Result: &ForwardResult{
			RequestID: "gateway_detached_ctx",
			Usage: ClaudeUsage{
				InputTokens:  10,
				OutputTokens: 6,
			},
			Model:    "claude-sonnet-4",
			Duration: time.Second,
		},
		APIKey: &APIKey{
			ID:    501,
			Quota: 100,
		},
		User:          &User{ID: 601},
		Account:       &Account{ID: 701},
		APIKeyService: quotaSvc,
	})

	require.NoError(t, err)
	require.Equal(t, 1, usageRepo.calls)
	require.Equal(t, 1, userRepo.deductCalls)
	require.NoError(t, userRepo.lastCtxErr)
	require.Equal(t, 1, quotaSvc.quotaCalls)
	require.NoError(t, quotaSvc.lastQuotaCtxErr)
}

func TestGatewayServiceRecordUsage_BillingFingerprintIncludesRequestPayloadHash(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{}
	billingRepo := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: true}}
	svc := newGatewayRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{})

	payloadHash := HashUsageRequestPayload([]byte(`{"messages":[{"role":"user","content":"hello"}]}`))
	err := svc.RecordUsage(context.Background(), &RecordUsageInput{
		Result: &ForwardResult{
			RequestID: "gateway_payload_hash",
			Usage: ClaudeUsage{
				InputTokens:  10,
				OutputTokens: 6,
			},
			Model:    "claude-sonnet-4",
			Duration: time.Second,
		},
		APIKey:             &APIKey{ID: 501, Quota: 100},
		User:               &User{ID: 601},
		Account:            &Account{ID: 701},
		RequestPayloadHash: payloadHash,
	})
	require.NoError(t, err)
	require.NotNil(t, billingRepo.lastCmd)
	require.Equal(t, payloadHash, billingRepo.lastCmd.RequestPayloadHash)
}

func TestGatewayServiceRecordUsage_BillingFingerprintFallsBackToContextRequestID(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{}
	billingRepo := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: true}}
	svc := newGatewayRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{})

	ctx := context.WithValue(context.Background(), ctxkey.RequestID, "req-local-123")
	err := svc.RecordUsage(ctx, &RecordUsageInput{
		Result: &ForwardResult{
			RequestID: "gateway_payload_fallback",
			Usage: ClaudeUsage{
				InputTokens:  10,
				OutputTokens: 6,
			},
			Model:    "claude-sonnet-4",
			Duration: time.Second,
		},
		APIKey:  &APIKey{ID: 501, Quota: 100},
		User:    &User{ID: 601},
		Account: &Account{ID: 701},
	})
	require.NoError(t, err)
	require.NotNil(t, billingRepo.lastCmd)
	require.Equal(t, "local:req-local-123", billingRepo.lastCmd.RequestPayloadHash)
}

func TestGatewayServiceRecordUsage_PreservesRequestedAndUpstreamModels(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	svc := newGatewayRecordUsageServiceForTest(usageRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{})
	mappedModel := "claude-sonnet-4-20250514"

	err := svc.RecordUsage(context.Background(), &RecordUsageInput{
		Result: &ForwardResult{
			RequestID:     "gateway_models_split",
			Usage:         ClaudeUsage{InputTokens: 10, OutputTokens: 6},
			Model:         "claude-sonnet-4",
			UpstreamModel: mappedModel,
			Duration:      time.Second,
		},
		APIKey:  &APIKey{ID: 501, Quota: 100},
		User:    &User{ID: 601},
		Account: &Account{ID: 701},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	require.Equal(t, "claude-sonnet-4", usageRepo.lastLog.Model)
	require.Equal(t, "claude-sonnet-4", usageRepo.lastLog.RequestedModel)
	require.NotNil(t, usageRepo.lastLog.UpstreamModel)
	require.Equal(t, mappedModel, *usageRepo.lastLog.UpstreamModel)
}

func TestGatewayServiceRecordUsage_GeminiFlashThinkingTierUsesCatalogPrice(t *testing.T) {
	for _, baseModel := range []string{"gemini-3.7-flash", "gemini-3.8-flash"} {
		t.Run(baseModel, func(t *testing.T) {
			usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
			userRepo := &openAIRecordUsageUserRepoStub{}
			svc := newGatewayRecordUsageServiceForTest(usageRepo, userRepo, &openAIRecordUsageSubRepoStub{})
			svc.billingService = NewBillingService(svc.cfg, &PricingService{pricingData: map[string]*LiteLLMModelPricing{
				baseModel: {InputCostPerToken: 0.75e-6, OutputCostPerToken: 3.75e-6, CacheReadInputTokenCost: 0.075e-6},
			}})
			svc.resolver = NewModelPricingResolver(nil, svc.billingService)
			group := &Group{ID: 27, Platform: PlatformGemini, RateMultiplier: 0.15}
			model := baseModel + "-medium"

			err := svc.RecordUsage(context.Background(), &RecordUsageInput{
				Result: &ForwardResult{
					RequestID:     "gemini_thinking_tier",
					Model:         model,
					UpstreamModel: model,
					Usage:         ClaudeUsage{InputTokens: 8498, OutputTokens: 469, CacheReadInputTokens: 159248},
					Duration:      time.Second,
				},
				APIKey:  &APIKey{ID: 501, GroupID: &group.ID, Group: group},
				User:    &User{ID: 601},
				Account: &Account{ID: 701, Platform: PlatformGemini, Type: AccountTypeAPIKey},
			})

			require.NoError(t, err)
			require.NotNil(t, usageRepo.lastLog)
			require.Equal(t, model, usageRepo.lastLog.Model)
			require.InDelta(t, 0.02007585, usageRepo.lastLog.TotalCost, 1e-12)
			require.InDelta(t, 0.0030113775, usageRepo.lastLog.ActualCost, 1e-12)
			require.InDelta(t, 0.0030113775, userRepo.lastAmount, 1e-12)
		})
	}
}

func TestGatewayServiceRecordUsage_PreservesChannelMappedUpstreamModel(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	svc := newGatewayRecordUsageServiceForTest(usageRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{})

	err := svc.RecordUsage(context.Background(), &RecordUsageInput{
		Result: &ForwardResult{
			RequestID:     "gateway_channel_mapping_models",
			Usage:         ClaudeUsage{InputTokens: 10, OutputTokens: 6},
			Model:         "gpt-5.6-terra",
			UpstreamModel: "gpt-5.6-terra",
			Duration:      time.Second,
		},
		APIKey:  &APIKey{ID: 501, Quota: 100},
		User:    &User{ID: 601},
		Account: &Account{ID: 701},
		ChannelUsageFields: ChannelUsageFields{
			OriginalModel:      "gpt-5.6-sol",
			ChannelMappedModel: "gpt-5.6-terra",
		},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	require.Equal(t, "gpt-5.6-sol", usageRepo.lastLog.RequestedModel)
	require.Equal(t, "gpt-5.6-terra", usageRepo.lastLog.Model)
	require.NotNil(t, usageRepo.lastLog.UpstreamModel)
	require.Equal(t, "gpt-5.6-terra", *usageRepo.lastLog.UpstreamModel)
}

func TestGatewayServiceRecordUsage_PreservesLoopedChannelAndAccountUpstreamModel(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	svc := newGatewayRecordUsageServiceForTest(usageRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{})

	err := svc.RecordUsage(context.Background(), &RecordUsageInput{
		Result: &ForwardResult{
			RequestID:     "gateway_looped_mapping_models",
			Usage:         ClaudeUsage{InputTokens: 10, OutputTokens: 6},
			Model:         "gpt-5.6-terra",
			UpstreamModel: "gpt-5.6-sol",
			Duration:      time.Second,
		},
		APIKey:  &APIKey{ID: 501, Quota: 100},
		User:    &User{ID: 601},
		Account: &Account{ID: 701},
		ChannelUsageFields: ChannelUsageFields{
			OriginalModel:      "gpt-5.6-sol",
			ChannelMappedModel: "gpt-5.6-terra",
		},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	require.Equal(t, "gpt-5.6-sol", usageRepo.lastLog.RequestedModel)
	require.Equal(t, "gpt-5.6-terra", usageRepo.lastLog.Model)
	require.NotNil(t, usageRepo.lastLog.UpstreamModel)
	require.Equal(t, "gpt-5.6-sol", *usageRepo.lastLog.UpstreamModel)
}

func TestGatewayServiceRecordUsage_EmptyImageSizeDefaultsBeforeBillingAndPersistence(t *testing.T) {
	imagePrice2K := 0.19
	groupID := int64(901)
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	svc := newGatewayRecordUsageServiceForTest(usageRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{})

	err := svc.RecordUsage(context.Background(), &RecordUsageInput{
		Result: &ForwardResult{
			RequestID:      "gateway_image_default_size",
			Model:          "gemini-image",
			ImageCount:     1,
			ImageInputSize: "auto",
			Duration:       time.Second,
		},
		APIKey: &APIKey{
			ID:      801,
			GroupID: i64p(groupID),
			Group: &Group{
				ID:             groupID,
				RateMultiplier: 1.0,
				ImagePrice2K:   &imagePrice2K,
			},
		},
		User:    &User{ID: 601},
		Account: &Account{ID: 701},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	require.Equal(t, 1, usageRepo.lastLog.ImageCount)
	require.NotNil(t, usageRepo.lastLog.ImageSize)
	require.Equal(t, ImageBillingSize2K, *usageRepo.lastLog.ImageSize)
	require.NotNil(t, usageRepo.lastLog.ImageInputSize)
	require.Equal(t, "auto", *usageRepo.lastLog.ImageInputSize)
	require.NotNil(t, usageRepo.lastLog.ImageSizeSource)
	require.Equal(t, ImageSizeSourceDefault, *usageRepo.lastLog.ImageSizeSource)
	require.InDelta(t, 0.19, usageRepo.lastLog.TotalCost, 1e-12)
	require.InDelta(t, 0.19, usageRepo.lastLog.ActualCost, 1e-12)
}

func TestGatewayServiceRecordUsage_PeakRateAffectsTokenModeImageOutputTokens(t *testing.T) {
	groupID := int64(902)
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	userRepo := &openAIRecordUsageUserRepoStub{}
	svc := newGatewayRecordUsageServiceForTest(usageRepo, userRepo, &openAIRecordUsageSubRepoStub{})
	svc.resolver = newOpenAITokenImageChannelPricingResolverForTest(t, groupID, "gemini-image")

	err := svc.RecordUsage(context.Background(), &RecordUsageInput{
		Result: &ForwardResult{
			RequestID:  "gateway_peak_image_tokens",
			Model:      "gemini-image",
			ImageCount: 1,
			Usage: ClaudeUsage{
				InputTokens:       1000,
				OutputTokens:      600,
				ImageOutputTokens: 100,
			},
			Duration: time.Second,
		},
		APIKey: &APIKey{
			ID:      802,
			GroupID: i64p(groupID),
			Group: &Group{
				ID:                 groupID,
				RateMultiplier:     1.0,
				SubscriptionType:   SubscriptionTypeSubscription,
				PeakRateEnabled:    true,
				PeakStart:          "00:00",
				PeakEnd:            "23:59",
				PeakRateMultiplier: 3.0,
			},
		},
		User:    &User{ID: 602},
		Account: &Account{ID: 702},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	require.NotNil(t, usageRepo.lastLog.BillingMode)
	require.Equal(t, string(BillingModeToken), *usageRepo.lastLog.BillingMode)
	require.Equal(t, 3.0, usageRepo.lastLog.RateMultiplier)

	textInput := 1000 * 3e-6
	textOutput := 500 * 15e-6
	imageOutput := 100 * 15e-6
	expectedActual := (textInput + textOutput + imageOutput) * 3.0

	require.InDelta(t, textInput+textOutput+imageOutput, usageRepo.lastLog.TotalCost, 1e-12)
	require.InDelta(t, imageOutput, usageRepo.lastLog.ImageOutputCost, 1e-12)
	require.InDelta(t, expectedActual, usageRepo.lastLog.ActualCost, 1e-12)
	require.InDelta(t, expectedActual, userRepo.lastAmount, 1e-12)
}

func TestGatewayServiceRecordUsage_TimePricingUsesPricingAt(t *testing.T) {
	groupID := int64(904)
	requestStart := time.Date(2024, time.January, 2, 2, 0, 0, 0, time.UTC) // 上海 10:00
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	userRepo := &openAIRecordUsageUserRepoStub{}
	svc := newGatewayRecordUsageServiceForTest(usageRepo, userRepo, &openAIRecordUsageSubRepoStub{})
	svc.resolver = newOpenAITokenImageChannelPricingResolverWithTimeForTest(t, groupID, "gpt-5.1", &ChannelTimePricing{
		Timezone: "Asia/Shanghai",
		Periods:  []ChannelTimePricingPeriod{{StartTime: "09:00", EndTime: "12:00", Multiplier: 2}},
	})

	err := svc.RecordUsage(context.Background(), &RecordUsageInput{
		Result: &ForwardResult{
			RequestID: "gateway_time_pricing_request_start",
			Model:     "gpt-5.1",
			Usage:     ClaudeUsage{InputTokens: 1000, OutputTokens: 500},
		},
		APIKey: &APIKey{ID: 804, GroupID: i64p(groupID), Group: &Group{
			ID: groupID, RateMultiplier: 0.8, SubscriptionType: SubscriptionTypeSubscription,
		}},
		User:      &User{ID: 604},
		Account:   &Account{ID: 704},
		PricingAt: requestStart,
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	baseCost := 1000*3e-6 + 500*15e-6
	require.InDelta(t, baseCost*2, usageRepo.lastLog.TotalCost, 1e-12)
	require.InDelta(t, baseCost*2*0.8, usageRepo.lastLog.ActualCost, 1e-12)
	require.InDelta(t, 0.8, usageRepo.lastLog.RateMultiplier, 1e-12)
}
func TestGatewayServiceRecordUsage_UsesExplicitPricingAtForPeakRate(t *testing.T) {
	for _, platform := range []string{PlatformAnthropic, PlatformGemini, PlatformGrok, PlatformAntigravity} {
		t.Run(platform, func(t *testing.T) {
			groupID := int64(903)
			usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
			userRepo := &openAIRecordUsageUserRepoStub{}
			svc := newGatewayRecordUsageServiceForTest(usageRepo, userRepo, &openAIRecordUsageSubRepoStub{})
			svc.resolver = newOpenAITokenImageChannelPricingResolverForTest(t, groupID, "gemini-image")

			pricingAt := time.Date(2026, time.January, 1, 0, 30, 0, 0, time.UTC)
			err := svc.RecordUsage(context.Background(), &RecordUsageInput{
				Result: &ForwardResult{
					RequestID:  "gateway_explicit_pricing_at_" + platform,
					Model:      "gemini-image",
					ImageCount: 1,
					Usage: ClaudeUsage{
						InputTokens:       1000,
						OutputTokens:      600,
						ImageOutputTokens: 100,
					},
				},
				APIKey: &APIKey{
					ID:      803,
					GroupID: i64p(groupID),
					Group: &Group{
						ID:                 groupID,
						Platform:           platform,
						RateMultiplier:     1.0,
						SubscriptionType:   SubscriptionTypeSubscription,
						PeakRateEnabled:    true,
						PeakStart:          "00:00",
						PeakEnd:            "01:00",
						PeakRateMultiplier: 3.0,
					},
				},
				User:      &User{ID: 603},
				Account:   &Account{ID: 703, Platform: platform},
				PricingAt: pricingAt,
			})

			require.NoError(t, err)
			require.NotNil(t, usageRepo.lastLog)
			require.Equal(t, 3.0, usageRepo.lastLog.RateMultiplier)
		})
	}
}

func TestGatewayServiceRecordUsage_DeepSeekAccountStatsUsesRequestPricingAtAndUpstreamModel(t *testing.T) {
	for _, model := range []struct {
		name        string
		offPeakCost float64
	}{
		{"deepseek-v4-flash", 1000*2.2e-7 + 500*6.6e-7 + 1000*7e-9},
		{"deepseek-v4-pro", 1000*6.6e-7 + 500*1.98e-6 + 1000*2.2e-8},
	} {
		for _, slot := range []struct {
			name       string
			pricingAt  time.Time
			multiplier float64
		}{
			{"peak", time.Date(2026, time.August, 24, 2, 0, 0, 0, time.UTC), 2},
			{"off_peak", time.Date(2026, time.August, 24, 12, 0, 0, 0, time.UTC), 1},
		} {
			t.Run(model.name+"/"+slot.name, func(t *testing.T) {
				usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
				userRepo := &openAIRecordUsageUserRepoStub{}
				svc := newGatewayRecordUsageServiceForTest(usageRepo, userRepo, &openAIRecordUsageSubRepoStub{})
				groupID := int64(905)
				svc.channelService = newTestChannelServiceForStats(t, &Channel{ID: 1, Status: StatusActive}, groupID, PlatformDeepseek)
				svc.resolver = NewModelPricingResolver(svc.channelService, svc.billingService)
				alias := "customer-chat"
				inputPrice, outputPrice, cachePrice := 1e-6, 2e-6, 1e-7
				group := &Group{ID: groupID, Platform: PlatformDeepseek, RateMultiplier: 0.8,
					ModelPricing: []ChannelModelPricing{{
						Models: []string{alias}, BillingMode: BillingModeToken,
						InputPrice: &inputPrice, OutputPrice: &outputPrice, CacheReadPrice: &cachePrice,
					}},
				}
				err := svc.RecordUsage(context.Background(), &RecordUsageInput{
					Result: &ForwardResult{
						RequestID: "gateway_deepseek_account_stats_" + model.name + "_" + slot.name,
						Model:     alias, UpstreamModel: model.name,
						Usage: ClaudeUsage{InputTokens: 1000, OutputTokens: 500, CacheReadInputTokens: 1000},
					},
					APIKey: &APIKey{ID: 805, GroupID: &groupID, Group: group},
					User:   &User{ID: 605}, Account: &Account{ID: 705, Platform: PlatformDeepseek},
					PricingAt:          slot.pricingAt,
					ChannelUsageFields: ChannelUsageFields{OriginalModel: alias, BillingModelSource: BillingModelSourceRequested},
				})
				require.NoError(t, err)
				require.NotNil(t, usageRepo.lastLog)
				log := usageRepo.lastLog
				require.Equal(t, alias, log.RequestedModel)
				require.NotNil(t, log.UpstreamModel)
				require.Equal(t, model.name, *log.UpstreamModel)
				require.WithinDuration(t, time.Now(), log.CreatedAt, time.Minute)
				require.False(t, log.CreatedAt.Equal(slot.pricingAt), "request pricing time must differ from record creation")
				customerTotal := 1000*inputPrice + 500*outputPrice + 1000*cachePrice
				require.InDelta(t, customerTotal, log.TotalCost, 1e-12)
				require.InDelta(t, customerTotal*0.8, log.ActualCost, 1e-12)
				require.Equal(t, 1, userRepo.deductCalls)
				require.InDelta(t, customerTotal*0.8, userRepo.lastAmount, 1e-12)
				require.NotNil(t, log.AccountStatsCost)
				require.InDelta(t, model.offPeakCost*slot.multiplier, *log.AccountStatsCost, 1e-12,
					"account cost must use the upstream model and historical PricingAt")
			})
		}
	}
}

func TestGatewayServiceRecordUsage_UsageLogWriteErrorDoesNotSkipBilling(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: false, err: MarkUsageLogCreateNotPersisted(context.Canceled)}
	userRepo := &openAIRecordUsageUserRepoStub{}
	subRepo := &openAIRecordUsageSubRepoStub{}
	quotaSvc := &openAIRecordUsageAPIKeyQuotaStub{}
	svc := newGatewayRecordUsageServiceForTest(usageRepo, userRepo, subRepo)

	err := svc.RecordUsage(context.Background(), &RecordUsageInput{
		Result: &ForwardResult{
			RequestID: "gateway_not_persisted",
			Usage: ClaudeUsage{
				InputTokens:  10,
				OutputTokens: 6,
			},
			Model:    "claude-sonnet-4",
			Duration: time.Second,
		},
		APIKey: &APIKey{
			ID:    503,
			Quota: 100,
		},
		User:          &User{ID: 603},
		Account:       &Account{ID: 703},
		APIKeyService: quotaSvc,
	})

	require.NoError(t, err)
	require.Equal(t, 1, usageRepo.calls)
	require.Equal(t, 1, userRepo.deductCalls)
	require.Equal(t, 1, quotaSvc.quotaCalls)
}

func TestGatewayServiceRecordUsage_UsesFallbackRequestIDForUsageLog(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{}
	userRepo := &openAIRecordUsageUserRepoStub{}
	subRepo := &openAIRecordUsageSubRepoStub{}
	svc := newGatewayRecordUsageServiceForTest(usageRepo, userRepo, subRepo)

	ctx := context.WithValue(context.Background(), ctxkey.RequestID, "gateway-local-fallback")
	err := svc.RecordUsage(ctx, &RecordUsageInput{
		Result: &ForwardResult{
			RequestID: "",
			Usage: ClaudeUsage{
				InputTokens:  10,
				OutputTokens: 6,
			},
			Model:    "claude-sonnet-4",
			Duration: time.Second,
		},
		APIKey:  &APIKey{ID: 504},
		User:    &User{ID: 604},
		Account: &Account{ID: 704},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	require.Equal(t, "local:gateway-local-fallback", usageRepo.lastLog.RequestID)
}

func TestGatewayServiceRecordUsage_PrefersClientRequestIDOverUpstreamRequestID(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{}
	billingRepo := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: true}}
	svc := newGatewayRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{})

	ctx := context.WithValue(context.Background(), ctxkey.ClientRequestID, "client-stable-123")
	ctx = context.WithValue(ctx, ctxkey.RequestID, "req-local-ignored")
	err := svc.RecordUsage(ctx, &RecordUsageInput{
		Result: &ForwardResult{
			RequestID: "upstream-volatile-456",
			Usage: ClaudeUsage{
				InputTokens:  10,
				OutputTokens: 6,
			},
			Model:    "claude-sonnet-4",
			Duration: time.Second,
		},
		APIKey:  &APIKey{ID: 506},
		User:    &User{ID: 606},
		Account: &Account{ID: 706},
	})

	require.NoError(t, err)
	require.NotNil(t, billingRepo.lastCmd)
	require.Equal(t, "client:client-stable-123", billingRepo.lastCmd.RequestID)
	require.NotNil(t, usageRepo.lastLog)
	require.Equal(t, "client:client-stable-123", usageRepo.lastLog.RequestID)
}

func TestGatewayServiceRecordUsage_GeneratesRequestIDWhenAllSourcesMissing(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{}
	billingRepo := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: true}}
	svc := newGatewayRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{})

	err := svc.RecordUsage(context.Background(), &RecordUsageInput{
		Result: &ForwardResult{
			RequestID: "",
			Usage: ClaudeUsage{
				InputTokens:  10,
				OutputTokens: 6,
			},
			Model:    "claude-sonnet-4",
			Duration: time.Second,
		},
		APIKey:  &APIKey{ID: 507},
		User:    &User{ID: 607},
		Account: &Account{ID: 707},
	})

	require.NoError(t, err)
	require.NotNil(t, billingRepo.lastCmd)
	require.True(t, strings.HasPrefix(billingRepo.lastCmd.RequestID, "generated:"))
	require.NotNil(t, usageRepo.lastLog)
	require.Equal(t, billingRepo.lastCmd.RequestID, usageRepo.lastLog.RequestID)
}

func TestGatewayServiceRecordUsage_DroppedUsageLogFallsBackToSyncCreate(t *testing.T) {
	// 计费成功后 best-effort 写入被丢弃（队列超时）时必须同步兜底，
	// 否则出现“已扣费但无 usage_log”的对账缺口（issue #3656）。
	usageRepo := &openAIRecordUsageBestEffortLogRepoStub{
		bestEffortErr: MarkUsageLogCreateDropped(errors.New("usage log best-effort queue full")),
	}
	billingRepo := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: true}}
	svc := newGatewayRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{})

	err := svc.RecordUsage(context.Background(), &RecordUsageInput{
		Result: &ForwardResult{
			RequestID: "gateway_drop_usage_log",
			Usage: ClaudeUsage{
				InputTokens:  10,
				OutputTokens: 6,
			},
			Model:    "claude-sonnet-4",
			Duration: time.Second,
		},
		APIKey:  &APIKey{ID: 508},
		User:    &User{ID: 608},
		Account: &Account{ID: 708},
	})

	require.NoError(t, err)
	require.Equal(t, 1, usageRepo.bestEffortCalls)
	require.Equal(t, 1, usageRepo.createCalls)
	// 兜底调用使用的 ctx 必须仍然存活，不能带着已死的 ctx 走过场。
	require.NoError(t, usageRepo.lastCtxErr)
}

func TestGatewayServiceRecordUsage_BillingErrorWritesUnsettledUsageLog(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{}
	billingErr := errors.New("billing tx failed")
	billingRepo := &openAIRecordUsageBillingRepoStub{err: billingErr}
	userRepo := &openAIRecordUsageUserRepoStub{}
	subRepo := &openAIRecordUsageSubRepoStub{}
	svc := newGatewayRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, userRepo, subRepo)

	err := svc.RecordUsage(context.Background(), &RecordUsageInput{
		Result: &ForwardResult{
			RequestID: "gateway_billing_fail",
			Usage: ClaudeUsage{
				InputTokens:  10,
				OutputTokens: 6,
			},
			Model:    "claude-sonnet-4",
			Duration: time.Second,
		},
		APIKey:  &APIKey{ID: 505},
		User:    &User{ID: 605},
		Account: &Account{ID: 705},
	})

	require.ErrorIs(t, err, billingErr)
	require.Equal(t, 1, billingRepo.calls)
	require.Equal(t, 1, usageRepo.calls)
	require.NotNil(t, usageRepo.lastLog)
	require.Equal(t, 10, usageRepo.lastLog.InputTokens)
	require.Equal(t, 6, usageRepo.lastLog.OutputTokens)
	require.Greater(t, usageRepo.lastLog.InputCost, 0.0)
	require.Greater(t, usageRepo.lastLog.OutputCost, 0.0)
	require.Greater(t, usageRepo.lastLog.TotalCost, 0.0)
	require.Zero(t, usageRepo.lastLog.ActualCost)
}

func TestGatewayServiceRecordUsage_InsufficientBalanceInvalidatesBalanceCache(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{}
	cache := &balanceEligibilityCacheStub{balance: 0.30}
	cfg := &config.Config{}
	cfg.Billing.MinimumBalanceReserve = 0.10
	billingCacheSvc := NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingCacheSvc.Stop)

	// billing repo 原子扣费失败（余额不足以覆盖 amount + reserve）→
	// recordUsageCore 应把余额缓存失效，保证下一次 preflight 从 DB 重读并 403。
	billingRepo := &openAIRecordUsageBillingRepoStub{err: ErrInsufficientBalance}
	svc := newGatewayRecordUsageServiceForTest(usageRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{})
	svc.usageBillingRepo = billingRepo
	svc.billingCacheService = billingCacheSvc

	err := svc.RecordUsage(context.Background(), &RecordUsageInput{
		Result: &ForwardResult{
			RequestID: "gateway_billing_insufficient",
			Usage: ClaudeUsage{
				InputTokens:  100,
				OutputTokens: 60,
			},
			Model:    "claude-sonnet-4",
			Duration: time.Second,
		},
		APIKey:  &APIKey{ID: 509},
		User:    &User{ID: 609},
		Account: &Account{ID: 709},
	})

	require.ErrorIs(t, err, ErrInsufficientBalance)
	require.Equal(t, int64(1), cache.invalidateCalls.Load(), "insufficient-balance billing error must invalidate the balance cache")
}

// 钱包不足以覆盖全额时，统一计费事务把余额扣到 reserve 保留线为止（永不为负），
// 返回 BalanceShortfall > 0。RecordUsage 必须：
//   - 视为成功（上游成本已发生，能收的已收），不返回错误；
//   - usage_log.ActualCost 记为实收金额，保证 sum(actual_cost) == 实际扣减；
//   - 失效余额缓存，让下一次预检回源读到 balance == floor → 403。
func TestGatewayServiceRecordUsage_PartialCollectionSettlesUsageLogAndInvalidatesCache(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{}
	cache := &balanceEligibilityCacheStub{balance: 0.30}
	cfg := &config.Config{}
	cfg.Billing.MinimumBalanceReserve = 0.10
	billingCacheSvc := NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingCacheSvc.Stop)

	// 1000/600 tokens 的 claude-sonnet-4 按回退价 ×1.1 约 $0.0132；钱包只剩 0.105，
	// 扣到 floor 0.10 实收 0.005，其余记为 shortfall。
	floor := 0.10
	const collected = 0.005
	billingRepo := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{
		Applied:          true,
		NewBalance:       &floor,
		BalanceCollected: collected,
		BalanceShortfall: 0.0082,
	}}
	svc := newGatewayRecordUsageServiceForTest(usageRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{})
	svc.usageBillingRepo = billingRepo
	svc.billingCacheService = billingCacheSvc

	err := svc.RecordUsage(context.Background(), &RecordUsageInput{
		Result: &ForwardResult{
			RequestID: "gateway_billing_partial",
			Usage: ClaudeUsage{
				InputTokens:  1000,
				OutputTokens: 600,
			},
			Model:    "claude-sonnet-4",
			Duration: time.Second,
		},
		APIKey:  &APIKey{ID: 510},
		User:    &User{ID: 610},
		Account: &Account{ID: 710},
	})

	require.NoError(t, err, "partial collection is a settled request, not a billing failure")
	require.Equal(t, 1, billingRepo.calls)
	require.NotNil(t, billingRepo.lastCmd)
	require.Greater(t, billingRepo.lastCmd.BalanceCost, collected, "the full cost must still be submitted to the billing transaction")
	require.NotNil(t, usageRepo.lastLog)
	require.Greater(t, usageRepo.lastLog.TotalCost, collected, "TotalCost keeps the real upstream cost")
	require.InDelta(t, collected, usageRepo.lastLog.ActualCost, 1e-9, "ActualCost must equal the amount actually collected")
	require.Equal(t, int64(1), cache.invalidateCalls.Load(), "draining to the floor must invalidate the balance cache so the next preflight returns 403")

	// 闭环验证：缓存失效后，预检以 DB 真实余额（== floor）判定，拒绝后续请求。
	cache.cacheMissAfterInvalidate = true
	billingCacheSvc.userRepo = &balanceLoadUserRepoStub{balance: floor}
	err = billingCacheSvc.CheckBillingEligibility(context.Background(), &User{ID: 610}, nil, nil, nil, "")
	require.ErrorIs(t, err, ErrInsufficientBalance)
}

// legacyFloorUserRepoStub 模拟生产 userRepository：实现 DeductBalanceToFloor，
// 让 repo=nil 的 legacy 兜底路径也走“扣到底线为止”的语义。
type legacyFloorUserRepoStub struct {
	openAIRecordUsageUserRepoStub

	deduction  BalanceDeduction
	floorErr   error
	floorCalls int
	lastFloor  float64
}

func (s *legacyFloorUserRepoStub) DeductBalanceToFloor(ctx context.Context, id int64, amount, floor float64) (BalanceDeduction, error) {
	s.floorCalls++
	s.lastAmount = amount
	s.lastFloor = floor
	s.lastCtxErr = ctx.Err()
	if s.floorErr != nil {
		return BalanceDeduction{}, s.floorErr
	}
	return s.deduction, nil
}

func TestGatewayServiceRecordUsage_LegacyFallbackDrainsToFloorAndSettlesUsageLog(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{}
	userRepo := &legacyFloorUserRepoStub{deduction: BalanceDeduction{NewBalance: 0.10, Collected: 0.01, Shortfall: 0.04}}
	cache := &balanceEligibilityCacheStub{balance: 0.11}
	cfg := &config.Config{}
	cfg.Billing.MinimumBalanceReserve = 0.10
	billingCacheSvc := NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingCacheSvc.Stop)

	// repo=nil → legacy fallback；cfg 带 reserve，兜底路径必须把 floor 透传给仓储。
	svc := newGatewayRecordUsageServiceForTest(usageRepo, userRepo, &openAIRecordUsageSubRepoStub{})
	svc.cfg.Billing.MinimumBalanceReserve = 0.10
	svc.billingCacheService = billingCacheSvc

	err := svc.RecordUsage(context.Background(), &RecordUsageInput{
		Result: &ForwardResult{
			RequestID: "gateway_legacy_partial",
			Usage: ClaudeUsage{
				InputTokens:  1000,
				OutputTokens: 600,
			},
			Model:    "claude-sonnet-4",
			Duration: time.Second,
		},
		APIKey:  &APIKey{ID: 511},
		User:    &User{ID: 611},
		Account: &Account{ID: 711},
	})

	require.NoError(t, err)
	require.Equal(t, 1, userRepo.floorCalls, "legacy path must prefer the floor-capable deduction")
	require.Equal(t, 0, userRepo.deductCalls, "strict DeductBalance must not be used when the floor variant is available")
	require.InDelta(t, 0.10, userRepo.lastFloor, 1e-9)
	require.NoError(t, userRepo.lastCtxErr)
	require.NotNil(t, usageRepo.lastLog)
	require.InDelta(t, 0.01, usageRepo.lastLog.ActualCost, 1e-9, "ActualCost must equal the amount actually collected")
	require.Equal(t, int64(1), cache.invalidateCalls.Load())
}

func TestGatewayServiceRecordUsage_LegacyFallbackAtFloorFailsClosed(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{}
	userRepo := &legacyFloorUserRepoStub{floorErr: ErrInsufficientBalance}
	quotaSvc := &openAIRecordUsageAPIKeyQuotaStub{}
	cache := &balanceEligibilityCacheStub{balance: 0.10}
	cfg := &config.Config{}
	cfg.Billing.MinimumBalanceReserve = 0.10
	billingCacheSvc := NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingCacheSvc.Stop)

	svc := newGatewayRecordUsageServiceForTest(usageRepo, userRepo, &openAIRecordUsageSubRepoStub{})
	svc.cfg.Billing.MinimumBalanceReserve = 0.10
	svc.billingCacheService = billingCacheSvc

	err := svc.RecordUsage(context.Background(), &RecordUsageInput{
		Result: &ForwardResult{
			RequestID: "gateway_legacy_at_floor",
			Usage: ClaudeUsage{
				InputTokens:  1000,
				OutputTokens: 600,
			},
			Model:    "claude-sonnet-4",
			Duration: time.Second,
		},
		APIKey:        &APIKey{ID: 512, Quota: 100},
		User:          &User{ID: 612},
		Account:       &Account{ID: 712},
		APIKeyService: quotaSvc,
	})

	// 钱包已经在 floor 上：一分钱都扣不到 → 整笔 fail-closed，usage_log ActualCost=0，
	// 不累加 APIKey quota，并失效缓存。
	require.ErrorIs(t, err, ErrInsufficientBalance)
	require.Equal(t, 1, userRepo.floorCalls)
	require.NotNil(t, usageRepo.lastLog)
	require.Zero(t, usageRepo.lastLog.ActualCost)
	require.Equal(t, 0, quotaSvc.quotaCalls, "quota must not be consumed when nothing was collected")
	// legacy 路径与 recordUsageCore 各失效一次（幂等 DEL），只断言“确实失效过”。
	require.GreaterOrEqual(t, cache.invalidateCalls.Load(), int64(1))
}

func TestGatewayServiceRecordUsage_ReasoningEffortPersisted(t *testing.T) {
	usageRepo := &openAIRecordUsageBestEffortLogRepoStub{}
	svc := newGatewayRecordUsageServiceForTest(usageRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{})

	effort := "max"
	err := svc.RecordUsage(context.Background(), &RecordUsageInput{
		Result: &ForwardResult{
			RequestID: "effort_test",
			Usage: ClaudeUsage{
				InputTokens:  10,
				OutputTokens: 5,
			},
			Model:           "claude-opus-4-6",
			Duration:        time.Second,
			ReasoningEffort: &effort,
		},
		APIKey:  &APIKey{ID: 1},
		User:    &User{ID: 1},
		Account: &Account{ID: 1},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	require.NotNil(t, usageRepo.lastLog.ReasoningEffort)
	require.Equal(t, "max", *usageRepo.lastLog.ReasoningEffort)
}

func TestGatewayServiceRecordUsage_ReasoningEffortNil(t *testing.T) {
	usageRepo := &openAIRecordUsageBestEffortLogRepoStub{}
	svc := newGatewayRecordUsageServiceForTest(usageRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{})

	err := svc.RecordUsage(context.Background(), &RecordUsageInput{
		Result: &ForwardResult{
			RequestID: "no_effort_test",
			Usage: ClaudeUsage{
				InputTokens:  10,
				OutputTokens: 5,
			},
			Model:    "claude-sonnet-4",
			Duration: time.Second,
		},
		APIKey:  &APIKey{ID: 1},
		User:    &User{ID: 1},
		Account: &Account{ID: 1},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	require.Nil(t, usageRepo.lastLog.ReasoningEffort)
}

// newGatewayRecordUsageServiceWithResolverForTest mirrors production wiring for
// token billing: a pricing resolver plus a grouped API key select the unified
// billing path, which is the only one that honours the service tier.
func newGatewayRecordUsageServiceWithResolverForTest(usageRepo UsageLogRepository) (*GatewayService, *APIKey) {
	svc := newGatewayRecordUsageServiceForTest(usageRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{})
	svc.resolver = NewModelPricingResolver(nil, svc.billingService)
	groupID := int64(7)
	return svc, &APIKey{ID: 1, GroupID: &groupID, Group: &Group{ID: groupID, RateMultiplier: 1.0}}
}

func TestGatewayServiceRecordUsage_FastSpeedDowngradedByUpstreamResponse(t *testing.T) {
	usageRepo := &openAIRecordUsageBestEffortLogRepoStub{}
	svc, apiKey := newGatewayRecordUsageServiceWithResolverForTest(usageRepo)

	tier := "fast"
	err := svc.RecordUsage(context.Background(), &RecordUsageInput{
		Result: &ForwardResult{
			RequestID:                   "fast_downgraded_test",
			Usage:                       ClaudeUsage{InputTokens: 100, OutputTokens: 50},
			Model:                       "claude-opus-5",
			Duration:                    time.Second,
			ServiceTier:                 &tier,
			UpstreamResponseServiceTier: "standard",
		},
		APIKey:  apiKey,
		User:    &User{ID: 1},
		Account: &Account{ID: 1, Platform: PlatformAnthropic},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	require.NotNil(t, usageRepo.lastLog.ServiceTier)
	require.Equal(t, "standard", *usageRepo.lastLog.ServiceTier)

	tokens := UsageTokens{InputTokens: 100, OutputTokens: 50}
	standardCost, err := svc.billingService.CalculateCost("claude-opus-5", tokens, 1.0)
	require.NoError(t, err)
	fastCost, err := svc.billingService.CalculateCostWithServiceTier("claude-opus-5", tokens, 1.0, "fast")
	require.NoError(t, err)
	require.Greater(t, fastCost.TotalCost, standardCost.TotalCost, "fast mode must carry a premium for the test to be meaningful")
	require.InDelta(t, standardCost.TotalCost, usageRepo.lastLog.TotalCost, 1e-10)
}

func TestGatewayServiceRecordUsage_FastSpeedHonouredKeepsPremium(t *testing.T) {
	usageRepo := &openAIRecordUsageBestEffortLogRepoStub{}
	svc, apiKey := newGatewayRecordUsageServiceWithResolverForTest(usageRepo)

	tier := "fast"
	err := svc.RecordUsage(context.Background(), &RecordUsageInput{
		Result: &ForwardResult{
			RequestID:                   "fast_honoured_test",
			Usage:                       ClaudeUsage{InputTokens: 100, OutputTokens: 50},
			Model:                       "claude-opus-5",
			Duration:                    time.Second,
			ServiceTier:                 &tier,
			UpstreamResponseServiceTier: "fast",
		},
		APIKey:  apiKey,
		User:    &User{ID: 1},
		Account: &Account{ID: 1, Platform: PlatformAnthropic},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	require.Equal(t, "fast", *usageRepo.lastLog.ServiceTier)

	fastCost, err := svc.billingService.CalculateCostWithServiceTier("claude-opus-5", UsageTokens{InputTokens: 100, OutputTokens: 50}, 1.0, "fast")
	require.NoError(t, err)
	require.InDelta(t, fastCost.TotalCost, usageRepo.lastLog.TotalCost, 1e-10)
}

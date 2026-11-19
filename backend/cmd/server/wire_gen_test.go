package main

import (
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestProvideServiceBuildInfo(t *testing.T) {
	in := handler.BuildInfo{
		Version:   "v-test",
		BuildType: "release",
	}
	out := provideServiceBuildInfo(in)
	require.Equal(t, in.Version, out.Version)
	require.Equal(t, in.BuildType, out.BuildType)
}

func TestProvideCleanup_WithMinimalDependencies_NoPanic(t *testing.T) {
	cfg := &config.Config{}

	oauthSvc := service.NewOAuthService(nil, nil)
	openAIOAuthSvc := service.NewOpenAIOAuthService(nil, nil)
	geminiOAuthSvc := service.NewGeminiOAuthService(nil, nil, nil, nil, cfg)
	antigravityOAuthSvc := service.NewAntigravityOAuthService(nil)

	tokenRefreshSvc := service.NewTokenRefreshService(
		nil,
		oauthSvc,
		openAIOAuthSvc,
		geminiOAuthSvc,
		antigravityOAuthSvc,
		nil,
		nil,
		cfg,
		nil,
	)
	accountExpirySvc := service.NewAccountExpiryService(nil, time.Second)
	codexVersionSyncSvc := service.NewOpenAICodexVersionSyncService(nil, nil, nil, time.Second)
	claudeCodeVersionSyncSvc := service.NewClaudeCodeVersionSyncService(nil, nil, nil, time.Second)
	proxyExpirySvc := service.NewProxyExpiryService(nil, time.Second)
	subscriptionExpirySvc := service.NewSubscriptionExpiryService(nil, time.Second)
	pricingSvc := service.NewPricingService(cfg, nil)
	emailQueueSvc := service.NewEmailQueueService(nil, 1)
	billingCacheSvc := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	idempotencyCleanupSvc := service.NewIdempotencyCleanupService(nil, cfg)
	schedulerSnapshotSvc := service.NewSchedulerSnapshotService(nil, nil, nil, nil, cfg)
	opsSystemLogSinkSvc := service.NewOpsSystemLogSink(nil)
	accountPerformanceSvc := service.NewAccountPerformanceService(nil)

	cleanup := provideCleanup(
		nil, // providerChannels
		nil, // qqBot
		service.ProvideBalanceNotifyService(nil, nil, nil, nil), // balanceNotify
		nil,                                  // autoCreditEmail
		nil,                                  // cfg
		nil,                                  // requestCaptures
		nil,                                  // entClient
		nil,                                  // rdb
		&service.OpsMetricsCollector{},       // opsMetricsCollector
		&service.OpsAggregationService{},     // opsAggregation
		&service.OpsAlertEvaluatorService{},  // opsAlertEvaluator
		&service.OpsCleanupService{},         // opsCleanup
		&service.OpsScheduledReportService{}, // opsScheduledReport
		opsSystemLogSinkSvc,                  // opsSystemLogSink
		nil,                                  // opsService
		nil,                                  // opsIngressReject
		nil,                                  // apiKeyService
		nil,                                  // concurrencyService
		nil,                                  // authCacheInvalidationWorker
		schedulerSnapshotSvc,                 // schedulerSnapshot
		tokenRefreshSvc,                      // tokenRefresh
		accountExpirySvc,                     // accountExpiry
		nil,                                  // cnProviderBalanceCheck
		codexVersionSyncSvc,                  // codexVersionSync
		claudeCodeVersionSyncSvc,             // claudeCodeVersionSync
		proxyExpirySvc,                       // proxyExpiry
		subscriptionExpirySvc,                // subscriptionExpiry
		&service.UsageCleanupService{},       // usageCleanup
		idempotencyCleanupSvc,                // idempotencyCleanup
		service.NewGeminiCachedContentService(nil, nil, cfg), // geminiCachedContent
		&service.BatchImageCleanupService{},                  // batchImageCleanup
		nil,                                                  // imageArchive
		nil,                                                  // batchImageWorker
		pricingSvc,                                           // pricing
		emailQueueSvc,                                        // emailQueue
		billingCacheSvc,                                      // billingCache
		&service.UsageRecordWorkerPool{},                     // usageRecordWorkerPool
		nil,                                                  // usageBillingLanes
		service.NewDigestSessionStore(),                      // digestSessions
		&service.SubscriptionService{},                       // subscriptionService
		oauthSvc,                                             // oauth
		openAIOAuthSvc,                                       // openaiOAuth
		geminiOAuthSvc,                                       // geminiOAuth
		antigravityOAuthSvc,                                  // antigravityOAuth
		nil,                                                  // grokOAuth
		nil,                                                  // openAIGateway
		nil,                                                  // scheduledTestRunner
		nil,                                                  // controlledExperiment
		nil,                                                  // accountOps
		accountPerformanceSvc,                                // accountPerformance
		nil,                                                  // openAIRelogin
		nil,                                                  // accountTokenGuardV2
		nil,                                                  // backupSvc
		nil,                                                  // paymentOrderExpiry
		nil,                                                  // channelMonitorRunner
		nil,                                                  // channelMonitorV2Aggregator
		nil,                                                  // channelMonitorV3Prober
		nil,                                                  // quotaFlusher
		nil,                                                  // upstreamBillingProbe
		nil,                                                  // ollamaCloudUsage
		nil,                                                  // opencodeGoUsage
		nil,                                                  // auditLog
		nil,                                                  // openAIAutoReset
		nil,                                                  // promptAudit
		nil,                                                  // pluginManager
		nil,                                                  // cyberModeration

	)

	require.NotPanics(t, func() {
		cleanup()
	})
}

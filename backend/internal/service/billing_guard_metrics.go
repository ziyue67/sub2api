package service

import (
	"sync/atomic"
	"time"
)

// 计费护栏统一可观测性。
//
// 背景：这条"后付费零超发"防线由多个修复叠加而成——结算封底（#3）、放行前最坏
// 费用预检（#4/#5）、在途预留（#6）、连接池与复核合并（#7）。但此前整条链只有
// write-off 一个指标，而且没有任何暴露点：
//
//   - 预检/预留的"降级、fail-open、被拒"全都只打日志，ops 无法判断护栏是否真的生效；
//   - "真的没钱"与"被最坏费用/预留护栏提前拦下"返回同一个 403 INSUFFICIENT_BALANCE，
//     客服与运营无法区分，用户投诉时无从解释；
//   - 上游桥不执行 max_tokens 这类"预检低估"只有结算封顶指标能间接反映。
//
// 本文件把这些状态变成可查询的数字。所有计数器都是无锁原子量（热路径不能引入
// 互斥），由 BillingGuardStatsSnapshot() 汇总，经 GET /api/v1/admin/ops/billing-guard
// （admin 鉴权，见 internal/server/routes/admin.go）暴露给 ops 面板。
//
// 健康基线：所有 reject 计数按斜率观察即可——余额不足的用户本来就会被拦，
// 关键是 *_fail_open / *_degrade / *abandoned / *release_error 这些
// **护栏失效类**计数应当恒为 0；不增长即说明护栏全程生效。

// BillingPreflightRejectReason 描述余额预检拒绝的**根本原因**。
//
// 这些原因共用同一个对外错误（ErrInsufficientBalance / 403 INSUFFICIENT_BALANCE，
// 保持既有 billing_error 形状不变），但内部语义完全不同：
//   - below_reserve / marker_active / db_truth_below_reserve：用户真的没钱，
//     属于正常业务拒绝，斜率上升是正常的；
//   - worst_case_* / inflight_reservation：用户还有余额，只是付不满"这一笔"的
//     最坏费用，或已被同用户的在途请求占用额度。这类拒绝若要上升，说明预检口径
//     偏保守（可能误伤），需要运维介入调参。
type BillingPreflightRejectReason string

const (
	// BillingRejectMarkerActive 命中"钱包已耗尽"标记（结算路径判定已扣不动）。
	BillingRejectMarkerActive BillingPreflightRejectReason = "marker_active"
	// BillingRejectBelowReserve 缓存余额已 ≤ 封底线。
	BillingRejectBelowReserve BillingPreflightRejectReason = "below_reserve"
	// BillingRejectDBTruthBelowReserve DB 真值复核确认余额 ≤ 封底线。
	BillingRejectDBTruthBelowReserve BillingPreflightRejectReason = "db_truth_below_reserve"
	// BillingRejectWorstCaseCache 无 userRepo（降级装配）时仅凭缓存判定吃不下最坏费用。
	BillingRejectWorstCaseCache BillingPreflightRejectReason = "worst_case_cache_only"
	// BillingRejectWorstCaseDBTruth DB 真值确认吃不下本次最坏费用。
	BillingRejectWorstCaseDBTruth BillingPreflightRejectReason = "worst_case_db_truth"
	// BillingRejectReservationGuard 在途预留累加后击穿封底（并发准入护栏生效）。
	BillingRejectReservationGuard BillingPreflightRejectReason = "inflight_reservation"
)

// AllBillingPreflightRejectReasons 列出全部拒绝原因，供快照与前端稳定枚举。
func AllBillingPreflightRejectReasons() []BillingPreflightRejectReason {
	return []BillingPreflightRejectReason{
		BillingRejectMarkerActive,
		BillingRejectBelowReserve,
		BillingRejectDBTruthBelowReserve,
		BillingRejectWorstCaseCache,
		BillingRejectWorstCaseDBTruth,
		BillingRejectReservationGuard,
	}
}

// 预检拒绝原因计数（按原因分离，避免"护栏误拦"淹没在"真的没钱"里）。
var (
	billingRejectMarkerActiveTotal        atomic.Int64
	billingRejectBelowReserveTotal        atomic.Int64
	billingRejectDBTruthBelowReserveTotal atomic.Int64
	billingRejectWorstCaseCacheTotal      atomic.Int64
	billingRejectWorstCaseDBTruthTotal    atomic.Int64
	billingRejectReservationGuardTotal    atomic.Int64
	billingRejectWorstCaseDBTruthLastUnix atomic.Int64
	billingRejectReservationLastUnix      atomic.Int64
)

// 在途预留计数。这些计数直接回答"并发护栏到底有没有在挡人"。
var (
	billingReservationReservedTotal       atomic.Int64
	billingReservationReleasedTotal       atomic.Int64
	billingReservationRejectedTotal       atomic.Int64
	billingReservationFailOpenTotal       atomic.Int64
	billingReservationFailOpenLastUnix    atomic.Int64
	billingReservationReleaseErrTotal     atomic.Int64
	billingReservationReleaseErrLastUnix  atomic.Int64
	billingReservationAbandonedTotal      atomic.Int64
	billingReservationAbandonedLastUnix   atomic.Int64
	billingSettlementFailedTotal          atomic.Int64
	billingSettlementFailedLastUnix       atomic.Int64
	billingReservationHeldOnFailureTotal  atomic.Int64
	billingPrecheckUnpricedFallbackTotal  atomic.Int64
	billingReservationExpiredReleaseTotal atomic.Int64
	billingReservationRenewTotal          atomic.Int64
	billingReservationRenewErrTotal       atomic.Int64
	// Redis 侧的预留后端不可用、改由 DB（billing_balance_reservations）兜底的次数。
	// 非零即代表正在降级运行：准入仍受护栏保护，但单用户吞吐被行锁串行化。
	billingReservationDBFallbackTotal    atomic.Int64
	billingReservationDBFallbackErrTotal atomic.Int64
	// 双账本都不可用时的 fail-closed、Redis 故障切 DB、共享兜底窗口状态。
	billingReservationRedisFailureTotal        atomic.Int64
	billingReservationFailClosedTotal          atomic.Int64
	billingReservationFallbackActivatedTotal   atomic.Int64
	billingReservationFallbackActivateErrTotal atomic.Int64
	billingReservationFallbackProbeErrTotal    atomic.Int64
	// 切到 DB 兜底账本前把 Redis 存量预留搬进 DB 的结果：
	// imported = 成功搬运的笔数（护栏语义对齐的直接证据）；err = 搬运失败
	// （DB 账本缺少存量预留，护栏会高估可用额度，必须排查）。
	billingReservationFallbackImportedTotal  atomic.Int64
	billingReservationFallbackImportErrTotal atomic.Int64
)

// 复核与降级计数。这些是"护栏静默失效"的直接证据面。
var (
	billingRecheckDBReadsTotal              atomic.Int64
	billingRecheckFailClosedTotal           atomic.Int64
	billingRecheckSkippedNoUserRepoTotal    atomic.Int64
	billingPrecheckDisabledTotal            atomic.Int64
	billingPrecheckUnavailableTotal         atomic.Int64
	billingSubscriptionReservationTotal     atomic.Int64
	billingSubscriptionReserveFailOpenTotal atomic.Int64
	// "结算后的用量必须立即可见"系列：订阅 / API Key 限流用量改为结算内同步落缓存，
	// API Key 额度另加"已结算用量"共享高水位账本（见 apiKeyQuotaUsedLedgerStore）。
	// 三者只要失败就会让预检在一段时间内看到偏小的用量 —— 即额度可被重复放行，
	// 因此失败次数必须可观测（恒为 0 才是健康）。
	billingSubscriptionUsageSyncErrTotal  atomic.Int64
	billingAPIKeyRateLimitSyncErrTotal    atomic.Int64
	billingAPIKeyQuotaLedgerReadErrTotal  atomic.Int64
	billingAPIKeyQuotaLedgerWriteErrTotal atomic.Int64
	// 账本高水位**高于**鉴权快照的次数：即"快照已过期、护栏靠账本兜住"的实证。
	// 它不为 0 是正常的（说明护栏在起作用）；持续为 0 说明鉴权缓存很短命或账本未生效。
	billingAPIKeyQuotaLedgerStaleRescueTotal atomic.Int64
)

// RecordBillingPreflightReject 记录一次余额预检拒绝及其原因。
func RecordBillingPreflightReject(reason BillingPreflightRejectReason) {
	now := time.Now().Unix()
	switch reason {
	case BillingRejectMarkerActive:
		billingRejectMarkerActiveTotal.Add(1)
	case BillingRejectBelowReserve:
		billingRejectBelowReserveTotal.Add(1)
	case BillingRejectDBTruthBelowReserve:
		billingRejectDBTruthBelowReserveTotal.Add(1)
	case BillingRejectWorstCaseCache:
		billingRejectWorstCaseCacheTotal.Add(1)
	case BillingRejectWorstCaseDBTruth:
		billingRejectWorstCaseDBTruthTotal.Add(1)
		billingRejectWorstCaseDBTruthLastUnix.Store(now)
	case BillingRejectReservationGuard:
		billingRejectReservationGuardTotal.Add(1)
		billingRejectReservationLastUnix.Store(now)
	}
}

// RecordBillingReservationAbandoned 记录"结算任务被丢弃、预留被提前放弃归还"。
//
// 出现即说明 usage_record worker 池按 drop/sample 语义丢掉了本应扣费的结算任务：
// 该笔请求既不会扣费（上游成本无法收回），预留也只能立刻归还而不是等结算完成。
// 健康值恒为 0。
func RecordBillingReservationAbandoned() {
	billingReservationAbandonedTotal.Add(1)
	billingReservationAbandonedLastUnix.Store(time.Now().Unix())
}

// RecordBillingSettlementFailedUncharged 记录"结算重试后仍失败、本笔未扣费"（审计 M3）。
// 预留会保留到 TTL 到期而不是立即归还（避免把未收回的额度立刻放给下一笔请求），
// 但上游成本已经发生，需要按 usage_log（actual_cost=0）对账补扣。健康值恒为 0。
func RecordBillingSettlementFailedUncharged() {
	billingSettlementFailedTotal.Add(1)
	billingSettlementFailedLastUnix.Store(time.Now().Unix())
}

// RecordBillingReservationHeldAfterSettlementFailure 记录"结算失败后预留被保留到 TTL"。
func RecordBillingReservationHeldAfterSettlementFailure() {
	billingReservationHeldOnFailureTotal.Add(1)
}

// RecordBillingPrecheckUnpricedFallback 记录"请求无法定价、按保守金额预留"。
// 非 0 说明有模型 / 别名缺少定价，应补齐定价（否则这类请求按固定额预留）。
func RecordBillingPrecheckUnpricedFallback() {
	billingPrecheckUnpricedFallbackTotal.Add(1)
}

// BillingGuardStats 是护栏健康快照。
//
// 判读顺序建议：先看 ReservationFailOpen / RecheckSkippedNoUserRepo /
// ReservationAbandoned / ReservationReleaseErr —— 这四个只要非 0，就说明护栏在
// 某些条件下**静默失效**（不是拦得多，而是根本没拦）；再看
// RejectWorstCase / RejectInflightReservation 的斜率，判断预检是否过保守（误伤）。
type BillingGuardStats struct {
	// 结算侧：write-off（与 GatewayBillingShortfallStats 同源）。
	SettlementShortfallCount    int64 `json:"settlement_shortfall_count"`
	SettlementShortfallMicros   int64 `json:"settlement_shortfall_micros"`
	SettlementShortfallLastUnix int64 `json:"settlement_shortfall_last_unix"`

	// 预检拒绝（按原因）。
	RejectMarkerActive        int64 `json:"reject_marker_active"`
	RejectBelowReserve        int64 `json:"reject_below_reserve"`
	RejectDBTruthBelowReserve int64 `json:"reject_db_truth_below_reserve"`
	RejectWorstCaseCacheOnly  int64 `json:"reject_worst_case_cache_only"`
	RejectWorstCaseDBTruth    int64 `json:"reject_worst_case_db_truth"`
	RejectInflightReservation int64 `json:"reject_inflight_reservation"`
	RejectWorstCaseLastUnix   int64 `json:"reject_worst_case_last_unix"`
	RejectReservationLastUnix int64 `json:"reject_reservation_last_unix"`

	// 在途预留。
	ReservationReserved           int64 `json:"reservation_reserved"`
	ReservationReleased           int64 `json:"reservation_released"`
	ReservationRejected           int64 `json:"reservation_rejected"`
	ReservationFailOpen           int64 `json:"reservation_fail_open"`
	ReservationFailOpenLastUnix   int64 `json:"reservation_fail_open_last_unix"`
	ReservationReleaseErr         int64 `json:"reservation_release_error"`
	ReservationReleaseErrLastUnix int64 `json:"reservation_release_error_last_unix"`
	ReservationAbandoned          int64 `json:"reservation_abandoned"`
	ReservationAbandonedLastUnix  int64 `json:"reservation_abandoned_last_unix"`
	SettlementFailedUncharged     int64 `json:"settlement_failed_uncharged"`
	SettlementFailedLastUnix      int64 `json:"settlement_failed_last_unix"`
	ReservationHeldOnFailure      int64 `json:"reservation_held_on_settlement_failure"`
	PrecheckUnpricedFallback      int64 `json:"precheck_unpriced_fallback"`
	ReservationExpiredRelease     int64 `json:"reservation_expired_release"`
	ReservationRenew              int64 `json:"reservation_renew"`
	ReservationRenewError         int64 `json:"reservation_renew_error"`
	// DB 兜底：Redis 预留后端不可用时的降级准入次数（与失败次数）。
	ReservationDBFallback    int64 `json:"reservation_db_fallback"`
	ReservationDBFallbackErr int64 `json:"reservation_db_fallback_error"`

	ReservationRedisFailure        int64 `json:"reservation_redis_failure"`
	ReservationFailClosed          int64 `json:"reservation_fail_closed"`
	ReservationFallbackActivated   int64 `json:"reservation_fallback_activated"`
	ReservationFallbackActivateErr int64 `json:"reservation_fallback_activate_error"`
	ReservationFallbackProbeErr    int64 `json:"reservation_fallback_probe_error"`
	// 切账本时从 Redis 搬运到的存量预留笔数 / 搬运失败次数。
	ReservationFallbackImported  int64 `json:"reservation_fallback_imported"`
	ReservationFallbackImportErr int64 `json:"reservation_fallback_import_error"`

	// DB 复核与降级。
	RecheckDBReads              int64 `json:"recheck_db_reads"`
	RecheckFailClosed           int64 `json:"recheck_fail_closed"`
	RecheckSkippedNoUserRepo    int64 `json:"recheck_skipped_no_user_repo"`
	PrecheckDisabled            int64 `json:"precheck_disabled"`
	PrecheckUnavailable         int64 `json:"precheck_unavailable"`
	SubscriptionReservation     int64 `json:"subscription_reservation"`
	SubscriptionReserveFailOpen int64 `json:"subscription_reservation_fail_open"`

	// 结算后用量即时可见性（失败 = 预检可能短暂看到偏小用量）。
	SubscriptionUsageSyncError   int64 `json:"subscription_usage_sync_error"`
	APIKeyRateLimitSyncError     int64 `json:"api_key_rate_limit_sync_error"`
	APIKeyQuotaLedgerReadError   int64 `json:"api_key_quota_ledger_read_error"`
	APIKeyQuotaLedgerWriteError  int64 `json:"api_key_quota_ledger_write_error"`
	APIKeyQuotaLedgerStaleRescue int64 `json:"api_key_quota_ledger_stale_rescue"`

	// 派生判据。
	Healthy              bool     `json:"healthy"`
	DegradedSignals      []string `json:"degraded_signals"`
	WorstCaseRejectRatio float64  `json:"worst_case_reject_ratio"`
}

// BillingGuardStatsSnapshot 汇总护栏健康快照。斜率由调用方按时间差分计算。
func BillingGuardStatsSnapshot() BillingGuardStats {
	count, micros, lastUnix := GatewayBillingShortfallStats()
	stats := BillingGuardStats{
		SettlementShortfallCount:    count,
		SettlementShortfallMicros:   micros,
		SettlementShortfallLastUnix: lastUnix,

		RejectMarkerActive:        billingRejectMarkerActiveTotal.Load(),
		RejectBelowReserve:        billingRejectBelowReserveTotal.Load(),
		RejectDBTruthBelowReserve: billingRejectDBTruthBelowReserveTotal.Load(),
		RejectWorstCaseCacheOnly:  billingRejectWorstCaseCacheTotal.Load(),
		RejectWorstCaseDBTruth:    billingRejectWorstCaseDBTruthTotal.Load(),
		RejectInflightReservation: billingRejectReservationGuardTotal.Load(),
		RejectWorstCaseLastUnix:   billingRejectWorstCaseDBTruthLastUnix.Load(),
		RejectReservationLastUnix: billingRejectReservationLastUnix.Load(),

		ReservationReserved:           billingReservationReservedTotal.Load(),
		ReservationReleased:           billingReservationReleasedTotal.Load(),
		ReservationRejected:           billingReservationRejectedTotal.Load(),
		ReservationFailOpen:           billingReservationFailOpenTotal.Load(),
		ReservationFailOpenLastUnix:   billingReservationFailOpenLastUnix.Load(),
		ReservationReleaseErr:         billingReservationReleaseErrTotal.Load(),
		ReservationReleaseErrLastUnix: billingReservationReleaseErrLastUnix.Load(),
		ReservationAbandoned:          billingReservationAbandonedTotal.Load(),
		ReservationAbandonedLastUnix:  billingReservationAbandonedLastUnix.Load(),
		SettlementFailedUncharged:     billingSettlementFailedTotal.Load(),
		SettlementFailedLastUnix:      billingSettlementFailedLastUnix.Load(),
		ReservationHeldOnFailure:      billingReservationHeldOnFailureTotal.Load(),
		PrecheckUnpricedFallback:      billingPrecheckUnpricedFallbackTotal.Load(),
		ReservationExpiredRelease:     billingReservationExpiredReleaseTotal.Load(),
		ReservationRenew:              billingReservationRenewTotal.Load(),
		ReservationRenewError:         billingReservationRenewErrTotal.Load(),
		ReservationDBFallback:         billingReservationDBFallbackTotal.Load(),
		ReservationDBFallbackErr:      billingReservationDBFallbackErrTotal.Load(),

		ReservationRedisFailure:        billingReservationRedisFailureTotal.Load(),
		ReservationFailClosed:          billingReservationFailClosedTotal.Load(),
		ReservationFallbackActivated:   billingReservationFallbackActivatedTotal.Load(),
		ReservationFallbackActivateErr: billingReservationFallbackActivateErrTotal.Load(),
		ReservationFallbackProbeErr:    billingReservationFallbackProbeErrTotal.Load(),
		ReservationFallbackImported:    billingReservationFallbackImportedTotal.Load(),
		ReservationFallbackImportErr:   billingReservationFallbackImportErrTotal.Load(),

		RecheckDBReads:              billingRecheckDBReadsTotal.Load(),
		RecheckFailClosed:           billingRecheckFailClosedTotal.Load(),
		RecheckSkippedNoUserRepo:    billingRecheckSkippedNoUserRepoTotal.Load(),
		PrecheckDisabled:            billingPrecheckDisabledTotal.Load(),
		PrecheckUnavailable:         billingPrecheckUnavailableTotal.Load(),
		SubscriptionReservation:     billingSubscriptionReservationTotal.Load(),
		SubscriptionReserveFailOpen: billingSubscriptionReserveFailOpenTotal.Load(),

		SubscriptionUsageSyncError:   billingSubscriptionUsageSyncErrTotal.Load(),
		APIKeyRateLimitSyncError:     billingAPIKeyRateLimitSyncErrTotal.Load(),
		APIKeyQuotaLedgerReadError:   billingAPIKeyQuotaLedgerReadErrTotal.Load(),
		APIKeyQuotaLedgerWriteError:  billingAPIKeyQuotaLedgerWriteErrTotal.Load(),
		APIKeyQuotaLedgerStaleRescue: billingAPIKeyQuotaLedgerStaleRescueTotal.Load(),
	}

	// 护栏失效类信号：非 0 即说明防线在这些条件下没有生效，需要排查。
	if stats.SettlementShortfallCount > 0 {
		stats.DegradedSignals = append(stats.DegradedSignals,
			"settlement write-off 仍在发生：预检口径未能覆盖全部请求（检查 request_spend_min_output_tokens / default_max_output_tokens）")
	}
	if stats.ReservationFailOpen > 0 {
		stats.DegradedSignals = append(stats.DegradedSignals,
			"在途预留 fail-open：Redis 预留失败时并发护栏静默失效（检查 Redis 可用性）")
	}
	if stats.ReservationDBFallback > 0 {
		stats.DegradedSignals = append(stats.DegradedSignals,
			"在途预留正在走 DB 兜底：Redis 预留后端不可用，准入改由 billing_balance_reservations 行锁串行化（护栏仍在，但单用户吞吐下降；检查 Redis 可用性）")
	}
	if stats.ReservationDBFallbackErr > 0 {
		stats.DegradedSignals = append(stats.DegradedSignals,
			"DB 预留兜底也失败：正式装配会 fail-closed 返回 503；仅无 DB 能力的降级装配才 fail-open（同时检查 Redis 与 PostgreSQL）")
	}
	if stats.ReservationRedisFailure > 0 {
		stats.DegradedSignals = append(stats.DegradedSignals,
			"Redis 预留不可用，已切换到 DB 兜底账本（护栏仍生效，但预留走 PostgreSQL；检查 Redis 可用性）")
	}
	if stats.ReservationFailClosed > 0 {
		stats.DegradedSignals = append(stats.DegradedSignals,
			"Redis 与 DB 兜底同时不可用：预检 fail-closed，用户看到 503")
	}
	if stats.ReservationFallbackActivateErr > 0 || stats.ReservationFallbackProbeErr > 0 {
		stats.DegradedSignals = append(stats.DegradedSignals,
			"DB 兜底窗口抬升或探测失败：跨实例账本一致性变弱（检查 DB 连接与迁移 240）")
	}
	if stats.ReservationFallbackImportErr > 0 {
		stats.DegradedSignals = append(stats.DegradedSignals,
			"切 DB 兜底账本时未能把 Redis 存量预留搬进 DB：两本账会各算一遍同一份额度（最坏重复放行一个预算；检查 Redis 读与 DB 写）")
	}
	if stats.ReservationReleaseErr > 0 {
		stats.DegradedSignals = append(stats.DegradedSignals,
			"预留归还失败：额度会滞留到 TTL 到期（检查 Redis 写入与网络）")
	}
	if stats.ReservationRenewError > 0 {
		stats.DegradedSignals = append(stats.DegradedSignals,
			"预留续期失败：超长请求可能在结算前失去护栏保护（检查 Redis 写入与网络）")
	}
	if stats.ReservationExpiredRelease > 0 {
		stats.DegradedSignals = append(stats.DegradedSignals,
			"存在凭据已过期的归还：说明有请求在预留 TTL 内未完成结算，或结算任务丢失")
	}
	if stats.ReservationAbandoned > 0 {
		stats.DegradedSignals = append(stats.DegradedSignals,
			"结算任务被丢弃：该笔未扣费（usage_record 池 drop/sample 溢出，检查池容量）")
	}
	if stats.SettlementFailedUncharged > 0 {
		stats.DegradedSignals = append(stats.DegradedSignals,
			"结算重试后仍失败：该笔未扣费（usage_log actual_cost=0，按 request_id 对账补扣）")
	}
	if stats.RecheckSkippedNoUserRepo > 0 {
		stats.DegradedSignals = append(stats.DegradedSignals,
			"预检无法读 DB 真值（userRepo 未装配）：降级模式坏账窗口回到修复前")
	}
	if stats.RecheckFailClosed > 0 {
		stats.DegradedSignals = append(stats.DegradedSignals,
			"复核失败触发 fail-closed：用户看到 503（通常伴随连接池打满）")
	}
	if stats.PrecheckDisabled > 0 {
		stats.DegradedSignals = append(stats.DegradedSignals,
			"最坏费用预检被显式关闭（request_spend_precheck_disabled=true）")
	}
	if stats.PrecheckUnavailable > 0 {
		stats.DegradedSignals = append(stats.DegradedSignals,
			"部分请求无法给出最坏费用上界（无定价/依赖缺失），闸门未生效")
	}
	if stats.SubscriptionUsageSyncError > 0 {
		stats.DegradedSignals = append(stats.DegradedSignals,
			"订阅用量同步落缓存失败：该笔结算的用量只能靠缓存失效 + DB 回源补齐（检查 Redis 写入）")
	}
	if stats.APIKeyRateLimitSyncError > 0 {
		stats.DegradedSignals = append(stats.DegradedSignals,
			"API Key 限流用量同步落缓存失败：限流窗口可能短暂按偏小用量放行（检查 Redis 写入）")
	}
	if stats.APIKeyQuotaLedgerReadError > 0 || stats.APIKeyQuotaLedgerWriteError > 0 {
		stats.DegradedSignals = append(stats.DegradedSignals,
			"API Key 已用额度账本读写失败：该 key 的额度校验可能退回过期快照（检查 Redis 可用性）")
	}
	stats.Healthy = len(stats.DegradedSignals) == 0

	// 误伤比例：最坏费用/预留类拒绝占全部预检拒绝的比例。持续偏高说明预检过保守。
	worstCase := stats.RejectWorstCaseCacheOnly + stats.RejectWorstCaseDBTruth + stats.RejectInflightReservation
	total := worstCase + stats.RejectMarkerActive + stats.RejectBelowReserve + stats.RejectDBTruthBelowReserve
	if total > 0 {
		stats.WorstCaseRejectRatio = float64(worstCase) / float64(total)
	}
	return stats
}

// 以下为各记录点使用的导出记录器。集中在一处，避免各调用点散落直接操纵原子量。

// RecordBillingReservationReserved 记录一次成功的在途预留。
func RecordBillingReservationReserved() { billingReservationReservedTotal.Add(1) }

// RecordBillingReservationRejected 记录一次"预留累加后击穿封底"的拒绝（并发护栏生效）。
func RecordBillingReservationRejected() { billingReservationRejectedTotal.Add(1) }

// RecordBillingReservationFailOpen 记录一次预留 fail-open（Redis 故障时护栏失效）。
func RecordBillingReservationFailOpen() {
	billingReservationFailOpenTotal.Add(1)
	billingReservationFailOpenLastUnix.Store(time.Now().Unix())
}

// RecordBillingReservationReleased 记录一次成功的预留归还。
func RecordBillingReservationReleased() { billingReservationReleasedTotal.Add(1) }

// RecordBillingReservationReleaseError 记录一次归还失败（额度将滞留到 TTL 到期）。
func RecordBillingReservationReleaseError() {
	billingReservationReleaseErrTotal.Add(1)
	billingReservationReleaseErrLastUnix.Store(time.Now().Unix())
}

// RecordBillingReservationExpiredRelease 记录一次"凭据已过期、归还被安全忽略"。
//
// 这不是故障，而是预留 TTL 自愈按预期工作的证据：健康部署下它只会因
// "结算任务丢失"或"心跳未覆盖的超长请求"而出现少量增长。
func RecordBillingReservationExpiredRelease() { billingReservationExpiredReleaseTotal.Add(1) }

// RecordBillingReservationRenew 记录一次成功的预留续期（长请求心跳）。
func RecordBillingReservationRenew() { billingReservationRenewTotal.Add(1) }

// RecordBillingReservationRenewError 记录一次续期失败（非"凭据已过期"）。
func RecordBillingReservationRenewError() { billingReservationRenewErrTotal.Add(1) }

// RecordBillingReservationDBFallback 记录一次"Redis 预留后端不可用、改由 DB 兜底"的准入。
//
// 出现即代表正在降级运行：护栏仍然成立（余额不会被并发击穿），但同一用户的准入被
// 数据库行锁串行化，吞吐显著下降。运维应据此排查 Redis 可用性。
func RecordBillingReservationDBFallback() { billingReservationDBFallbackTotal.Add(1) }

// RecordBillingReservationDBFallbackError 记录一次"DB 兜底也失败"。
//
// 这是比 FailOpen 更严重的信号：它意味着 Redis 与 PostgreSQL 同时不可用，该笔请求
// 正式装配会由服务层转成 503；仅未装配 DB 兜底的降级/测试装配才会 fail-open。
func RecordBillingReservationDBFallbackError() { billingReservationDBFallbackErrTotal.Add(1) }

// RecordBillingReservationRedisFailure 记录一次 Redis 预留失败（即将切换到 DB 兜底）。
// 与 FailOpen 的区别：它表示护栏已降级到 DB 但仍生效；FailOpen 表示护栏真正消失。
func RecordBillingReservationRedisFailure() { billingReservationRedisFailureTotal.Add(1) }

// RecordBillingReservationFailClosed 记录一次 Redis 与 DB 兜底都不可用的拒绝（503）。
func RecordBillingReservationFailClosed() { billingReservationFailClosedTotal.Add(1) }

// RecordBillingReservationFallbackActivated 记录一次本实例抬起共享 DB 兜底窗口。
func RecordBillingReservationFallbackActivated() { billingReservationFallbackActivatedTotal.Add(1) }

// RecordBillingReservationFallbackActivationError 记录一次共享兜底窗口抬升失败。
func RecordBillingReservationFallbackActivationError() {
	billingReservationFallbackActivateErrTotal.Add(1)
}

// RecordBillingReservationFallbackProbeError 记录一次共享兜底窗口探测失败。
func RecordBillingReservationFallbackProbeError() { billingReservationFallbackProbeErrTotal.Add(1) }

// RecordBillingReservationFallbackImported 记录切账本时从 Redis 搬运的存量预留笔数。
// 非零即证明"两本账语义已对齐"，是修复生效的直接证据。
func RecordBillingReservationFallbackImported(count int) {
	if count <= 0 {
		return
	}
	billingReservationFallbackImportedTotal.Add(int64(count))
}

// RecordBillingReservationFallbackImportError 记录一次"存量预留搬运失败"。
//
// 出现即说明 DB 账本缺少切换前已放行的预留，护栏会在故障期高估可用额度
// （最坏重复放行一个预算）。必须排查 Redis 读与 DB 写。
func RecordBillingReservationFallbackImportError() {
	billingReservationFallbackImportErrTotal.Add(1)
}

// RecordBillingRecheckDBRead 记录一次预检的 DB 真值回源。
func RecordBillingRecheckDBRead() { billingRecheckDBReadsTotal.Add(1) }

// RecordBillingRecheckFailClosed 记录一次复核失败导致的 fail-closed（用户可见 503）。
func RecordBillingRecheckFailClosed() { billingRecheckFailClosedTotal.Add(1) }

// RecordBillingRecheckSkippedNoUserRepo 记录一次"因缺 userRepo 而无法回源复核"。
//
// 出现即代表部署处于降级装配：预检只能依据可能偏高的缓存值，
// 坏账窗口回到本次修复之前。
func RecordBillingRecheckSkippedNoUserRepo() { billingRecheckSkippedNoUserRepoTotal.Add(1) }

// RecordBillingPrecheckDisabled 记录一次"最坏费用预检被配置显式关闭"。
func RecordBillingPrecheckDisabled() { billingPrecheckDisabledTotal.Add(1) }

// RecordBillingPrecheckUnavailable 记录一次"无法给出最坏费用上界"（闸门未生效）。
func RecordBillingPrecheckUnavailable() { billingPrecheckUnavailableTotal.Add(1) }

// RecordBillingSubscriptionReservation 记录一次订阅模式占位预留。
func RecordBillingSubscriptionReservation() { billingSubscriptionReservationTotal.Add(1) }

// RecordBillingSubscriptionReserveFailOpen 记录一次订阅模式预留 fail-open。
func RecordBillingSubscriptionReserveFailOpen() { billingSubscriptionReserveFailOpenTotal.Add(1) }

// RecordBillingSubscriptionUsageSyncError 记录一次"结算后订阅用量同步落缓存失败"。
//
// 出现即说明本次结算的用量没能写进 Redis，只能靠缓存失效 + DB 回源补齐；
// 若 Redis 同时不可用，预检会在缓存 TTL 内看到偏小的订阅用量（额度可被重复放行）。
func RecordBillingSubscriptionUsageSyncError() { billingSubscriptionUsageSyncErrTotal.Add(1) }

// RecordBillingAPIKeyRateLimitSyncError 记录一次"结算后 API Key 限流用量同步落缓存失败"。
func RecordBillingAPIKeyRateLimitSyncError() { billingAPIKeyRateLimitSyncErrTotal.Add(1) }

// RecordBillingAPIKeyQuotaLedgerReadError 记录一次"读取 API Key 已用额度账本失败"。
//
// 读取失败时准入退回只用鉴权快照（方向偏松），因此这是护栏降级信号，须排查 Redis。
func RecordBillingAPIKeyQuotaLedgerReadError() { billingAPIKeyQuotaLedgerReadErrTotal.Add(1) }

// RecordBillingAPIKeyQuotaLedgerWriteError 记录一次"发布 API Key 已用额度账本失败"。
//
// 写入失败意味着该 key 的下一次预检可能继续用旧快照判定（额度可在快照 TTL 内被重复
// 放行）；调用方会同时失效该 key 的鉴权缓存来兜底，但失败本身必须可观测。
func RecordBillingAPIKeyQuotaLedgerWriteError() { billingAPIKeyQuotaLedgerWriteErrTotal.Add(1) }

// RecordBillingAPIKeyQuotaLedgerStaleRescue 记录一次"账本高水位领先鉴权快照"。
//
// 这不是故障，而是本修复生效的直接证据：说明鉴权快照已经过期，是账本把已用额度
// 拉了回来（否则这一批请求会按冻结的旧 quota_used 重复放行）。
func RecordBillingAPIKeyQuotaLedgerStaleRescue() { billingAPIKeyQuotaLedgerStaleRescueTotal.Add(1) }

// BillingGuardRuntimeInfo 返回护栏的静态运行参数。
//
// 这些值是"护栏的行为契约"（预留 TTL、心跳间隔、保守折算常量），把它们和计数一起
// 暴露出去，ops 才能在不下代码的情况下判断某个斜率是配置问题还是实现问题：
// 例如 write-off 上升而 min_output_tokens 仍是 0，就说明该把下限打开了。
func BillingGuardRuntimeInfo() map[string]any {
	return map[string]any{
		"reservation_ttl_seconds":       int64(billingReservationTTL / time.Second),
		"reservation_heartbeat_seconds": int64(billingReservationHeartbeatInterval / time.Second),
		"release_timeout_seconds":       int64(billingReservationReleaseTimeout / time.Second),
		"default_max_output_tokens":     requestSpendFallbackMaxOutputTokens,
		"fallback_safety_multiplier":    requestSpendFallbackSafetyMultiplier,
		"input_overhead_tokens":         requestSpendInputOverheadTokens,
		"min_inline_binary_run":         requestSpendMinInlineBinaryRun,
		"image_token_allowance":         requestSpendImageTokenAllowance,
		"reject_reasons":                AllBillingPreflightRejectReasons(),
	}
}

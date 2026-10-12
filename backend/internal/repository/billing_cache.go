package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"math"
	"math/rand/v2"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"
)

const (
	billingBalanceKeyPrefix   = "billing:balance:"
	billingSubKeyPrefix       = "billing:sub:"
	billingRateLimitKeyPrefix = "apikey:rate:"
	// billingBalanceExhaustedKeyPrefix 是"钱包已耗尽（余额已到 reserve 底线）"标记键。
	// 该标记与余额缓存（billing:balance:）严格分离：只有"结算判定钱包已耗尽"与
	// "余额增加"两条路径会写它，余额未命中回源的异步写回永远不会写它，
	// 因此它不会被扣费前的旧余额快照"复活"。
	billingBalanceExhaustedKeyPrefix = "billing:balance_exhausted:"
	// billingReservedKeyPrefix 是"在途预留"键前缀：该用户当前全部"已放行、尚未结算"请求的
	// 最坏费用上界之和（USD）。预检放行前用 INCRBYFLOAT 原子累加、结算完成后归还，
	// 使"余额 - 在途预留 >= 封底"成为跨请求的原子准入护栏，堵住并发突发时用同一份
	// 余额快照全部放行、结算时却扣不动钱的坏账（键带 TTL 自愈，崩溃/丢任务不会永久钉住余额）。
	billingReservedKeyPrefix = "billing:reserved:"
	// billingReservedItemKeyPrefix 是"单笔预留凭据"键前缀：每个请求用**自己的**
	// requestID 在 Redis 上留一条金额凭据（TTL 与预留一致）。
	//
	// 为什么必须有它：上面的聚合键只有"总额"一个标量，无法回答"这笔归还到底是不是
	// 我自己那笔"。当某个请求在途时间超过预留 TTL（长流式响应完全可能），它的聚合份额
	// 已被 TTL 自愈清掉，而它结算时的递减会**吃掉同一用户后来那笔请求的预留**，
	// 甚至把聚合键 DEL 掉 —— 护栏被错误解除，并发超额窗口原样复现。
	//
	// 有了凭据键，归还变成"只有我这条凭据还在，才允许递减，且递减金额以凭据为准"：
	// 凭据过期的归还一律 no-op，绝不会触碰别人的预留。代价是聚合键可能短暂偏高
	// （已过期但未归还的份额要等聚合键 TTL 兜底），这是保守方向，不会放行超额。
	billingReservedItemKeyPrefix = "billing:resv_item:"
	// billingReservedAmountKeyPrefix 是与 billingReservedKeyPrefix（ZSET 过期索引）配对的
	// HASH（requestID -> 金额）。聚合总额由存活成员实时求和，不再维护标量计数。
	billingReservedAmountKeyPrefix = "billing:reserved_amt:"
	subCacheInvalidateChannel      = "subscription:cache:invalidate"
	billingCacheTTL                = 5 * time.Minute
	billingCacheJitter             = 30 * time.Second
	// apiKeyQuotaUsedLedgerKeyPrefix 是 API Key 额度「已结算用量」共享账本的键前缀。
	//
	// 为什么需要它：API Key 总额度（api_key.quota）的准入读的是鉴权缓存里的 quota_used
	// 快照（L1 默认 15s / L2 默认 300s），而结算只在 DB 里原子递增 quota_used
	// （usage_billing_repo 的 incrementUsageBillingAPIKeyQuota）。在快照过期前，
	// 预检看到的「已用额度」是**冻结**的旧值：等此前放行的请求陆续结算并归还完在途预留，
	// 聚合预留归零而快照里的 quota_used 仍是旧值，于是同一个 key 可以在一个快照周期内
	// 把额度重复放行一遍（≈ 一个完整额度上限的超发）。
	//
	// 本账本把「结算提交后的 DB 真值」写进 Redis（单调不减），准入取
	// max(鉴权快照 quota_used, 账本)，使已用额度不再随快照冻结。账本值恒 ≤ DB 真值
	// （写入的就是 DB 的 RETURNING 结果），因此只会让准入更保守，不会放行超出
	// DB 已用额度的请求。
	apiKeyQuotaUsedLedgerKeyPrefix = "billing:apikey_quota_used:"
	// balanceExhaustedMarkerTTL 必须 >= 余额缓存的最长存活时间（billingCacheTTL），
	// 否则标记先过期、而余额缓存里仍留着偏高的旧值，预检又会被放行。
	balanceExhaustedMarkerTTL = billingCacheTTL + time.Minute
	rateLimitCacheTTL         = 7 * 24 * time.Hour // 7 days matches the longest window
	// apiKeyQuotaUsedLedgerTTL 是共享账本的存活时间：只需覆盖「结算提交 → 下一次鉴权
	// 缓存回源读到同一份 DB 真值」的最长时延（L2 默认 300s），取 30 分钟留足余量。
	// 过期后账本归零，最坏退化为修复前行为（靠快照 + 在途预留把关）。
	apiKeyQuotaUsedLedgerTTL = 30 * time.Minute

	// billingBalanceGenerationKeyPrefix 是余额缓存的**变动代号**键前缀。
	//
	// 它解决的是"回源写回把旧快照重新发布"这一竞态（审计 R9）：余额缓存是
	// "未命中回源 + 异步写回"，而读取方从 DB 读到值到真正写回缓存之间可能发生结算扣费。
	// 扣费会递减缓存，但如果此刻缓存键正好不存在（`deductBalanceScript` 直接返回 0、
	// 不写任何值），随后落下的旧快照就会把**偏高**的余额重新发布出去，使预检在整个
	// 缓存 TTL 内持续放行注定扣费失败的请求。
	//
	// 办法是给每次余额变动递增一个代号：读取方在**发起 DB 读取之前**取一份代号，
	// 写回时只有代号未变才发布（见 publishBalanceScript）。用代号而不是时间戳是为了
	// 免去多实例间的时钟偏移问题 —— 比较的是同一条 Redis 上的单调计数。
	billingBalanceGenerationKeyPrefix = "billing:balance_gen:"
	// balanceGenerationTTL 是代号键的存活时间。只需覆盖"DB 读取 + 写回"的最大时延
	// （回源超时 3s + 队列等待），取 5 分钟与余额缓存同量级即可；过期后代号归零，
	// 两侧同读 '0'，最坏退化为"不设防"，与修复前行为一致。
	balanceGenerationTTL = 10 * time.Minute
	// balanceGenerationTTLSeconds 是上面 TTL 的秒数形式（脚本参数用秒）。
	balanceGenerationTTLSeconds = int(balanceGenerationTTL / time.Second)

	// Rate limit window durations — must match service.RateLimitWindow* constants.
	rateLimitWindow5h = 5 * time.Hour
	rateLimitWindow1d = 24 * time.Hour
	rateLimitWindow7d = 7 * 24 * time.Hour

	// billingSubGenerationKeyPrefix 是订阅缓存的**变动代号**键前缀（与余额缓存的
	// billing:balance_gen 同构，审计 R9/R10）。
	//
	// 它解决的是订阅缓存的同一类竞态：缓存是"未命中回源 + 异步写回"，读取方从 DB 读到
	// usage 到真正写回缓存之间有窗口；若窗口内发生了结算（同步把 Redis usage 累加），
	// 迟到的旧快照会把累加结果**覆盖回偏小值** —— 预检随即按偏小的用量放行，
	// 订阅限额被重复消耗（与 API Key 额度冻结快照同构）。
	//
	// 办法与余额一致：每次订阅用量变动递增一个代号；读取方在**发起 DB 读取之前**取一份
	// 代号，写回时只有代号未变才发布。比较的是同一条 Redis 上的单调计数，
	// 天然免疫多实例时钟偏移（不用时间戳比较）。
	billingSubGenerationKeyPrefix = "billing:sub_gen:"
	// subscriptionGenerationTTL 是代号键的存活时间：只需覆盖"DB 读取 + 写回"的最大时延
	// （回源超时 + 队列等待），取 10 分钟与余额代号一致；过期后两侧同读 '0'，
	// 最坏退化为"不设防"（与修复前行为一致）。
	subscriptionGenerationTTL = 10 * time.Minute
	// subscriptionGenerationTTLSeconds 是上面 TTL 的秒数形式（脚本参数用秒）。
	subscriptionGenerationTTLSeconds = int(subscriptionGenerationTTL / time.Second)
)

// jitteredTTL 返回带随机抖动的 TTL，防止缓存雪崩
func jitteredTTL() time.Duration {
	// 只做“减法抖动”，确保实际 TTL 不会超过 billingCacheTTL（避免上界预期被打破）。
	if billingCacheJitter <= 0 {
		return billingCacheTTL
	}
	jitter := time.Duration(rand.IntN(int(billingCacheJitter)))
	return billingCacheTTL - jitter
}

// billingBalanceKey generates the Redis key for user balance cache.
func billingBalanceKey(userID int64) string {
	return fmt.Sprintf("%s%d", billingBalanceKeyPrefix, userID)
}

// billingBalanceGenerationKey generates the Redis key for a user's balance generation
// counter (bumped on every balance mutation; used to discard stale回源 snapshots).
func billingBalanceGenerationKey(userID int64) string {
	return fmt.Sprintf("%s%d", billingBalanceGenerationKeyPrefix, userID)
}

// billingBalanceExhaustedKey generates the Redis key for the "wallet exhausted" marker.
func billingBalanceExhaustedKey(userID int64) string {
	return fmt.Sprintf("%s%d", billingBalanceExhaustedKeyPrefix, userID)
}

// billingReservedKey generates the Redis key for a reservation scope's in-flight total.
//
// scope 由服务层构造，必须是"同一份可被并发放大的额度"的唯一标识：
//   - 余额模式：用户的 userID；
//   - 订阅模式："sub:<userID>:<groupID>"（限额挂在用户 × 分组上，与余额互不干扰）。
func billingReservedKey(scope string) string {
	return billingReservedKeyPrefix + "{" + scope + "}"
}

// billingReservedAmountKey generates the Redis HASH (requestID -> amount) paired with
// the billingReservedKey ZSET index.
func billingReservedAmountKey(scope string) string {
	return billingReservedAmountKeyPrefix + "{" + scope + "}"
}

// billingReservedItemKey generates the Redis key holding one request's reservation
// receipt (amount + its own TTL). scope/requestID must already be sanitized by the caller.
// The scope is wrapped in a hash tag so all three reservation keys share one Cluster slot.
func billingReservedItemKey(scope, requestID string) string {
	return billingReservedItemKeyPrefix + "{" + scope + "}:" + requestID
}

// billingReservationKeys returns the keys used by the reservation scripts. Wallet scopes
// (plain userID) also pass the legacy wallet in-flight keys (same {uid} hash tag) so the
// aggregate counts reservations made by not-yet-upgraded instances during a rolling deploy.
func billingReservationKeys(scope, requestID string) []string {
	keys := []string{billingReservedKey(scope), billingReservedAmountKey(scope), billingReservedItemKey(scope, requestID)}
	if userID, ok := reservationUserIDFromScope(scope); ok {
		legacyZSet, legacyHash := billingInflightKeys(userID)
		keys = append(keys, legacyZSet, legacyHash)
	}
	return keys
}

// reservationPruneSumLua 是预留脚本共享的前导：清理过期成员并对存活成员求和，
// 以及在登记/续期后把索引键 TTL 至少延长到本笔 TTL（索引键随最后一笔存活预留过期）。
// 约定 ARGV[1] = now_ms。
const reservationPruneSumLua = `
	local now = tonumber(ARGV[1])
	local function prune_and_sum()
		local expired = redis.call('ZRANGEBYSCORE', KEYS[1], '-inf', now)
		if #expired > 0 then
			redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', now)
			redis.call('HDEL', KEYS[2], unpack(expired))
		end
		local vals = redis.call('HVALS', KEYS[2])
		local sum = 0
		for i = 1, #vals do
			sum = sum + (tonumber(vals[i]) or 0)
		end
		if #vals == 0 then
			redis.call('DEL', KEYS[1], KEYS[2])
		end
		-- 钱包 scope 额外计入旧版钱包预留（billing:inflight:{uid} / billing:inflight_amt:{uid}）：
		-- 滚动发布期间旧实例仍在那里登记在途请求，不计入会让新旧实例各自放行同一份余额。
		if KEYS[4] ~= nil and KEYS[5] ~= nil then
			local legacyExpired = redis.call('ZRANGEBYSCORE', KEYS[4], '-inf', now)
			if #legacyExpired > 0 then
				redis.call('ZREMRANGEBYSCORE', KEYS[4], '-inf', now)
				redis.call('HDEL', KEYS[5], unpack(legacyExpired))
			end
			local legacy = redis.call('HGETALL', KEYS[5])
			for i = 1, #legacy, 2 do
				if string.sub(legacy[i], 1, 3) ~= 'v2:' then
					sum = sum + (tonumber(legacy[i + 1]) or 0)
				end
			end
		end
		return sum
	end
	-- 镜像到旧版钱包预留键（成员名加前缀避免与旧实例自己的成员冲突）：滚动发布期间
	-- 旧实例只读旧键，镜像让它们也能看到新实例的在途预留。汇总时跳过镜像成员，避免重复计数。
	local function mirror_legacy(member, amount, expireAt)
		if KEYS[4] ~= nil and KEYS[5] ~= nil then
			redis.call('ZADD', KEYS[4], expireAt, 'v2:' .. member)
			redis.call('HSET', KEYS[5], 'v2:' .. member, amount)
			if redis.call('PTTL', KEYS[4]) < expireAt - now then
				redis.call('PEXPIRE', KEYS[4], expireAt - now)
				redis.call('PEXPIRE', KEYS[5], expireAt - now)
			end
		end
	end
	local function unmirror_legacy(member)
		if KEYS[4] ~= nil and KEYS[5] ~= nil then
			redis.call('ZREM', KEYS[4], 'v2:' .. member)
			redis.call('HDEL', KEYS[5], 'v2:' .. member)
		end
	end
	local function touch_index(ttl)
		for i = 1, 2 do
			if redis.call('PTTL', KEYS[i]) < ttl then
				redis.call('PEXPIRE', KEYS[i], ttl)
			end
		end
	end
`

// apiKeyQuotaUsedLedgerKey generates the Redis key holding an API key's settled
// quota_used high-water mark (see apiKeyQuotaUsedLedgerKeyPrefix).
func apiKeyQuotaUsedLedgerKey(apiKeyID int64) string {
	return fmt.Sprintf("%s%d", apiKeyQuotaUsedLedgerKeyPrefix, apiKeyID)
}

// billingSubKey generates the Redis key for subscription cache.
func billingSubKey(userID, groupID int64) string {
	return fmt.Sprintf("%s%d:%d", billingSubKeyPrefix, userID, groupID)
}

// billingSubGenerationKey generates the Redis key for a subscription cache generation
// counter (bumped on every usage mutation; used to discard stale回源 snapshots).
func billingSubGenerationKey(userID, groupID int64) string {
	return fmt.Sprintf("%s%d:%d", billingSubGenerationKeyPrefix, userID, groupID)
}

const (
	subFieldStatus       = "status"
	subFieldExpiresAt    = "expires_at"
	subFieldDailyUsage   = "daily_usage"
	subFieldWeeklyUsage  = "weekly_usage"
	subFieldMonthlyUsage = "monthly_usage"
	subFieldVersion      = "version"
)

// billingRateLimitKey generates the Redis key for API key rate limit cache.
func billingRateLimitKey(keyID int64) string {
	return fmt.Sprintf("%s%d", billingRateLimitKeyPrefix, keyID)
}

const (
	rateLimitFieldUsage5h  = "usage_5h"
	rateLimitFieldUsage1d  = "usage_1d"
	rateLimitFieldUsage7d  = "usage_7d"
	rateLimitFieldWindow5h = "window_5h"
	rateLimitFieldWindow1d = "window_1d"
	rateLimitFieldWindow7d = "window_7d"
)

var (
	// deductBalanceScript 递减余额缓存，并**同时递增变动代号**（KEYS[2]）。
	//
	// 代号必须在同一次脚本里递增：扣费与"作废在途快照"是同一个事实，分开做会留下
	// "扣了费但没作废"的窗口（审计 R9）。
	//
	// KEYS[1] = billing:balance:<userID>，KEYS[2] = billing:balance_gen:<userID>
	// ARGV[1] = 金额，ARGV[2] = 余额缓存 TTL（秒），ARGV[3] = 代号 TTL（秒）
	deductBalanceScript = redis.NewScript(`
		redis.call('INCR', KEYS[2])
		redis.call('EXPIRE', KEYS[2], ARGV[3])
		local current = redis.call('GET', KEYS[1])
		if current == false then
			return 0
		end
		local newVal = tonumber(current) - tonumber(ARGV[1])
		redis.call('SET', KEYS[1], newVal)
		redis.call('EXPIRE', KEYS[1], ARGV[2])
		return 1
	`)

	// publishBalanceScript 发布回源快照，但只在该快照"读到之后没有发生过余额变动"时
	// 才落地：代号不一致说明读取方手里的值可能早于最近一次扣费/加钱，发布它会让预检
	// 依据偏高的余额放行（审计 R9）。被拦下的写回不创建任何键 —— 下一次读取会回到
	// DB 取真值，自愈。
	//
	// KEYS[1] = billing:balance:<userID>，KEYS[2] = billing:balance_gen:<userID>
	// ARGV[1] = 余额，ARGV[2] = 余额缓存 TTL（毫秒），ARGV[3] = 读取方取到的代号
	publishBalanceScript = redis.NewScript(`
		local gen = redis.call('GET', KEYS[2])
		if gen == false then
			gen = '0'
		end
		if gen ~= ARGV[3] then
			return 0
		end
		redis.call('SET', KEYS[1], ARGV[1], 'PX', ARGV[2])
		return 1
	`)

	// invalidateBalanceScript 失效余额缓存，并同时递增变动代号。
	//
	// 加钱（充值/兑换/返利/管理员调整）走这里：既删掉旧值，也让所有在途的旧快照作废，
	// 避免"充值后又被扣费前的旧快照写回"。
	//
	// KEYS[1] = billing:balance:<userID>，KEYS[2] = billing:balance_gen:<userID>
	// ARGV[1] = 代号 TTL（秒）
	invalidateBalanceScript = redis.NewScript(`
		redis.call('INCR', KEYS[2])
		redis.call('EXPIRE', KEYS[2], ARGV[1])
		redis.call('DEL', KEYS[1])
		return 1
	`)

	// 在途预留账本（每个 scope 三类键，同一 hash tag 以兼容 Redis Cluster）：
	//   - KEYS[1] billing:reserved:{scope}          ZSET  member=requestID  score=过期时刻(ms)
	//   - KEYS[2] billing:reserved_amt:{scope}      HASH  field=requestID   value=金额(USD)
	//   - KEYS[3] billing:resv_item:{scope}:<rid>   STRING 本笔凭据（金额 + 自身 TTL；供 DB 兜底导出）
	//
	// 聚合总额**每次都由存活成员重新求和**，过期成员先被惰性清理。旧实现用一个标量
	// INCRBYFLOAT 累加，凭据过期（结算丢失 / 进程崩溃 / 归还失败）后它的金额永远留在
	// 标量里；而每笔被接受的预留都会刷新标量的 TTL，只要用户持续有流量，泄漏就永不
	// 过期 —— 泄漏累计到可花余额后用户被持续 403，且无法自愈。按成员过期求和后，
	// 泄漏最多存活一个 TTL。

	// reserveBalanceScript 非原子路径（仅在缓存不支持 TryReserve 时使用）：登记本笔后返回聚合。
	// ARGV[1]=now_ms ARGV[2]=金额 ARGV[3]=TTL(ms) ARGV[4]=requestID
	reserveBalanceScript = redis.NewScript(reservationPruneSumLua + `
		local sum = prune_and_sum()
		local expireAt = now + tonumber(ARGV[3])
		local existing = redis.call('HGET', KEYS[2], ARGV[4])
		if existing == false then
			redis.call('HSET', KEYS[2], ARGV[4], ARGV[2])
			sum = sum + tonumber(ARGV[2])
		end
		redis.call('ZADD', KEYS[1], expireAt, ARGV[4])
		redis.call('SET', KEYS[3], existing or ARGV[2], 'PX', ARGV[3])
		mirror_legacy(ARGV[4], existing or ARGV[2], expireAt)
		touch_index(tonumber(ARGV[3]))
		return string.format('%.17g', sum)
	`)

	// tryReserveBalanceScript 把“清理过期 + 求和 + 上限判定 + 登记”合并成一次原子操作。
	// 被拒绝的请求不写任何键。同一 requestID 重复预留只续期，不重复累加。
	// 浮点统一以 %.17g 写回/返回（审计 R4）。
	// ARGV[1]=now_ms ARGV[2]=金额 ARGV[3]=聚合上限 ARGV[4]=TTL(ms) ARGV[5]=requestID
	tryReserveBalanceScript = redis.NewScript(reservationPruneSumLua + `
		local sum = prune_and_sum()
		local ttl = tonumber(ARGV[4])
		local expireAt = now + ttl
		local existing = redis.call('HGET', KEYS[2], ARGV[5])
		if existing ~= false then
			redis.call('ZADD', KEYS[1], expireAt, ARGV[5])
			redis.call('SET', KEYS[3], existing, 'PX', ttl)
			mirror_legacy(ARGV[5], existing, expireAt)
			touch_index(ttl)
			return {'1', string.format('%.17g', sum)}
		end
		local newVal = sum + tonumber(ARGV[2])
		if newVal > tonumber(ARGV[3]) + 1e-12 then
			return {'0', string.format('%.17g', sum)}
		end
		redis.call('ZADD', KEYS[1], expireAt, ARGV[5])
		redis.call('HSET', KEYS[2], ARGV[5], ARGV[2])
		redis.call('SET', KEYS[3], ARGV[2], 'PX', ttl)
		mirror_legacy(ARGV[5], ARGV[2], expireAt)
		touch_index(ttl)
		return {'1', string.format('%.17g', newVal)}
	`)

	// releaseBalanceScript 原子地归还**本请求**那一笔：成员不存在或已过期时返回 'EXPIRED'，
	// 绝不触碰别人的预留。返回剩余聚合（字符串）或 'EXPIRED'。
	// ARGV[1]=now_ms ARGV[2]=requestID
	releaseBalanceScript = redis.NewScript(reservationPruneSumLua + `
		local score = redis.call('ZSCORE', KEYS[1], ARGV[2])
		local alive = score ~= false and tonumber(score) > now
		redis.call('ZREM', KEYS[1], ARGV[2])
		redis.call('HDEL', KEYS[2], ARGV[2])
		redis.call('DEL', KEYS[3])
		unmirror_legacy(ARGV[2])
		if not alive then
			prune_and_sum()
			return 'EXPIRED'
		end
		return string.format('%.17g', prune_and_sum())
	`)

	// renewBalanceScript 续期仍存活的本笔预留（长请求心跳）。已过期成员不得被复活。
	// ARGV[1]=now_ms ARGV[2]=TTL(ms) ARGV[3]=requestID
	renewBalanceScript = redis.NewScript(reservationPruneSumLua + `
		local score = redis.call('ZSCORE', KEYS[1], ARGV[3])
		if score == false or tonumber(score) <= now then
			prune_and_sum()
			return '0'
		end
		local ttl = tonumber(ARGV[2])
		redis.call('ZADD', KEYS[1], 'XX', now + ttl, ARGV[3])
		local amount = redis.call('HGET', KEYS[2], ARGV[3])
		if amount ~= false then
			redis.call('SET', KEYS[3], amount, 'PX', ttl)
			mirror_legacy(ARGV[3], amount, now + ttl)
		end
		touch_index(ttl)
		return '1'
	`)

	// peekBalanceScript 返回存活预留的聚合总额（顺带清理过期成员），供运维/测试观测用，
	// 不改变任何 TTL。ARGV[1]=now_ms
	peekBalanceScript = redis.NewScript(reservationPruneSumLua + `
		return string.format('%.17g', prune_and_sum())
	`)

	updateSubUsageScript = redis.NewScript(`
		local exists = redis.call('EXISTS', KEYS[1])
		if exists == 0 then
			return 0
		end
		local cost = tonumber(ARGV[1])
		redis.call('HINCRBYFLOAT', KEYS[1], 'daily_usage', cost)
		redis.call('HINCRBYFLOAT', KEYS[1], 'weekly_usage', cost)
		redis.call('HINCRBYFLOAT', KEYS[1], 'monthly_usage', cost)
		redis.call('EXPIRE', KEYS[1], ARGV[2])
		redis.call('INCR', KEYS[2])
		redis.call('EXPIRE', KEYS[2], ARGV[3])
		return 1
	`)

	// invalidateSubCacheScript 失效订阅缓存，并同时递增变动代号。
	//
	// 必须递增代号的原因与余额缓存（审计 R9）相同：删除之后，一个在删除之前发起 DB
	// 读取、尚未落盘的旧快照仍可能被发布回缓存；如果那份快照的 usage 偏小，
	// 预检会在整个 TTL 内按偏小的用量放行。
	//
	// KEYS[1] = billing:sub:<userID>:<groupID>，KEYS[2] = billing:sub_gen:<userID>:<groupID>
	// ARGV[1] = 代号 TTL（秒）
	invalidateSubCacheScript = redis.NewScript(`
		redis.call('INCR', KEYS[2])
		redis.call('EXPIRE', KEYS[2], ARGV[1])
		redis.call('DEL', KEYS[1])
		return 1
	`)

	// publishSubCacheScript 是订阅缓存回填的**条件写回**（按"变动代号"，与余额缓存的
	// publishBalanceScript 同构）：只有代号仍等于读取方在 DB 读取之前取到的值时，
	// 才允许把整条快照（含 usage 绝对值）发布进缓存。
	//
	// 为什么必须有这道闸：订阅缓存是"未命中回源 + 异步写回"。读取方在 T0 从 DB 读到
	// usage=5 并在 T2 才落盘；若 T1 有结算同步把缓存累加为 7（或 T1 时缓存键还不存在、
	// 那次累加被守卫跳过 —— 此时增量只在 DB），T2 的旧快照就会把 usage=5 **覆盖**回去，
	// 预检随即按偏小的用量放行：订阅限额被重复消耗。
	//
	// 代号（KEYS[2]）在每次用量变动时由 updateSubUsageScript / 失效路径递增，
	// 因此"读取期间发生过变动"必然表现为代号不等，这份旧快照必须丢弃；
	// 下一次读取回源 DB 真值自愈。
	//
	// 返回值：1 = 已发布，0 = 期间有变动，快照被丢弃（不是错误）。
	//
	// KEYS[1] = billing:sub:<userID>:<groupID>，KEYS[2] = billing:sub_gen:<userID>:<groupID>
	// ARGV[1] = TTL（毫秒），ARGV[2] = 读取方取到的代号，ARGV[3] = 代号 TTL（秒），
	// 其余为快照字段值
	publishSubCacheScript = redis.NewScript(`
		local gen = redis.call('GET', KEYS[2])
		if gen == false then
			gen = '0'
		end
		if gen ~= ARGV[2] then
			return 0
		end
		redis.call('HSET', KEYS[1],
			'status', ARGV[4],
			'expires_at', ARGV[5],
			'daily_usage', ARGV[6],
			'weekly_usage', ARGV[7],
			'monthly_usage', ARGV[8],
			'version', ARGV[9])
		redis.call('PEXPIRE', KEYS[1], ARGV[1])
		redis.call('EXPIRE', KEYS[2], ARGV[3])
		return 1
	`)

	// setAPIKeyQuotaUsedLedgerScript 以**单调高水位**方式发布 API Key 已用额度：
	// 只有新值大于存量时才写入，因此并发/乱序的结算不会把账本写小（写小 = 放行超额）。
	// 值缺失时按 0 处理（键不存在或已过期）。
	//
	// KEYS[1] = billing:apikey_quota_used:<keyID>
	// ARGV[1] = 结算后的 quota_used（DB RETURNING 结果），ARGV[2] = TTL（毫秒）
	setAPIKeyQuotaUsedLedgerScript = redis.NewScript(`
		local current = redis.call('GET', KEYS[1])
		if current ~= false and tonumber(current) >= tonumber(ARGV[1]) then
			return 0
		end
		redis.call('SET', KEYS[1], ARGV[1], 'PX', ARGV[2])
		return 1
	`)

	// updateRateLimitUsageScript atomically increments all three rate limit usage counters
	// with window expiration checking. If a window has expired, its usage is reset to cost
	// (instead of accumulated) and the window timestamp is updated, matching the DB-side
	// IncrementRateLimitUsage semantics.
	//
	// ARGV: [1]=cost, [2]=ttl_seconds, [3]=now_unix, [4]=window_5h_seconds, [5]=window_1d_seconds, [6]=window_7d_seconds
	updateRateLimitUsageScript = redis.NewScript(`
		local exists = redis.call('EXISTS', KEYS[1])
		if exists == 0 then
			return 0
		end
		local cost = tonumber(ARGV[1])
		local now = tonumber(ARGV[3])
		local win5h = tonumber(ARGV[4])
		local win1d = tonumber(ARGV[5])
		local win7d = tonumber(ARGV[6])

		-- Helper: check if window is expired and update usage + window accordingly
		-- Returns nothing, modifies the hash in-place.
		local function update_window(usage_field, window_field, window_duration)
			local w = tonumber(redis.call('HGET', KEYS[1], window_field) or 0)
			if w == 0 or (now - w) >= window_duration then
				-- Window expired or never started: reset usage to cost, start new window
				redis.call('HSET', KEYS[1], usage_field, tostring(cost))
				redis.call('HSET', KEYS[1], window_field, tostring(now))
			else
				-- Window still valid: accumulate
				redis.call('HINCRBYFLOAT', KEYS[1], usage_field, cost)
			end
		end

		update_window('usage_5h', 'window_5h', win5h)
		update_window('usage_1d', 'window_1d', win1d)
		update_window('usage_7d', 'window_7d', win7d)
		redis.call('EXPIRE', KEYS[1], ARGV[2])
		return 1
	`)
)

type billingCache struct {
	rdb *redis.Client
	// db 是**可选**的在途预留 DB 兜底句柄（nil = 不启用）。
	//
	// Redis 不可用时，准入会退化为 DB 事务（见 dbReservationStore）：钱包 scope 锁
	// users 行，其它 scope 用 advisory transaction lock；语义与 Redis 侧一致。
	// 正式装配中 Redis 与 DB 都失败会返回 sentinel 并由服务层 fail-closed（503）。
	// 只有未装配 DB 的降级/测试装配才保留旧的 fail-open 行为。
	db *sql.DB

	// Shared DB-fallback window probe cache. The window is persisted in
	// billing_reservation_fallback_state so every instance switches ledgers
	// together; this local cache avoids one SELECT per reservation.
	fallbackProbeMu    sync.Mutex
	fallbackProbeAt    time.Time
	fallbackProbeUntil time.Time
	// fallbackObservedActive 记录本实例上一次观测到窗口处于生效状态；窗口结束
	// （生效 → 失效）时据此把 DB 账本上仍存活的预留导回 Redis（见 exitReservationFallback）。
	fallbackObservedActive bool
	// fallbackActivation 合并同一实例上并发触发的切换（导入 + 抬升窗口只做一次）。
	fallbackActivation singleflight.Group
}

func NewBillingCache(rdb *redis.Client) service.BillingCache {
	return &billingCache{rdb: rdb}
}

// NewBillingCacheWithDB 构造带 DB 预留兜底的计费缓存。
//
// 与 NewBillingCache 的区别只有一个：Redis 侧的预留操作失败时，不再直接放弃
// （让上层 fail-open），而是改用 DB 预留账本做同样语义的原子准入：
// 钱包 scope 使用 billing_balance_reservations，其它 scope 使用
// billing_scope_reservations。生产装配用这个构造函数；测试与降级装配可以继续用
// NewBillingCache。
func NewBillingCacheWithDB(rdb *redis.Client, db *sql.DB) service.BillingCache {
	return &billingCache{rdb: rdb, db: db}
}

func reservationOperationContext(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithTimeout(ctx, timeout)
}

func reservationProbeContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return reservationDetachedContext(ctx, billingReservationFallbackProbeTimeout)
}

func reservationDetachedContext(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithTimeout(context.WithoutCancel(ctx), timeout)
}

// reservationFallbackWindowActive reports whether every instance must use the
// database reservation ledger. The probe result is cached briefly so a healthy
// Redis path does not add one database query per request.
func (c *billingCache) reservationFallbackWindowActive(ctx context.Context) bool {
	if c == nil || c.db == nil {
		return false
	}
	now := time.Now()
	c.fallbackProbeMu.Lock()
	if !c.fallbackProbeAt.IsZero() && now.Sub(c.fallbackProbeAt) < billingReservationFallbackProbeInterval {
		until := c.fallbackProbeUntil
		c.fallbackProbeMu.Unlock()
		return until.After(now)
	}
	c.fallbackProbeMu.Unlock()

	store := newDBReservationStore(c.db)
	if store == nil {
		return false
	}
	probeCtx, cancel := reservationProbeContext(ctx)
	defer cancel()
	until, err := store.fallbackWindowUntil(probeCtx)
	if err != nil {
		service.RecordBillingReservationFallbackProbeError()
		log.Printf("Warning: probe billing reservation fallback window failed: %v", err)
		// 失败同样缓存一个探测周期：DB 变慢时不能让每个请求都再探一次（放大 DB 压力）。
		// 沿用上一次已知的窗口结论。
		c.fallbackProbeMu.Lock()
		c.fallbackProbeAt = now
		until = c.fallbackProbeUntil
		c.fallbackProbeMu.Unlock()
		return until.After(now)
	}
	active := until.After(now)
	c.fallbackProbeMu.Lock()
	c.fallbackProbeAt = now
	c.fallbackProbeUntil = until
	exiting := c.fallbackObservedActive && !active
	c.fallbackObservedActive = active
	c.fallbackProbeMu.Unlock()
	if exiting {
		c.exitReservationFallback(ctx, store)
	}
	return active
}

// reservationBackendFailure 判断一次 Redis 预留调用的错误是否代表"Redis 后端不可用"。
//
// 调用方自身的 context 被取消/超时（客户端断开、请求超时）不是后端故障：go-redis 在发
// 命令前就会检查 ctx。若把它当成故障，任何一个断开的客户端都会把整个集群切到 DB 串行
// 准入（审计 H1）。
func reservationBackendFailure(ctx context.Context, err error) bool {
	if err == nil || errors.Is(err, redis.Nil) {
		return false
	}
	if ctx != nil && ctx.Err() != nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	return true
}

func (c *billingCache) activateReservationFallback(ctx context.Context, ttl time.Duration) {
	if c == nil || c.db == nil {
		return
	}
	// 同一实例上并发的 Redis 故障只做一次"导入 + 抬升窗口"：否则故障瞬间每个请求都会
	// 各自 SCAN 一遍 Redis 并批量写 DB，把故障放大成 DB 过载。
	_, _, _ = c.fallbackActivation.Do("activate", func() (any, error) {
		c.activateReservationFallbackOnce(ctx, ttl)
		return nil, nil
	})
}

func (c *billingCache) activateReservationFallbackOnce(ctx context.Context, ttl time.Duration) {
	store := newDBReservationStore(c.db)
	if store == nil {
		return
	}
	// 先把 Redis 账本上仍存活的在途预留搬进 DB 账本，再抬升共享窗口。
	// 顺序很重要：窗口一旦生效，其它实例会在 1s 内切到 DB 账本；若此时存量预留
	// 还没导入，它们就不在 DB 账本的求和里 —— 同一份额度被两本账各算一遍，
	// 等于把"一个预算"重复放行（PR#19 引入 DB 兜底账本时遗留的双账本缺口）。
	c.importRedisReservationsIntoDatabase(ctx, store)
	activateCtx, cancel := reservationDetachedContext(ctx, billingReservationDBOperationTimeout)
	defer cancel()
	if err := store.activateFallbackWindow(activateCtx, ttl); err != nil {
		service.RecordBillingReservationFallbackActivationError()
		log.Printf("Warning: activate billing reservation fallback window failed: %v", err)
	} else {
		service.RecordBillingReservationFallbackActivated()
	}
	now := time.Now()
	c.fallbackProbeMu.Lock()
	c.fallbackProbeAt = now
	c.fallbackProbeUntil = now.Add(normalizedBillingReservationTTL(ttl))
	c.fallbackObservedActive = true
	c.fallbackProbeMu.Unlock()
}

// exitReservationFallback 在窗口结束时把 DB 账本上仍存活的预留导回 Redis。
//
// 窗口长度固定为"最后一次真实 Redis 故障 + 预留 TTL"，不再随 DB 预留/续期顺延——
// 顺延会让窗口在持续流量下永不结束，整个集群永久停留在 DB 串行准入（审计 H1）。
// 代价是窗口结束时可能还有 DB 预留在途：不把它们导回 Redis，Redis 账本就会少算
// 这部分额度（与进入窗口时的导入对称）。导入按 requestID 幂等，多实例并发执行无害；
// 导回的预留随后由 Redis 侧的心跳/归还正常维护，DB 行随自身 TTL 过期。
func (c *billingCache) exitReservationFallback(ctx context.Context, store *dbReservationStore) {
	if c == nil || store == nil || c.rdb == nil {
		return
	}
	_, _, _ = c.fallbackActivation.Do("exit", func() (any, error) {
		importCtx, cancel := reservationDetachedContext(ctx, billingReservationDBOperationTimeout)
		defer cancel()
		receipts, err := store.liveReservationReceipts(importCtx)
		if err != nil {
			service.RecordBillingReservationFallbackImportError()
			log.Printf("Warning: list DB reservations on fallback exit failed (they stay uncounted in redis until they expire): %v", err)
			return nil, nil
		}
		now := time.Now()
		for _, receipt := range receipts {
			ttl := receipt.ExpiresAt.Sub(now)
			if ttl <= 0 {
				continue
			}
			if _, runErr := reserveBalanceScript.Run(importCtx, c.rdb,
				billingReservationKeys(receipt.Scope, receipt.RequestID),
				now.UnixMilli(), strconv.FormatFloat(receipt.Amount, 'f', -1, 64), ttl.Milliseconds(), receipt.RequestID).Result(); runErr != nil && !errors.Is(runErr, redis.Nil) {
				service.RecordBillingReservationFallbackImportError()
				log.Printf("Warning: import DB reservation into redis on fallback exit failed for scope %s: %v", receipt.Scope, runErr)
				return nil, nil
			}
		}
		return nil, nil
	})
}

// releaseDBReservationIfAny 在 Redis 报告本笔凭据不存在时，顺手删除可能残留在 DB 账本里
// 的同一笔（Redis 故障期登记、窗口结束后才归还）。只做幂等删除，不触发窗口切换。
func (c *billingCache) releaseDBReservationIfAny(ctx context.Context, scope, requestID string) error {
	switch outcome, dbErr := c.tryReleaseDBReservation(ctx, scope, requestID); outcome {
	case dbReservationHandled:
		return nil
	case dbReservationAbsent:
		return service.ErrBillingReservationExpired
	default:
		if dbErr != nil {
			log.Printf("Warning: db reservation release failed for scope %s: %v", scope, dbErr)
			return dbErr
		}
		return service.ErrBillingReservationExpired
	}
}

// importRedisReservationsIntoDatabase 把 Redis 侧存活的预留凭据导入 DB 兜底账本。
//
// 失败语义：导入失败**不阻塞**窗口抬升（可用性优先 —— 否则 Redis 故障会直接变成
// 全站 503），但必须计数 + ALERT：此时 DB 账本缺少存量预留，护栏会高估可用额度，
// 最坏把"一个预算"重复放行。该残余风险有界（存量预留随 TTL 自然过期，窗口长度
// 也覆盖这段时间），且只会在"Redis 读写同时故障"时出现。
func (c *billingCache) importRedisReservationsIntoDatabase(ctx context.Context, store *dbReservationStore) {
	if c == nil || store == nil {
		return
	}
	scanCtx, cancel := reservationDetachedContext(ctx, billingReservationDBOperationTimeout)
	defer cancel()
	receipts, err := c.scanRedisReservationReceipts(scanCtx)
	if err != nil {
		service.RecordBillingReservationFallbackImportError()
		log.Printf("Warning: scan redis reservations before DB fallback failed (existing in-flight reservations stay uncounted): %v", err)
		return
	}
	if len(receipts) == 0 {
		service.RecordBillingReservationFallbackImported(0)
		return
	}
	imported, err := store.importRedisReservationReceipts(scanCtx, receipts)
	if err != nil {
		service.RecordBillingReservationFallbackImportError()
		log.Printf("Warning: import %d redis reservations into DB fallback ledger failed (imported=%d): %v", len(receipts), imported, err)
		return
	}
	service.RecordBillingReservationFallbackImported(imported)
}

func (c *billingCache) tryReserveDatabase(ctx context.Context, scope, requestID string, amount, maxTotal float64, ttl time.Duration) (float64, bool, error) {
	store := newDBReservationStore(c.db)
	if store == nil {
		return 0, false, fmt.Errorf("%w: database reservation store unavailable", service.ErrReservationBackendsUnavailable)
	}
	dbCtx, cancel := reservationOperationContext(ctx, billingReservationDBOperationTimeout)
	defer cancel()
	total, accepted, err := store.tryReserve(dbCtx, scope, requestID, amount, maxTotal, ttl)
	if err != nil {
		service.RecordBillingReservationDBFallbackError()
		log.Printf("Warning: db reservation fallback failed for scope %s: %v", scope, err)
		return 0, false, fmt.Errorf("%w: %v", service.ErrReservationBackendsUnavailable, err)
	}
	service.RecordBillingReservationDBFallback()
	return total, accepted, nil
}

func (c *billingCache) GetUserBalance(ctx context.Context, userID int64) (float64, error) {
	key := billingBalanceKey(userID)
	val, err := c.rdb.Get(ctx, key).Result()
	if err != nil {
		return 0, err
	}
	return strconv.ParseFloat(val, 64)
}

func (c *billingCache) SetUserBalance(ctx context.Context, userID int64, balance float64) error {
	key := billingBalanceKey(userID)
	return c.rdb.Set(ctx, key, balance, jitteredTTL()).Err()
}

// BalanceGeneration 返回余额缓存当前的"变动代号"，供回源路径在**发起 DB 读取之前**取一份
// 快照，再交给 SetUserBalanceIfGeneration 做条件发布（审计 R9）。
//
// 读不到（键不存在）时返回 "0"，与发布脚本对缺失代号的归一化一致。
func (c *billingCache) BalanceGeneration(ctx context.Context, userID int64) (string, error) {
	val, err := c.rdb.Get(ctx, billingBalanceGenerationKey(userID)).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return "0", nil
		}
		return "", err
	}
	return val, nil
}

// SetUserBalanceIfGeneration 只在该用户余额的变动代号仍等于 generation 时发布快照。
//
// 代号不一致意味着"读取方发起 DB 读取之后发生过扣费/加钱"，此时快照可能偏高（也可能是
// 加钱后的偏低值），一律不发布：下一次读取回源真值，自愈。返回 published=false 不代表
// 错误，调用方不需要处理。
func (c *billingCache) SetUserBalanceIfGeneration(ctx context.Context, userID int64, balance float64, generation string) (bool, error) {
	if generation == "" {
		// 没拿到代号（取代号失败/降级装配）：退回无条件发布，与修复前一致。
		return true, c.SetUserBalance(ctx, userID, balance)
	}
	reply, err := publishBalanceScript.Run(ctx, c.rdb,
		[]string{billingBalanceKey(userID), billingBalanceGenerationKey(userID)},
		balance, jitteredTTL().Milliseconds(), generation).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return false, err
	}
	return fmt.Sprint(reply) == "1", nil
}

func (c *billingCache) DeductUserBalance(ctx context.Context, userID int64, amount float64) error {
	key := billingBalanceKey(userID)
	_, err := deductBalanceScript.Run(ctx, c.rdb,
		[]string{key, billingBalanceGenerationKey(userID)},
		amount, int(jitteredTTL().Seconds()), balanceGenerationTTLSeconds).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		log.Printf("Warning: deduct balance cache failed for user %d: %v", userID, err)
		return err
	}
	return nil
}

func (c *billingCache) InvalidateUserBalance(ctx context.Context, userID int64) error {
	_, err := invalidateBalanceScript.Run(ctx, c.rdb,
		[]string{billingBalanceKey(userID), billingBalanceGenerationKey(userID)},
		balanceGenerationTTLSeconds).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return err
	}
	return nil
}

// MarkUserBalanceExhausted 记录"该用户的钱包已经没有可花余额（已到 reserve 底线）"。
//
// 计费是后付费：一旦结算才发现余额不足，上游成本已经发生。因此转发前的预检必须
// 尽可能 fail-closed。但预检读的是余额缓存，而余额缓存采用"未命中回源 + 异步写回"，
// 扣费后的 InvalidateUserBalance(DEL) 可能被扣费前读到的旧余额写回"复活"，
// 使预检在缓存 TTL（5 分钟）内持续放行注定扣费失败的请求。
//
// 这个标记由结算路径在"扣到底线 / 扣费被拒"时写入，回源路径永不写它，因此不受该竞态影响。
func (c *billingCache) MarkUserBalanceExhausted(ctx context.Context, userID int64) error {
	key := billingBalanceExhaustedKey(userID)
	return c.rdb.Set(ctx, key, 1, balanceExhaustedMarkerTTL).Err()
}

// ClearUserBalanceExhausted 清除"钱包已耗尽"标记。任何让余额增加的路径（充值、兑换、
// 返利、管理员调整）都必须调用它，否则刚充值的用户在标记 TTL 内仍会被预检拦截。
func (c *billingCache) ClearUserBalanceExhausted(ctx context.Context, userID int64) error {
	key := billingBalanceExhaustedKey(userID)
	return c.rdb.Del(ctx, key).Err()
}

// IsUserBalanceExhausted 查询"钱包已耗尽"标记是否存在。
func (c *billingCache) IsUserBalanceExhausted(ctx context.Context, userID int64) (bool, error) {
	key := billingBalanceExhaustedKey(userID)
	n, err := c.rdb.Exists(ctx, key).Result()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// ReserveUserBalance 原子地为**某一次请求**建立在途预留：写入该请求的预留凭据，
// 并把它累加进该 scope 的在途预留总额，返回累加后的总额（USD）。
//
// 调用方（BillingCacheService 的预检）用它的返回值判断"这份可花额度减去在途预留后
// 是否仍然充足"：余额模式看 `balance - reserved >= reserve`，订阅模式看
// `usage + reserved <= limit`。只要成立，即使全部在途请求都以最坏费用结算，
// 也不会击穿额度，不存在收不满的坏账。
//
// scope 标识"同一份可被并发放大的额度"（余额用 userID，订阅用 user+group）；
// requestID 必须是**每笔请求唯一且不含分隔符 ':'** 的标识（服务层用去掉连字符的
// UUID），它决定归还时能否精确匹配到本笔凭据。
func (c *billingCache) ReserveUserBalance(ctx context.Context, scope string, requestID string, amount float64, ttl time.Duration) (float64, error) {
	if amount < 0 {
		return 0, fmt.Errorf("reserve amount must be nonnegative, got %v", amount)
	}
	if scope == "" {
		return 0, fmt.Errorf("reserve scope must not be empty")
	}
	if requestID == "" {
		return 0, fmt.Errorf("reserve requestID must not be empty")
	}
	if c.reservationFallbackWindowActive(ctx) {
		total, _, err := c.tryReserveDatabase(ctx, scope, requestID, amount, math.MaxFloat64, ttl)
		return total, err
	}
	reply, err := reserveBalanceScript.Run(ctx, c.rdb,
		billingReservationKeys(scope, requestID),
		time.Now().UnixMilli(), strconv.FormatFloat(amount, 'f', -1, 64), reservationTTLMillis(ttl), requestID).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		if !reservationBackendFailure(ctx, err) {
			return 0, err
		}
		service.RecordBillingReservationRedisFailure()
		if c.db != nil {
			c.activateReservationFallback(ctx, ttl)
			total, _, dbErr := c.tryReserveDatabase(ctx, scope, requestID, amount, math.MaxFloat64, ttl)
			return total, dbErr
		}
		return 0, err
	}
	return parseReservedBalanceReply(reply)
}

// TryReserveUserBalance 仅在聚合预留不超过 maxTotal 时原子建立本笔凭据。
func (c *billingCache) TryReserveUserBalance(ctx context.Context, scope string, requestID string, amount, maxTotal float64, ttl time.Duration) (float64, bool, error) {
	if amount < 0 || maxTotal < 0 || math.IsNaN(amount) || math.IsNaN(maxTotal) || math.IsInf(amount, 0) || math.IsInf(maxTotal, 0) {
		return 0, false, fmt.Errorf("reserve amount and limit must be nonnegative, got amount=%v limit=%v", amount, maxTotal)
	}
	if scope == "" || requestID == "" {
		return 0, false, fmt.Errorf("reserve scope and requestID must not be empty")
	}
	if c.reservationFallbackWindowActive(ctx) {
		return c.tryReserveDatabase(ctx, scope, requestID, amount, maxTotal, ttl)
	}
	reply, err := tryReserveBalanceScript.Run(ctx, c.rdb,
		billingReservationKeys(scope, requestID),
		time.Now().UnixMilli(), strconv.FormatFloat(amount, 'f', -1, 64), strconv.FormatFloat(maxTotal, 'f', -1, 64),
		reservationTTLMillis(ttl), requestID).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		if !reservationBackendFailure(ctx, err) {
			return 0, false, err
		}
		service.RecordBillingReservationRedisFailure()
		if c.db != nil {
			c.activateReservationFallback(ctx, ttl)
			return c.tryReserveDatabase(ctx, scope, requestID, amount, maxTotal, ttl)
		}
		return 0, false, err
	}
	parts, ok := reply.([]any)
	if !ok || len(parts) != 2 {
		return 0, false, fmt.Errorf("unexpected atomic reservation reply %T: %v", reply, reply)
	}
	// 只认两种明确答复：'1' = 已接受并落凭据，'0' = 因超过上限被拒。
	// 其它值（历史版本的 '-1'、脚本被替换、Lua 返回值异常）**按拒绝处理**而不是返回
	// error：返回 error 会让调用方走 "预留后端不可用 → fail-open" 的分支，使本笔在
	// 完全没有预留的情况下被放行。这里返回 accepted=false，交由调用方的 guard 再判
	// 一次（fail-closed，方向保守）。审计 R3。
	replyCode := fmt.Sprint(parts[0])
	if replyCode != "0" && replyCode != "1" {
		log.Printf("Warning: unexpected atomic reservation reply code %q for scope %s: %v", replyCode, scope, reply)
		return 0, false, nil
	}
	accepted := replyCode == "1"
	total, err := parseReservedBalanceReply(parts[1])
	if err != nil && accepted {
		// 凭据已经落下：必须按"已接受"返回，让服务层绑定并在收尾归还；
		// 返回 error 会让这笔凭据无人认领、白占额度到 TTL（审计 L2）。
		log.Printf("Warning: unparsable reservation total for scope %s (receipt kept bound): %v", scope, err)
		return amount, true, nil
	}
	return total, accepted, err
}

// dbReservationOutcome 描述 DB 兜底对某笔预留的处理结果。
type dbReservationOutcome int

const (
	// dbReservationNotApplicable: no database fallback handle is configured.
	dbReservationNotApplicable dbReservationOutcome = iota
	// dbReservationHandled：本笔确实记在 DB 兜底里，且操作已完成。
	dbReservationHandled
	// dbReservationAbsent：兜底可用，但本笔不在其中（已过期/已归还）。
	// 与 Redis 侧"凭据不存在"是同一个语义，因此归还/续期都按"已回收"处理。
	dbReservationAbsent
)

// tryReleaseDBReservation 尝试在 DB 兜底里归还本笔预留。
func (c *billingCache) tryReleaseDBReservation(ctx context.Context, scope, requestID string) (dbReservationOutcome, error) {
	store := newDBReservationStore(c.db)
	if store == nil {
		return dbReservationNotApplicable, nil
	}
	err := store.release(ctx, scope, requestID)
	if err == nil {
		return dbReservationHandled, nil
	}
	if errors.Is(err, service.ErrBillingReservationExpired) {
		return dbReservationAbsent, nil
	}
	return dbReservationNotApplicable, err
}

// tryRenewDBReservation 尝试在 DB 兜底里续期本笔预留，语义同 tryReleaseDBReservation。
func (c *billingCache) tryRenewDBReservation(ctx context.Context, scope, requestID string, ttl time.Duration) (dbReservationOutcome, error) {
	store := newDBReservationStore(c.db)
	if store == nil {
		return dbReservationNotApplicable, nil
	}
	err := store.renew(ctx, scope, requestID, ttl)
	if err == nil {
		return dbReservationHandled, nil
	}
	if errors.Is(err, service.ErrBillingReservationExpired) {
		return dbReservationAbsent, nil
	}
	return dbReservationNotApplicable, err
}

// ReleaseUserBalanceReservation 原子地归还"本请求"占用的在途预留。
//
// 与旧实现的关键差别：归还的前提是**本请求的凭据仍然存在**。凭据已随 TTL 过期时
// 整体 no-op 并返回 ErrBillingReservationExpired —— 少了这道闸，晚到的归还会把
// 同一 scope 后来那笔请求的预留一起扣掉，护栏被错误解除。
//
// DB 兜底带来一个额外分支：Redis 报告"凭据不存在"或 Redis 本身不可用时，本笔可能当初
// 是记在 billing_balance_reservations 里的（Redis 故障期准入），因此还要去 DB 删一次。
// 这个二次操作是**幂等**的，且只在 Redis 报错/报缺失时才发生，正常路径零额外开销。
func (c *billingCache) ReleaseUserBalanceReservation(ctx context.Context, scope string, requestID string, amount float64, ttl time.Duration) error {
	if scope == "" || requestID == "" {
		return nil
	}
	if amount <= 0 {
		return nil
	}
	if c.reservationFallbackWindowActive(ctx) {
		switch outcome, dbErr := c.tryReleaseDBReservation(ctx, scope, requestID); outcome {
		case dbReservationHandled:
			return nil
		case dbReservationAbsent:
			return service.ErrBillingReservationExpired
		default:
			if dbErr != nil {
				return dbErr
			}
			return service.ErrBillingReservationExpired
		}
	}
	reply, err := releaseBalanceScript.Run(ctx, c.rdb,
		billingReservationKeys(scope, requestID),
		time.Now().UnixMilli(), requestID).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		// Redis 不可用：本笔可能当初记在 DB 兜底里，二次尝试。兜底不可用/不支持该 scope 时
		// 维持既有行为（返回 Redis 错误）。只有真实后端故障才切换账本（审计 H1）。
		if c.db != nil && reservationBackendFailure(ctx, err) {
			c.activateReservationFallback(ctx, ttl)
		}
		switch outcome, dbErr := c.tryReleaseDBReservation(ctx, scope, requestID); outcome {
		case dbReservationHandled:
			return nil
		case dbReservationAbsent:
			return service.ErrBillingReservationExpired
		default:
			if dbErr != nil {
				log.Printf("Warning: db reservation release failed for scope %s: %v", scope, dbErr)
				return dbErr
			}
			log.Printf("Warning: release balance reservation failed for scope %s: %v", scope, err)
			return err
		}
	}
	if reservationReplyIsExpired(reply) {
		// Redis 侧没有本笔凭据（TTL 自愈已回收）——但兜底可用时它也可能记在 DB 里
		// （故障期登记、窗口结束后才归还）。Redis 本身是健康的：只做幂等删除，
		// **不**切换账本，否则正常的"心跳与归还竞态 / 凭据过期"会把集群切到 DB（审计 H1）。
		if c.db == nil {
			return service.ErrBillingReservationExpired
		}
		return c.releaseDBReservationIfAny(ctx, scope, requestID)
	}
	return nil
}

// RenewUserBalanceReservation 为**长请求**续期本笔预留（心跳）。凭据已过期/已归还时返回
// ErrBillingReservationExpired，调用方据此停止心跳。DB 兜底分支与归还同理。
func (c *billingCache) RenewUserBalanceReservation(ctx context.Context, scope string, requestID string, ttl time.Duration) error {
	if scope == "" || requestID == "" {
		return nil
	}
	if c.reservationFallbackWindowActive(ctx) {
		switch outcome, dbErr := c.tryRenewDBReservation(ctx, scope, requestID, ttl); outcome {
		case dbReservationHandled:
			return nil
		case dbReservationAbsent:
			return service.ErrBillingReservationExpired
		default:
			if dbErr != nil {
				return dbErr
			}
			return service.ErrBillingReservationExpired
		}
	}
	reply, err := renewBalanceScript.Run(ctx, c.rdb,
		billingReservationKeys(scope, requestID),
		time.Now().UnixMilli(), reservationTTLMillis(ttl), requestID).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		if c.db != nil && reservationBackendFailure(ctx, err) {
			c.activateReservationFallback(ctx, ttl)
		}
		switch outcome, dbErr := c.tryRenewDBReservation(ctx, scope, requestID, ttl); outcome {
		case dbReservationHandled:
			return nil
		case dbReservationAbsent:
			return service.ErrBillingReservationExpired
		default:
			if dbErr != nil {
				log.Printf("Warning: db reservation renew failed for scope %s: %v", scope, dbErr)
				return dbErr
			}
			log.Printf("Warning: renew balance reservation failed for scope %s: %v", scope, err)
			return err
		}
	}
	if reservationReplyIsMissing(reply) {
		// Redis 健康、只是没有本笔（已过期，或记在 DB 账本里）：续期 DB 行即可，
		// 不切换账本（审计 H1）。
		if c.db == nil {
			return service.ErrBillingReservationExpired
		}
		switch outcome, dbErr := c.tryRenewDBReservation(ctx, scope, requestID, ttl); outcome {
		case dbReservationHandled:
			return nil
		case dbReservationAbsent:
			return service.ErrBillingReservationExpired
		default:
			if dbErr != nil {
				log.Printf("Warning: db reservation renew failed for scope %s: %v", scope, dbErr)
				return dbErr
			}
			return service.ErrBillingReservationExpired
		}
	}
	return nil
}

// reservationReplyIsExpired 判断归还脚本的回复是否为"本笔凭据已随 TTL 回收"。
func reservationReplyIsExpired(reply any) bool {
	text, ok := reply.(string)
	return ok && text == reservationReleaseExpiredReply
}

// reservationReplyIsMissing 判断续期脚本的回复是否为"本笔凭据不存在"。
func reservationReplyIsMissing(reply any) bool {
	text, ok := reply.(string)
	return ok && text == "0"
}

// ReservedUserBalanceTotal 只读返回该 scope 当前的在途预留总额（USD），供运维观测使用。
// 不存在的键返回 0，不创建键、不改变 TTL。
func (c *billingCache) ReservedUserBalanceTotal(ctx context.Context, scope string) (float64, error) {
	if scope == "" {
		return 0, nil
	}
	reply, err := peekBalanceScript.Run(ctx, c.rdb, billingReservationKeys(scope, "peek"), time.Now().UnixMilli()).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return 0, err
	}
	return parseReservedBalanceReply(reply)
}

// reservationReleaseExpiredReply 是归还脚本在"本请求凭据已过期"时的返回值。
// 归一成一个常量，避免脚本字面量与 Go 侧判断漂移。
const reservationReleaseExpiredReply = "EXPIRED"

// reservationTTLMillis 把预留 TTL 规范化为毫秒；缺省/非法值退化为余额缓存 TTL。
func reservationTTLMillis(ttl time.Duration) int64 {
	millis := ttl.Milliseconds()
	if millis <= 0 {
		millis = billingCacheTTL.Milliseconds()
	}
	return millis
}

// parseReservedBalanceReply 解析预留脚本的十进制文本返回值，兼容 RESP2/RESP3 以及
// 集群客户端可能出现的多种返回形态（数字必须以文本往返，避免被整数截断）。
func parseReservedBalanceReply(reply any) (float64, error) {
	switch v := reply.(type) {
	case nil:
		return 0, nil
	case string:
		return strconv.ParseFloat(v, 64)
	case []byte:
		return strconv.ParseFloat(string(v), 64)
	case float64:
		return v, nil
	case int64:
		return float64(v), nil
	default:
		return 0, fmt.Errorf("unexpected balance reservation reply type %T", reply)
	}
}

func (c *billingCache) GetSubscriptionCache(ctx context.Context, userID, groupID int64) (*service.SubscriptionCacheData, error) {
	key := billingSubKey(userID, groupID)
	result, err := c.rdb.HGetAll(ctx, key).Result()
	if err != nil {
		return nil, err
	}
	if len(result) == 0 {
		return nil, redis.Nil
	}
	return c.parseSubscriptionCache(result)
}

func (c *billingCache) parseSubscriptionCache(data map[string]string) (*service.SubscriptionCacheData, error) {
	result := &service.SubscriptionCacheData{}

	result.Status = data[subFieldStatus]
	if result.Status == "" {
		return nil, errors.New("invalid cache: missing status")
	}

	if expiresStr, ok := data[subFieldExpiresAt]; ok {
		expiresAt, err := strconv.ParseInt(expiresStr, 10, 64)
		if err == nil {
			result.ExpiresAt = time.Unix(expiresAt, 0)
		}
	}

	if dailyStr, ok := data[subFieldDailyUsage]; ok {
		result.DailyUsage, _ = strconv.ParseFloat(dailyStr, 64)
	}

	if weeklyStr, ok := data[subFieldWeeklyUsage]; ok {
		result.WeeklyUsage, _ = strconv.ParseFloat(weeklyStr, 64)
	}

	if monthlyStr, ok := data[subFieldMonthlyUsage]; ok {
		result.MonthlyUsage, _ = strconv.ParseFloat(monthlyStr, 64)
	}

	if versionStr, ok := data[subFieldVersion]; ok {
		result.Version, _ = strconv.ParseInt(versionStr, 10, 64)
	}

	return result, nil
}

func (c *billingCache) SetSubscriptionCache(ctx context.Context, userID, groupID int64, data *service.SubscriptionCacheData) error {
	if data == nil {
		return nil
	}

	key := billingSubKey(userID, groupID)
	fields := map[string]any{
		subFieldStatus:       data.Status,
		subFieldExpiresAt:    data.ExpiresAt.Unix(),
		subFieldDailyUsage:   data.DailyUsage,
		subFieldWeeklyUsage:  data.WeeklyUsage,
		subFieldMonthlyUsage: data.MonthlyUsage,
		subFieldVersion:      data.Version,
	}
	pipe := c.rdb.Pipeline()
	pipe.HSet(ctx, key, fields)
	pipe.Expire(ctx, key, jitteredTTL())
	_, err := pipe.Exec(ctx)
	return err
}

// SubscriptionGeneration 返回订阅缓存当前的"变动代号"，供回源路径在**发起 DB 读取之前**
// 取一份快照，再交给 SetSubscriptionCacheIfGeneration 做条件发布（与余额缓存同构）。
//
// 读不到（键不存在）时返回 "0"，与发布脚本对缺失代号的归一化一致。
func (c *billingCache) SubscriptionGeneration(ctx context.Context, userID, groupID int64) (string, error) {
	val, err := c.rdb.Get(ctx, billingSubGenerationKey(userID, groupID)).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return "0", nil
		}
		return "", err
	}
	return val, nil
}

// SetSubscriptionCacheIfGeneration 只在订阅缓存的变动代号仍等于 generation 时发布快照。
//
// 代号不一致意味着"读取方发起 DB 读取之后发生过用量变动"（结算同步累加 / 缓存失效），
// 此时快照可能偏高（窗口重置等）也可能偏低（少了新结算的用量），一律不发布：
// 下一次读取回源真值，自愈。返回 published=false 不代表错误，调用方不需要处理。
//
// generation 为空串（未取到代号 / 轻量 stub 装配）时退回无条件发布，与修复前一致。
func (c *billingCache) SetSubscriptionCacheIfGeneration(ctx context.Context, userID, groupID int64, data *service.SubscriptionCacheData, generation string) (bool, error) {
	if generation == "" {
		return true, c.SetSubscriptionCache(ctx, userID, groupID, data)
	}
	if data == nil {
		return false, nil
	}
	reply, err := publishSubCacheScript.Run(ctx, c.rdb,
		[]string{billingSubKey(userID, groupID), billingSubGenerationKey(userID, groupID)},
		jitteredTTL().Milliseconds(),
		generation,
		subscriptionGenerationTTLSeconds,
		data.Status,
		data.ExpiresAt.Unix(),
		data.DailyUsage,
		data.WeeklyUsage,
		data.MonthlyUsage,
		data.Version,
	).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return false, err
	}
	return fmt.Sprint(reply) == "1", nil
}

func (c *billingCache) UpdateSubscriptionUsage(ctx context.Context, userID, groupID int64, cost float64) error {
	_, err := c.UpdateSubscriptionUsageApplied(ctx, userID, groupID, cost)
	return err
}

// UpdateSubscriptionUsageApplied 与 UpdateSubscriptionUsage 语义相同，但把脚本返回值
// 暴露出来：applied=false 表示缓存键不存在，这次累加被**跳过**（静默、无错误）。
//
// 跳过时的增量只存在于 DB（如果结算已经写了 DB）；调用方必须失效缓存，
// 否则预检会在 TTL 内继续用偏小的快照放行 —— 而这正是"预留已归还、usage 仍旧"的
// 超发窗口。见 SyncSubscriptionUsageAfterSettlement。
func (c *billingCache) UpdateSubscriptionUsageApplied(ctx context.Context, userID, groupID int64, cost float64) (bool, error) {
	key := billingSubKey(userID, groupID)
	// 同步递增变动代号（KEYS[2]）：累加与"作废在途快照"是同一个事实，分开做会留下
	// "加了用量但没作废"的窗口，迟到的旧快照就能把累加覆盖回偏小值。
	reply, err := updateSubUsageScript.Run(ctx, c.rdb,
		[]string{key, billingSubGenerationKey(userID, groupID)},
		cost,
		int(jitteredTTL().Seconds()),
		subscriptionGenerationTTLSeconds,
	).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		log.Printf("Warning: update subscription usage cache failed for user %d group %d: %v", userID, groupID, err)
		return false, err
	}
	return fmt.Sprint(reply) == "1", nil
}

func (c *billingCache) InvalidateSubscriptionCache(ctx context.Context, userID, groupID int64) error {
	key := billingSubKey(userID, groupID)
	// 失效同样递增代号：删除后仍可能在途的旧快照必须作废，否则它会把刚删掉的
	// 偏小值（或旧值）重新发布回来（与余额缓存的 invalidateBalanceScript 同理）。
	_, err := invalidateSubCacheScript.Run(ctx, c.rdb,
		[]string{key, billingSubGenerationKey(userID, groupID)},
		subscriptionGenerationTTLSeconds).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return err
	}
	return nil
}

func (c *billingCache) PublishSubscriptionCacheInvalidation(ctx context.Context, cacheKey string) error {
	return c.rdb.Publish(ctx, subCacheInvalidateChannel, cacheKey).Err()
}

func (c *billingCache) SubscribeSubscriptionCacheInvalidation(ctx context.Context, handler func(cacheKey string)) error {
	pubsub := c.rdb.Subscribe(ctx, subCacheInvalidateChannel)

	_, err := pubsub.Receive(ctx)
	if err != nil {
		_ = pubsub.Close()
		return fmt.Errorf("subscribe to subscription cache invalidation: %w", err)
	}

	go func() {
		defer func() {
			if err := pubsub.Close(); err != nil {
				log.Printf("Warning: failed to close subscription cache invalidation pubsub: %v", err)
			}
		}()

		ch := pubsub.Channel()
		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-ch:
				if !ok {
					return
				}
				if msg != nil {
					handler(msg.Payload)
				}
			}
		}
	}()

	return nil
}

func (c *billingCache) GetAPIKeyRateLimit(ctx context.Context, keyID int64) (*service.APIKeyRateLimitCacheData, error) {
	key := billingRateLimitKey(keyID)
	result, err := c.rdb.HGetAll(ctx, key).Result()
	if err != nil {
		return nil, err
	}
	if len(result) == 0 {
		return nil, redis.Nil
	}
	data := &service.APIKeyRateLimitCacheData{}
	if v, ok := result[rateLimitFieldUsage5h]; ok {
		data.Usage5h, _ = strconv.ParseFloat(v, 64)
	}
	if v, ok := result[rateLimitFieldUsage1d]; ok {
		data.Usage1d, _ = strconv.ParseFloat(v, 64)
	}
	if v, ok := result[rateLimitFieldUsage7d]; ok {
		data.Usage7d, _ = strconv.ParseFloat(v, 64)
	}
	if v, ok := result[rateLimitFieldWindow5h]; ok {
		data.Window5h, _ = strconv.ParseInt(v, 10, 64)
	}
	if v, ok := result[rateLimitFieldWindow1d]; ok {
		data.Window1d, _ = strconv.ParseInt(v, 10, 64)
	}
	if v, ok := result[rateLimitFieldWindow7d]; ok {
		data.Window7d, _ = strconv.ParseInt(v, 10, 64)
	}
	return data, nil
}

func (c *billingCache) SetAPIKeyRateLimit(ctx context.Context, keyID int64, data *service.APIKeyRateLimitCacheData) error {
	if data == nil {
		return nil
	}
	key := billingRateLimitKey(keyID)
	fields := map[string]any{
		rateLimitFieldUsage5h:  data.Usage5h,
		rateLimitFieldUsage1d:  data.Usage1d,
		rateLimitFieldUsage7d:  data.Usage7d,
		rateLimitFieldWindow5h: data.Window5h,
		rateLimitFieldWindow1d: data.Window1d,
		rateLimitFieldWindow7d: data.Window7d,
	}
	pipe := c.rdb.Pipeline()
	pipe.HSet(ctx, key, fields)
	pipe.Expire(ctx, key, rateLimitCacheTTL)
	_, err := pipe.Exec(ctx)
	return err
}

func (c *billingCache) UpdateAPIKeyRateLimitUsage(ctx context.Context, keyID int64, cost float64) error {
	_, err := c.UpdateAPIKeyRateLimitUsageApplied(ctx, keyID, cost)
	return err
}

// UpdateAPIKeyRateLimitUsageApplied 与 UpdateAPIKeyRateLimitUsage 语义相同，但暴露
// 脚本返回值：applied=false 表示限流缓存键不存在、这次累加被跳过（静默、无错误）。
// 调用方必须失效缓存让下一次预检回源 DB 真值，否则限流窗口会按偏小的用量放行。
func (c *billingCache) UpdateAPIKeyRateLimitUsageApplied(ctx context.Context, keyID int64, cost float64) (bool, error) {
	key := billingRateLimitKey(keyID)
	now := time.Now().Unix()
	reply, err := updateRateLimitUsageScript.Run(ctx, c.rdb, []string{key},
		cost,
		int(rateLimitCacheTTL.Seconds()),
		now,
		int(rateLimitWindow5h.Seconds()),
		int(rateLimitWindow1d.Seconds()),
		int(rateLimitWindow7d.Seconds()),
	).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		log.Printf("Warning: update rate limit usage cache failed for api key %d: %v", keyID, err)
		return false, err
	}
	return fmt.Sprint(reply) == "1", nil
}

func (c *billingCache) InvalidateAPIKeyRateLimit(ctx context.Context, keyID int64) error {
	key := billingRateLimitKey(keyID)
	return c.rdb.Del(ctx, key).Err()
}

// ============================================
// user × platform quota 缓存
// ============================================

// userPlatformQuotaCacheKey 构造 Redis key
func userPlatformQuotaCacheKey(userID int64, platform string) string {
	return fmt.Sprintf("billing:user_platform_quota:%d:%s", userID, platform)
}

// parseUserPlatformQuotaHash 将 Redis HGETALL 返回的 map[string]string 反序列化为
// *service.UserPlatformQuotaCacheEntry。空 map（key 不存在）返回 nil。
// GetUserPlatformQuotaCache 和 BatchGetUserPlatformQuotaCache 共用此函数，确保解析逻辑一致。
func parseUserPlatformQuotaHash(m map[string]string) *service.UserPlatformQuotaCacheEntry {
	if len(m) == 0 {
		return nil
	}
	parseFloat := func(s string) float64 {
		if s == "" {
			return 0
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			log.Printf("billing_cache: corrupt quota usage field %q (using 0): %v", s, err)
			return 0
		}
		return f
	}
	parseFloatPtr := func(s string) *float64 {
		if s == "" {
			return nil
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return nil
		}
		return &f
	}
	parseTimePtr := func(s string) *time.Time {
		if s == "" {
			return nil
		}
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return nil
		}
		t := time.Unix(n, 0).UTC()
		return &t
	}
	parseInt64 := func(s string) int64 {
		n, _ := strconv.ParseInt(s, 10, 64)
		return n
	}
	return &service.UserPlatformQuotaCacheEntry{
		DailyUsageUSD:      parseFloat(m["daily_usage"]),
		WeeklyUsageUSD:     parseFloat(m["weekly_usage"]),
		MonthlyUsageUSD:    parseFloat(m["monthly_usage"]),
		Version:            parseInt64(m["version"]),
		SchemaVersion:      parseInt64(m["schema_version"]),
		DailyLimitUSD:      parseFloatPtr(m["daily_limit"]),
		WeeklyLimitUSD:     parseFloatPtr(m["weekly_limit"]),
		MonthlyLimitUSD:    parseFloatPtr(m["monthly_limit"]),
		DailyWindowStart:   parseTimePtr(m["daily_window_start"]),
		WeeklyWindowStart:  parseTimePtr(m["weekly_window_start"]),
		MonthlyWindowStart: parseTimePtr(m["monthly_window_start"]),
	}
}

func (c *billingCache) GetUserPlatformQuotaCache(ctx context.Context, userID int64, platform string) (*service.UserPlatformQuotaCacheEntry, bool, error) {
	key := userPlatformQuotaCacheKey(userID, platform)
	m, err := c.rdb.HGetAll(ctx, key).Result()
	if err != nil {
		return nil, false, err
	}
	entry := parseUserPlatformQuotaHash(m)
	if entry == nil {
		// 空 map → key 不存在 → MISS
		return nil, false, nil
	}
	return entry, true, nil
}

func (c *billingCache) SetUserPlatformQuotaCache(ctx context.Context, userID int64, platform string, entry *service.UserPlatformQuotaCacheEntry, ttl time.Duration) error {
	if entry == nil {
		return nil
	}
	key := userPlatformQuotaCacheKey(userID, platform)
	pipe := c.rdb.TxPipeline()

	// 浮点可空字段：nil → 空字符串（读取时 parseFloatPtr 返回 nil，表示无限额）
	fmtFloatPtr := func(p *float64) string {
		if p == nil {
			return ""
		}
		return strconv.FormatFloat(*p, 'f', -1, 64)
	}
	// time.Time 可空字段：nil → 空字符串；有值 → unix 秒
	fmtTimePtr := func(p *time.Time) string {
		if p == nil {
			return ""
		}
		return strconv.FormatInt(p.Unix(), 10)
	}

	pipe.HSet(ctx, key,
		"daily_usage", entry.DailyUsageUSD,
		"weekly_usage", entry.WeeklyUsageUSD,
		"monthly_usage", entry.MonthlyUsageUSD,
		"version", entry.Version,
		"schema_version", entry.SchemaVersion,
		"daily_limit", fmtFloatPtr(entry.DailyLimitUSD),
		"weekly_limit", fmtFloatPtr(entry.WeeklyLimitUSD),
		"monthly_limit", fmtFloatPtr(entry.MonthlyLimitUSD),
		"daily_window_start", fmtTimePtr(entry.DailyWindowStart),
		"weekly_window_start", fmtTimePtr(entry.WeeklyWindowStart),
		"monthly_window_start", fmtTimePtr(entry.MonthlyWindowStart),
	)
	pipe.Expire(ctx, key, ttl)
	_, err := pipe.Exec(ctx)
	return err
}

func (c *billingCache) DeleteUserPlatformQuotaCache(ctx context.Context, userID int64, platform string) error {
	return c.rdb.Del(ctx, userPlatformQuotaCacheKey(userID, platform)).Err()
}

// updateUserPlatformQuotaUsageScript 缓存累加：EXISTS + schema_version 双重守卫。
// 旧版 entry（schema_version != ARGV[3]，包括缺字段的 0 值）不参与累加，由上层走 DB fallback 后
// SetCache 重建为新版 entry —— 若此处仍累加，上层覆盖时会丢失这部分增量，导致 Redis usage 比真实偏小。
// key 不存在同样跳过（由下次 SetCache 重建）。
// KEYS[1] = hash key
// KEYS[2] = 脏集 key（dirty set）
// ARGV[1] = cost (string float)
// ARGV[2] = ttl seconds
// ARGV[3] = expected schema_version (Go 侧 UserPlatformQuotaCacheSchemaV1)
// ARGV[4] = dirty set member（空串则不 SADD）
// ARGV[5] = 脏集兜底 TTL 秒
const updateUserPlatformQuotaUsageScript = `
if redis.call("EXISTS", KEYS[1]) == 0 then
    return 0
end
local ver = redis.call("HGET", KEYS[1], "schema_version")
if ver == false or tonumber(ver) ~= tonumber(ARGV[3]) then
    return 0
end
redis.call("HINCRBYFLOAT", KEYS[1], "daily_usage", ARGV[1])
redis.call("HINCRBYFLOAT", KEYS[1], "weekly_usage", ARGV[1])
redis.call("HINCRBYFLOAT", KEYS[1], "monthly_usage", ARGV[1])
redis.call("HINCRBY", KEYS[1], "version", 1)
redis.call("EXPIRE", KEYS[1], ARGV[2])
if ARGV[4] ~= "" then
    redis.call("SADD", KEYS[2], ARGV[4])
    redis.call("EXPIRE", KEYS[2], ARGV[5])
end
return 1
`

// userPlatformQuotaDirtySetKey 返回脏集（dirty set）的 Redis key。
// 使用与 userPlatformQuotaCacheKey 相同的前缀 "billing:"。
func userPlatformQuotaDirtySetKey() string { return "billing:" + "upq:dirty" }

// userPlatformQuotaDirtyTTLSeconds 脏集兜底 TTL（秒）：初始 SADD（Lua）与 Readd 共用，
// 确保 flusher 长期停摆时脏集最终过期；正常运行因持续 SADD 不断续期。
const userPlatformQuotaDirtyTTLSeconds = 86400

// userPlatformQuotaDirtyMember 构造脏集成员字符串 "userID:platform"。
func userPlatformQuotaDirtyMember(userID int64, platform string) string {
	return strconv.FormatInt(userID, 10) + ":" + platform
}

func (c *billingCache) IncrUserPlatformQuotaUsageCache(ctx context.Context, userID int64, platform string, cost float64, ttl time.Duration, markDirty bool) error {
	_, err := c.IncrUserPlatformQuotaUsageCacheApplied(ctx, userID, platform, cost, ttl, markDirty)
	return err
}

// IncrUserPlatformQuotaUsageCacheApplied 与 IncrUserPlatformQuotaUsageCache 语义相同，
// 但把 Lua 脚本的返回值暴露出来：applied=false 表示**这次累加被跳过**
// （key 不存在，或 entry 是旧 schema —— 脚本两道守卫都会静默 return 0）。
//
// 为什么必须暴露：跳过时增量只在内存里算过一次就消失了。此前调用方只看 error，
// 于是"缓存键缺失导致的静默丢增量"永远不可观测，也永远没有 DB 兜底 ——
// usage 会永久偏小（尤其是 flusher 模式下，缺的这笔连 DB 都不会写）。
// 服务层据此走 DB 同步兜底 + 失效缓存，见 IncrementUserPlatformQuotaUsage。
func (c *billingCache) IncrUserPlatformQuotaUsageCacheApplied(ctx context.Context, userID int64, platform string, cost float64, ttl time.Duration, markDirty bool) (bool, error) {
	member := ""
	if markDirty {
		member = userPlatformQuotaDirtyMember(userID, platform)
	}
	reply, err := c.rdb.Eval(ctx, updateUserPlatformQuotaUsageScript,
		[]string{userPlatformQuotaCacheKey(userID, platform), userPlatformQuotaDirtySetKey()},
		strconv.FormatFloat(cost, 'f', -1, 64),
		int(ttl.Seconds()),
		service.UserPlatformQuotaCacheSchemaV1,
		member,
		userPlatformQuotaDirtyTTLSeconds,
	).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return false, nil
		}
		return false, err
	}
	return fmt.Sprint(reply) == "1", nil
}

// parseUserPlatformQuotaDirtyMember 将脏集成员字符串 "userID:platform" 解析为
// service.UserPlatformQuotaKey。解析失败返回 ok=false。
func parseUserPlatformQuotaDirtyMember(m string) (service.UserPlatformQuotaKey, bool) {
	parts := strings.SplitN(m, ":", 2)
	if len(parts) != 2 {
		return service.UserPlatformQuotaKey{}, false
	}
	uid, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return service.UserPlatformQuotaKey{}, false
	}
	return service.UserPlatformQuotaKey{UserID: uid, Platform: parts[1]}, true
}

// PopDirtyUserPlatformQuotaKeys 从脏集随机弹出最多 n 个 key。
// 脏集为空时返回 (nil, nil)。
func (c *billingCache) PopDirtyUserPlatformQuotaKeys(ctx context.Context, n int) ([]service.UserPlatformQuotaKey, error) {
	members, err := c.rdb.SPopN(ctx, userPlatformQuotaDirtySetKey(), int64(n)).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		return nil, err
	}
	keys := make([]service.UserPlatformQuotaKey, 0, len(members))
	for _, m := range members {
		k, ok := parseUserPlatformQuotaDirtyMember(m)
		if !ok {
			log.Printf("billing_cache: skipping invalid dirty member %q", m)
			continue
		}
		keys = append(keys, k)
	}
	return keys, nil
}

// ReaddDirtyUserPlatformQuotaKeys 将 keys 重新加入脏集（flush 失败时回填）。
// 通过 pipeline 同时执行 SAdd + Expire，确保 Readd 后脏集具有兜底 TTL。
// 空切片时直接返回 nil。
func (c *billingCache) ReaddDirtyUserPlatformQuotaKeys(ctx context.Context, keys []service.UserPlatformQuotaKey) error {
	if len(keys) == 0 {
		return nil
	}
	dirtyKey := userPlatformQuotaDirtySetKey()
	members := make([]any, len(keys))
	for i, k := range keys {
		members[i] = userPlatformQuotaDirtyMember(k.UserID, k.Platform)
	}
	pipe := c.rdb.Pipeline()
	pipe.SAdd(ctx, dirtyKey, members...)
	pipe.Expire(ctx, dirtyKey, userPlatformQuotaDirtyTTLSeconds*time.Second)
	_, err := pipe.Exec(ctx)
	return err
}

// BatchGetUserPlatformQuotaCache 通过 Pipeline 批量 HGETALL 获取多个 user×platform 的
// quota cache。返回切片与 keys 顺序、长度对齐；MISS 或解析失败位置返回 nil。
func (c *billingCache) BatchGetUserPlatformQuotaCache(ctx context.Context, keys []service.UserPlatformQuotaKey) ([]*service.UserPlatformQuotaCacheEntry, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	pipe := c.rdb.Pipeline()
	cmds := make([]*redis.MapStringStringCmd, len(keys))
	for i, k := range keys {
		cmds[i] = pipe.HGetAll(ctx, userPlatformQuotaCacheKey(k.UserID, k.Platform))
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, err
	}
	results := make([]*service.UserPlatformQuotaCacheEntry, len(keys))
	for i, cmd := range cmds {
		m, err := cmd.Result()
		if err != nil {
			if !errors.Is(err, redis.Nil) {
				log.Printf("billing_cache: BatchGet HGETALL cmd[%d] failed: %v (skip, self-heal)", i, err)
			}
			// 单个命令失败 → 对应位置 nil，继续
			continue
		}
		results[i] = parseUserPlatformQuotaHash(m)
	}
	return results, nil
}

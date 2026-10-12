//go:build unit

package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// billingGuardDelta 返回某次操作前后某个计数器的增量。
//
// 用增量而不是绝对值断言：同包内其他用例只要有可能触碰这些计数（例如预检/预留
// 用例），绝对值断言就会变脆；增量断言与执行顺序无关。
func billingGuardDelta(t *testing.T, read func() int64, act func()) int64 {
	t.Helper()
	before := read()
	act()
	return read() - before
}

func TestRecordBillingPreflightReject_SeparatesReasons(t *testing.T) {
	reasons := []BillingPreflightRejectReason{
		BillingRejectMarkerActive,
		BillingRejectBelowReserve,
		BillingRejectDBTruthBelowReserve,
		BillingRejectWorstCaseCache,
		BillingRejectWorstCaseDBTruth,
		BillingRejectReservationGuard,
	}
	for _, reason := range reasons {
		reason := reason
		delta := billingGuardDelta(t, func() int64 {
			return rejectCounterForReason(reason)
		}, func() {
			RecordBillingPreflightReject(reason)
			RecordBillingPreflightReject(reason)
		})
		require.Equal(t, int64(2), delta, "原因 %s 的计数应各自独立累加", reason)
	}

	// 未知原因不得 panic，也不得污染任何已知计数。
	total := func() int64 {
		var sum int64
		for _, r := range reasons {
			sum += rejectCounterForReason(r)
		}
		return sum
	}
	delta := billingGuardDelta(t, total, func() {
		RecordBillingPreflightReject("not-a-real-reason")
	})
	require.Equal(t, int64(0), delta, "未知原因不应计入任何已知计数")
}

// rejectCounterForReason 把原因映射回它自己的计数器，供用例做逐项断言。
func rejectCounterForReason(reason BillingPreflightRejectReason) int64 {
	stats := BillingGuardStatsSnapshot()
	switch reason {
	case BillingRejectMarkerActive:
		return stats.RejectMarkerActive
	case BillingRejectBelowReserve:
		return stats.RejectBelowReserve
	case BillingRejectDBTruthBelowReserve:
		return stats.RejectDBTruthBelowReserve
	case BillingRejectWorstCaseCache:
		return stats.RejectWorstCaseCacheOnly
	case BillingRejectWorstCaseDBTruth:
		return stats.RejectWorstCaseDBTruth
	case BillingRejectReservationGuard:
		return stats.RejectInflightReservation
	default:
		return 0
	}
}

// TestBillingGuardStats_FlagsDegradedSignals 验证"护栏失效类"计数会被识别成
// 显式的 degraded signal：这正是此前缺失的能力 —— 护栏静默降级时 ops 无从发现。
func TestBillingGuardStats_FlagsDegradedSignals(t *testing.T) {
	base := BillingGuardStatsSnapshot()

	RecordBillingReservationFailOpen()
	afterFailOpen := BillingGuardStatsSnapshot()
	require.Equal(t, base.ReservationFailOpen+1, afterFailOpen.ReservationFailOpen)
	require.Greater(t, afterFailOpen.ReservationFailOpenLastUnix, int64(0), "fail-open 必须记录最近事件时间")
	require.False(t, afterFailOpen.Healthy, "存在 fail-open 时不得报告健康")
	require.Contains(t, strings.Join(afterFailOpen.DegradedSignals, "\n"), "fail-open")

	RecordBillingRecheckSkippedNoUserRepo()
	afterDegraded := BillingGuardStatsSnapshot()
	require.Equal(t, base.RecheckSkippedNoUserRepo+1, afterDegraded.RecheckSkippedNoUserRepo)
	require.Contains(t, strings.Join(afterDegraded.DegradedSignals, "\n"), "userRepo")

	RecordBillingPrecheckDisabled()
	RecordBillingPrecheckUnavailable()
	afterPrecheck := BillingGuardStatsSnapshot()
	require.Equal(t, base.PrecheckDisabled+1, afterPrecheck.PrecheckDisabled)
	require.Equal(t, base.PrecheckUnavailable+1, afterPrecheck.PrecheckUnavailable)
	require.Contains(t, strings.Join(afterPrecheck.DegradedSignals, "\n"), "precheck_disabled")
}

// TestBillingGuardStats_WorstCaseRejectRatio 验证"预检误伤比例"这一派生判据：
// 最坏费用/预留类拒绝占比越高，说明预检越保守（可能误伤余额充足但贴底的请求）。
func TestBillingGuardStats_WorstCaseRejectRatio(t *testing.T) {
	base := BillingGuardStatsSnapshot()

	// 3 笔"真的没钱 + 1 笔护栏拒绝"
	RecordBillingPreflightReject(BillingRejectBelowReserve)
	RecordBillingPreflightReject(BillingRejectBelowReserve)
	RecordBillingPreflightReject(BillingRejectBelowReserve)
	RecordBillingPreflightReject(BillingRejectReservationGuard)

	stats := BillingGuardStatsSnapshot()
	worstCase := stats.RejectWorstCaseCacheOnly + stats.RejectWorstCaseDBTruth + stats.RejectInflightReservation
	plainReject := stats.RejectMarkerActive + stats.RejectBelowReserve + stats.RejectDBTruthBelowReserve
	require.Greater(t, worstCase, base.RejectWorstCaseCacheOnly+base.RejectWorstCaseDBTruth+base.RejectInflightReservation)
	require.Greater(t, plainReject, base.RejectBelowReserve)
	require.Greater(t, stats.WorstCaseRejectRatio, 0.0)
	require.LessOrEqual(t, stats.WorstCaseRejectRatio, 1.0)
}

// TestBillingGuardRuntimeInfo_ExposesGuardContract 验证运行参数可被 ops 读取，
// 用于判断某个斜率是配置问题还是实现问题。
func TestBillingGuardRuntimeInfo_ExposesGuardContract(t *testing.T) {
	info := BillingGuardRuntimeInfo()
	for _, key := range []string{
		"reservation_ttl_seconds",
		"reservation_heartbeat_seconds",
		"default_max_output_tokens",
		"min_inline_binary_run",
		"image_token_allowance",
		"reject_reasons",
	} {
		require.Contains(t, info, key, "运行参数应包含 %s", key)
	}
	ttl, ok := info["reservation_ttl_seconds"].(int64)
	require.True(t, ok)
	require.Equal(t, int64(billingReservationTTL/time.Second), ttl)
	heartbeat, ok := info["reservation_heartbeat_seconds"].(int64)
	require.True(t, ok)
	require.Greater(t, heartbeat, int64(0))
	require.Less(t, heartbeat, ttl, "心跳间隔必须小于 TTL")
}

// TestCheckBalanceEligibility_RecordsRejectReasons 验证预检拒绝会被按原因记账，
// 从而让"真的没钱"与"被护栏提前拦下"在 ops 侧可区分（此前两者共用同一个 403，
// 无法归因，用户投诉时无从解释）。
func TestCheckBalanceEligibility_RecordsRejectReasons(t *testing.T) {
	ctx := context.Background()

	t.Run("缓存余额低于封底", func(t *testing.T) {
		cache := &balanceEligibilityCacheStub{balance: 0.05}
		cfg := &config.Config{}
		cfg.Billing.MinimumBalanceReserve = 0.10
		svc := NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
		t.Cleanup(svc.Stop)

		delta := billingGuardDelta(t, func() int64 {
			return BillingGuardStatsSnapshot().RejectBelowReserve
		}, func() {
			require.ErrorIs(t, svc.CheckBillingEligibility(ctx, &User{ID: 1}, nil, nil, nil, ""), ErrInsufficientBalance)
		})
		require.Equal(t, int64(1), delta)
	})

	t.Run("缓存吃得下但DB真值吃不下最坏费用", func(t *testing.T) {
		cache := &balanceEligibilityCacheStub{balance: 0.50}
		userRepo := &balanceLoadUserRepoStub{balance: 0.15}
		cfg := &config.Config{}
		cfg.Billing.MinimumBalanceReserve = 0.10
		svc := NewBillingCacheService(cache, userRepo, nil, nil, nil, nil, cfg, nil)
		t.Cleanup(svc.Stop)

		var slot BillingReservationSlot
		delta := billingGuardDelta(t, func() int64 {
			return BillingGuardStatsSnapshot().RejectWorstCaseDBTruth
		}, func() {
			err := svc.CheckBillingEligibility(ctx, &User{ID: 1}, nil, nil, nil, "",
				WithMaxRequestSpend(0.30), WithBalanceReservation(&slot))
			require.ErrorIs(t, err, ErrInsufficientBalance)
		})
		require.Equal(t, int64(1), delta, "DB 真值不足最坏费用时应记为 worst_case_db_truth")
	})

	t.Run("降级装配只能按缓存判定", func(t *testing.T) {
		// 0.15 落在复核带内（reserve 0.10，band 未配 → 阈值为 2*reserve = 0.20），
		// 因此两次调用都会走到"需要复核"这一步；缺 userRepo 时只能按缓存判定并计数。
		cache := &balanceEligibilityCacheStub{balance: 0.15}
		cfg := &config.Config{}
		cfg.Billing.MinimumBalanceReserve = 0.10
		// 刻意不注入 userRepo：模拟降级装配。
		svc := NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
		t.Cleanup(svc.Stop)

		var slot BillingReservationSlot
		worstCaseDelta := billingGuardDelta(t, func() int64 {
			return BillingGuardStatsSnapshot().RejectWorstCaseCacheOnly
		}, func() {
			err := svc.CheckBillingEligibility(ctx, &User{ID: 1}, nil, nil, nil, "",
				WithMaxRequestSpend(0.30), WithBalanceReservation(&slot))
			require.ErrorIs(t, err, ErrInsufficientBalance)
		})
		require.Equal(t, int64(1), worstCaseDelta)

		// 降级装配还必须被显式计数：否则这种部署看起来一切正常，实际复核能力缺失。
		skippedDelta := billingGuardDelta(t, func() int64 {
			return BillingGuardStatsSnapshot().RecheckSkippedNoUserRepo
		}, func() {
			err := svc.CheckBillingEligibility(ctx, &User{ID: 1}, nil, nil, nil, "",
				WithMaxRequestSpend(0.01), WithBalanceReservation(&slot))
			require.NoError(t, err)
		})
		require.Equal(t, int64(1), skippedDelta, "缺 userRepo 且需要复核时必须计数")
	})
}

// TestEstimateRequestInputTokensUpperBound_InlineBinaryIsNotText 验证多模态折算修正：
// 内联 base64 图片是**二进制**，不能再按 len(body)/2 折算成文本 token
// （一张小图的 base64 可达数 MB，旧口径会得出几十万 token 的天文上界，
// 让余额充足但贴近封底的带图请求被误 403）。
func TestEstimateRequestInputTokensUpperBound_InlineBinaryIsNotText(t *testing.T) {
	// 1MB 的 data URI 图片：旧口径 → 约 52 万 token；新口径 → 文本部分 + 单块图片上限。
	inlineImage := `{"model":"gpt-4o","messages":[{"role":"user","content":[
		{"type":"image_url","image_url":{"url":"data:image/png;base64,` +
		strings.Repeat("iVBORw0KGgoAAAANSUhEUg", 40000) + `"}},
		{"type":"text","text":"describe this"}]}]}`

	upper := EstimateRequestInputTokensUpperBound([]byte(inlineImage))
	require.Greater(t, upper, 0)
	require.Less(t, upper, 20000,
		"内联 base64 图片不得被按字节折算成文本 token（旧口径会得到数十万）")
	require.GreaterOrEqual(t, upper, requestSpendImageTokenAllowance,
		"至少应包含一块图片的固定 token 折算")

	// 同长度但**不含** base64 标记的文本请求，仍必须走保守的字节折算（口径不回退）。
	textBody := `{"model":"gpt-4o","messages":[{"role":"user","content":"` + strings.Repeat("a", len(inlineImage)) + `"}]}`
	textUpper := EstimateRequestInputTokensUpperBound([]byte(textBody))
	require.Greater(t, textUpper, len(textBody)/2-1, "纯文本请求必须保持 len(body)/2 的保守折算")
}

// TestEstimateRequestInputTokensUpperBound_AnthropicBase64Block 验证 Anthropic
// 形态的图片块（"type":"base64" + "data":"<base64>"）同样不再按文本折算。
func TestEstimateRequestInputTokensUpperBound_AnthropicBase64Block(t *testing.T) {
	payload := strings.Repeat("QWxwaGE0QmV0YUdBTU1hRGF0YQ", 30000)
	body := `{"model":"claude-sonnet-4-5","max_tokens":1024,"messages":[{"role":"user","content":[
		{"type":"image","source":{"type":"base64","media_type":"image/png","data":"` + payload + `"}},
		{"type":"text","text":"what is this"}]}]}`

	upper := EstimateRequestInputTokensUpperBound([]byte(body))
	require.Less(t, upper, 20000, "Anthropic image block 的 base64 不得按文本折算")

	// 反例保护：短 base64 串（普通长标识符）不计入二进制，仍按文本折算。
	shortId := `{"model":"gpt-4o","messages":[{"role":"user","content":"` + strings.Repeat("abc", 50) + `"}]}`
	require.Greater(t, EstimateRequestInputTokensUpperBound([]byte(shortId)), 100, "短串必须仍按文本折算")
}

// TestEstimateRequestInputTokensUpperBound_Base64InTextIsDense 是生产坏账的回归测试
// （166.1.232.118 2026-09-13：20KB base64 粘在 text 里，上游按文本分词为 18907 token、
// 实际成本 $0.00835，而修复前按"图片=1600 token"折算只估 $0.0021 → 低估 4 倍，
// 产生一笔 shortfall=$0.00335208 的 write-off）。
//
// 口径：**不在多模态块内**（前面最近的 JSON 键是 text/content 等）的 base64,
// 必须按稠密费率 requestSpendDenseTokensPerByte(1 token/字节) 计,
// 绝不能享受图片固定折算。
func TestEstimateRequestInputTokensUpperBound_Base64InTextIsDense(t *testing.T) {
	blob := strings.Repeat("iVBORw0KGgoAAAANSUhEUg", 1000) // ≈22KB base64 文本
	body := []byte(`{"model":"deepseek-v4-flash","messages":[{"role":"user","content":[{"type":"text","text":"describe: data:image/png;base64,` + blob + `"}]}]}`)

	upper := EstimateRequestInputTokensUpperBound([]byte(body))
	require.GreaterOrEqual(t, upper, len(blob),
		"text 里的 base64 必须按稠密费率(≈1 token/字节)计,不得按图片 1600/块折算")
	require.LessOrEqual(t, upper, len(blob)+len(body)/2+requestSpendInputOverheadTokens+64,
		"稠密部分之外不应再有大的放大")
}

// TestEstimateRequestInputTokensUpperBound_MixedImageAndTextBase64 验证同一请求里
// 两种形态并存时各按各的口径:真实 image block 固定折算,text 里的 base64 稠密折算。
func TestEstimateRequestInputTokensUpperBound_MixedImageAndTextBase64(t *testing.T) {
	realImage := strings.Repeat("iVBORw0KGgoAAAANSUhEUg", 400)      // ≈8.8KB 真实图片块
	textBase64 := strings.Repeat("QWxwaGE0QmV0YUdBTU1hRGF0YQ", 800) // ≈20.4KB 文本里的 base64
	body := []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":[
		{"type":"image_url","image_url":{"url":"data:image/png;base64,` + realImage + `"}},
		{"type":"text","text":"data:image/png;base64,` + textBase64 + `"}]}]}`)

	upper := EstimateRequestInputTokensUpperBound([]byte(body))
	// 真实图片块 → 1600;文本 base64 → ≈1 token/字节;其余文本 → /2。
	require.GreaterOrEqual(t, upper, len(textBase64), "text 里的 base64 必须按稠密费率计")
	require.Less(t, upper, len(realImage)+len(textBase64),
		"真实图片块不得按字节折算(否则会再加 8000+ token)")
}

// TestJsonKeyBefore 验证键名回溯(多模态块判定的核心)。
//
// 注意第 5 例：旧实现沿"data URI 字符白名单"回扫，值内部出现 `:` 会把它误当 scheme
// 冒号而回溯失败（返回 "" → 误判为 text）。现在改为"反向找到值的起始引号"，能正确
// 读出 `text` —— 这与"带 mime 参数的 data URI（`;charset=utf-8;base64,`）能被正确
// 归类"是同一个修复（见 gateway_request_spend_estimate.go 的 jsonKeyBefore 注释）。
func TestJsonKeyBefore(t *testing.T) {
	cases := []struct {
		body string
		pos  int // valueStart(值的第一个字节)
		want string
	}{
		{`{"a":{"url":"data:image/png;base64,XXXX"`, len(`{"a":{"url":"`), "url"},
		{`{"s":{"type":"base64","data":"XXXX"`, len(`{"s":{"type":"base64","data":"`), "data"},
		{`{"m":{"inline_data":{"data": "XXXX"`, len(`{"m":{"inline_data":{"data": "`), "data"},
		{`{"m":{"text":"data:image/png;base64,XXXX"`, len(`{"m":{"text":"data:image/png;base64,`), "text"},
		{`{"m":{"text":"here: XXXX"`, len(`{"m":{"text":"here: `), "text"},
		// 带 mime 参数/名字参数的 data URI（旧实现遇 `=` 断掉 → 误判 dense）
		{`{"m":{"image_url":"data:image/svg+xml;charset=utf-8;base64,XXXX"`, len(`{"m":{"image_url":"data:image/svg+xml;charset=utf-8;base64,`), "image_url"},
		{`{"m":{"url":"data:image/png;name=a.png;base64,XXXX"`, len(`{"m":{"url":"data:image/png;name=a.png;base64,`), "url"},
		// 值不在 JSON 字符串里（无起始引号）→ 无法回溯
		{`nokey XXXXXXXX`, len(`nokey `), ""},
		// 超出回扫预算（H1 的 Θ(n²) 防护）→ 返回 ""（调用方按稠密处理，方向保守）
		{`{"k":"` + strings.Repeat("x", requestSpendJSONBacktrackBudget+64) + `AAAA`, len(`{"k":"`) + requestSpendJSONBacktrackBudget + 64, ""},
	}
	for _, tc := range cases {
		got := jsonKeyBefore([]byte(tc.body), tc.pos)
		require.Equal(t, tc.want, got, "body=%s pos=%d", tc.body, tc.pos)
	}
}

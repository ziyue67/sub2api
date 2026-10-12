package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// GetBillingGuardStats 返回"后付费零超发"护栏的累计指标快照。
//
// GET /api/v1/admin/ops/billing-guard
//
// 为什么需要这个端点：这条防线由多个修复叠加而成（结算封底、放行前最坏费用预检、
// 在途预留、结算任务提交语义），此前只有 write-off 一个指标、且**没有任何暴露点**，
// ops 无法回答"护栏到底有没有在跑""它是不是静默降级了"。本端点把
// service.BillingGuardStatsSnapshot() 原样暴露出来，配合前端按时间差分算斜率即可：
//
//   - degraded_signals 非空 = 护栏在某条路径上没有生效（不是拦得多，而是根本没拦）；
//   - settlement_shortfall_count 斜率 > 0 = 预检口径仍有漏网；
//   - reject_worst_case_* / reject_inflight_reservation 斜率上升 = 预检偏保守（可能误伤）；
//   - reservation_reserved 与 reservation_released 的差值趋势 = 在途预留的堆积情况。
//
// 只读取进程内原子计数，不查库、不依赖 ops 监控开关，因此可以在故障期直接 curl。
func (h *OpsHandler) GetBillingGuardStats(c *gin.Context) {
	response.Success(c, gin.H{
		"stats":   service.BillingGuardStatsSnapshot(),
		"runtime": service.BillingGuardRuntimeInfo(),
	})
}

package service

import (
	"context"
	"strings"

	pluginv1 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v1"
)

// 调度器侧的插件提名接入。
//
// 位置选择：提名发生在 selectByLoadBalance 内部、`filtered` 已完成全部硬门槛
// 过滤且并发负载已读取之后、候选池拆分与排序之前。这个位置有三个好处：
//
//   - 插件看到的是**已经合法**的候选集，它不可能提名一个被过滤掉的账号；
//   - 提名只影响 selectionOrder 的顺序，抢槽与后续所有环节仍是原生逻辑；
//   - 插件不可用时（未启用/超时/异常）整段逻辑短路，零额外开销。
//
// 关键约束：一次选号只询问插件一次。候选池（订阅池 / 常规池）是宿主内部的
// 分层策略，不能因为插件而在层间搬运账号，否则会破坏"订阅优先"语义。

// pluginSchedulingNomination 是一次选号的插件提名结果。
type pluginSchedulingNomination struct {
	accountID   int64
	affinityHit bool
	// candidates 是询问插件时使用的候选集，用于界定"合法提名范围"。
	// 提名只能落在该集合内，超出即视为插件异常并被丢弃。
	candidates map[int64]struct{}
}

// resolveSchedulingNomination 组装候选、询问插件，并校验提名范围。
//
// 返回 nil 表示本次选号不使用插件意见（未启用、候选不足、调用失败、
// 或提名非法）。调用方对它做 nil 判断即可，无需处理各类错误分支。
func (s *defaultOpenAIAccountScheduler) resolveSchedulingNomination(
	ctx context.Context,
	req OpenAIAccountScheduleRequest,
	filtered []*Account,
	loadMap map[int64]*AccountLoadInfo,
) *pluginSchedulingNomination {
	if s == nil || s.service == nil || s.service.pluginManager == nil {
		return nil
	}
	// 快速短路：没有可用的调度绑定时不做任何候选构造。
	if !s.service.pluginManager.openAIAccountSchedulingEnabled() {
		return nil
	}
	// 候选只有一个时提名没有意义（无论选谁都是它），省掉一次 RPC。
	if len(filtered) < 2 {
		return nil
	}

	candidates, ids := buildPluginSchedulingCandidates(filtered, loadMap, s.stats)
	if len(candidates) < 2 {
		return nil
	}
	requestID := schedulingRequestID(req)
	nominated, affinityHit, err := s.service.pluginManager.NominateOpenAIAccountForScheduling(
		ctx,
		requestID,
		NormalizeOpenAICompatiblePlatform(req.Platform),
		req.SessionHash,
		req.PreviousResponseID,
		req.RequestedModel,
		req.GroupID,
		candidates,
	)
	if err != nil || nominated <= 0 {
		return nil
	}
	return &pluginSchedulingNomination{
		accountID:   nominated,
		affinityHit: affinityHit,
		candidates:  ids,
	}
}

// buildPluginSchedulingCandidates 把宿主候选转换成插件契约结构。
//
// 只传决策必需字段：负载、并发、优先级、错误率、TTFT。**不传**账号名、
// 凭据、代理、分组等 —— 插件只需要判断"哪个账号更闲、更稳"，不需要知道它是谁。
// 这也是最小暴露面原则：插件即使被完全攻破，也拿不到超出它职责的信息。
func buildPluginSchedulingCandidates(
	filtered []*Account,
	loadMap map[int64]*AccountLoadInfo,
	stats *openAIAccountRuntimeStats,
) ([]*pluginv1.ScheduleCandidate, map[int64]struct{}) {
	out := make([]*pluginv1.ScheduleCandidate, 0, len(filtered))
	ids := make(map[int64]struct{}, len(filtered))
	for _, account := range filtered {
		if account == nil || account.ID <= 0 {
			continue
		}
		loadInfo := loadMap[account.ID]
		inFlight, waiting := int64(0), int64(0)
		loadRate := 0.0
		if loadInfo != nil {
			inFlight = int64(loadInfo.CurrentConcurrency)
			waiting = int64(loadInfo.WaitingCount)
			// 宿主语义是 0-100 百分比，插件侧统一用 0-1 比率便于跨账号比较。
			loadRate = float64(loadInfo.LoadRate) / 100.0
		}
		errorRate, ttft, hasTTFT := 0.0, 0.0, false
		if stats != nil {
			errorRate, ttft, hasTTFT = stats.snapshot(account.ID)
		}
		out = append(out, &pluginv1.ScheduleCandidate{
			AccountId:      account.ID,
			AccountType:    account.Type,
			Priority:       int32(account.Priority),
			InFlight:       inFlight,
			MaxConcurrency: int64(account.EffectiveLoadFactor()),
			WaitingCount:   waiting,
			LoadRate:       loadRate,
			ErrorRate:      errorRate,
			TtftMs:         ttft,
			HasTtft:        hasTTFT,
		})
		ids[account.ID] = struct{}{}
	}
	return out, ids
}

// applyNominationToPlan 把提名账号排到选择序列首位。
//
// 这是"提名"语义的核心：**只调整顺序，不改变集合**。提名账号若抢不到并发槽位，
// 宿主会沿原序列继续尝试下一个候选，行为与没有插件时完全一致，只是多了一次尝试。
// 因此插件即使提名了一个"看着闲、实际瞬间被占满"的账号，最坏结果也只是退化为
// 原生排序，不会让请求失败。
func applyNominationToPlan(plan *openAIAccountLoadPlan, nomination *pluginSchedulingNomination, filtered []*Account) {
	if plan == nil || nomination == nil || nomination.accountID <= 0 {
		return
	}
	// 提名必须落在询问时的候选范围内。这一步是纵深防御：plugin_manager
	// 已经校验过一次，这里再校验是因为 plan 的候选集可能已经过 compact
	// 分层等二次过滤，两者的合法范围不一定相同。
	if _, ok := nomination.candidates[nomination.accountID]; !ok {
		return
	}
	if !poolContainsAccount(filtered, nomination.accountID) {
		return
	}
	plan.selectionOrder = promoteAccountInOrder(plan.selectionOrder, nomination.accountID)
}

// promoteAccountInOrder 将指定账号移到序列首位，其余保持相对顺序。
func promoteAccountInOrder(order []openAIAccountCandidateScore, accountID int64) []openAIAccountCandidateScore {
	if accountID <= 0 || len(order) <= 1 {
		return order
	}
	for i := range order {
		if order[i].account == nil || order[i].account.ID != accountID {
			continue
		}
		if i == 0 {
			return order
		}
		promoted := make([]openAIAccountCandidateScore, 0, len(order))
		promoted = append(promoted, order[i])
		promoted = append(promoted, order[:i]...)
		promoted = append(promoted, order[i+1:]...)
		return promoted
	}
	return order
}

func poolContainsAccount(filtered []*Account, accountID int64) bool {
	for _, account := range filtered {
		if account != nil && account.ID == accountID {
			return true
		}
	}
	return false
}

// attachNominationMark 记录"这次选号插件提名了谁、最终又选中了谁"。
//
// 只有在提名账号确实被选中时才写入标记：如果提名账号没抢到槽、宿主落到
// 其他候选，那这次选号的真实原因就是宿主排序而非插件，把它记成"插件提名"
// 会污染 Layer 指标、让运维误判插件效果。提名失败（未命中）则记录提名本身，
// 便于观察插件"提了但没选上"的频率。
func attachNominationMark(result *AccountSelectionResult, nomination *pluginSchedulingNomination) {
	if result == nil || nomination == nil || nomination.accountID <= 0 {
		return
	}
	result.pluginScheduling = &pluginSchedulingMark{
		nominatedAccountID: nomination.accountID,
		affinityHit:        nomination.affinityHit,
		selected:           result.Account != nil && result.Account.ID == nomination.accountID,
	}
}

// schedulingRequestID 生成一个不泄露会话内容的请求标识，仅用于插件侧日志关联。
// 会话标识会被截断，避免把完整会话指纹带进插件进程。
func schedulingRequestID(req OpenAIAccountScheduleRequest) string {
	if hash := strings.TrimSpace(req.SessionHash); hash != "" {
		return "s-" + truncateIdentifier(hash)
	}
	if id := strings.TrimSpace(req.PreviousResponseID); id != "" {
		return "r-" + truncateIdentifier(id)
	}
	return ""
}

// truncateIdentifier 截断标识，长度上限 16。
func truncateIdentifier(raw string) string {
	const length = 16
	if len(raw) <= length {
		return raw
	}
	return raw[:length]
}

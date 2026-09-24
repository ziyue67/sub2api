package service

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	pluginv1 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// 调度能力（openai.account.scheduling.v1）的宿主侧接入。
//
// 设计边界 —— 这是整个特性最重要的约定：
//
//  1. **插件只提名，不决策。** 插件拿到的候选集已经过宿主全部硬门槛过滤
//     （状态、平台、分组、传输能力、隐私、模型能力、配额、利润门）。插件只能
//     从里面挑一个，不能引入宿主没给过的账号。
//  2. **提名不产生任何副作用。** 抢并发槽位（tryAcquireOpenAIAccountSlot）、
//     lane 选择与水合（finalizeOpenAISelectionResult）、粘性写入、失败重放
//     全部仍由宿主原生逻辑完成。插件提名一个抢不到槽的账号，宿主会自然落到
//     下一个候选，不会出现"因为插件而绕过并发计数"。
//  3. **失败一律降级。** 插件未实现该 RPC、进程退出、超时、返回非法值、
//     提名不在候选集内 —— 全部走 fail-open，宿主退回自身排序。绝不因为
//     调度插件异常导致整条链路无号可用。
//
// 这三条让调度能力可以在不影响可用性的前提下上线。

const (
	// pluginSchedulingTimeout 是单次提名调用的上限。选号在请求关键路径上，
	// 必须有一个远小于用户可感知延迟的硬超时：插件卡住时宿主宁可退回自身
	// 排序，也不能让每个请求都等多秒。
	pluginSchedulingTimeout = 200 * time.Millisecond

	// pluginSchedulingLayerPlugin 是写入 decision.Layer 的标记，用于在指标和
	// 日志里区分"这次选号是插件提名的"还是宿主自己决定的。
	pluginSchedulingLayerPlugin = "plugin_scheduler"
)

// schedulingRuntime 是一次提名所依附的运行时快照。
type schedulingRuntime struct {
	pluginID       int64
	runtime        *pluginRuntime
	rolloutPercent int
}

// schedulingRoute 返回当前生效的调度能力绑定。
//
// 与出站传输的 route 不同，调度路由是**从 runtimes 映射派生**的，不维护独立
// 状态：这样启用/停用/失败切换/多实例对齐等所有既有生命周期逻辑都自动生效，
// 不需要在每一处 route.Store 旁再加一遍调度路由的写入 —— 那正是最容易漏掉
// 一个分支、导致"插件停了但调度还在生效"的地方。
func (m *PluginManager) schedulingRoute() *schedulingRuntime {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.started {
		return nil
	}
	for _, runtime := range m.runtimes {
		if runtime == nil || runtime.installation == nil {
			continue
		}
		rollout := schedulingBindingRollout(runtime.installation.Bindings)
		if rollout <= 0 {
			continue
		}
		if runtime.client == nil || runtime.client.Exited() {
			continue
		}
		if runtime.draining.Load() {
			continue
		}
		return &schedulingRuntime{
			pluginID:       runtime.installation.ID,
			runtime:        runtime,
			rolloutPercent: rollout,
		}
	}
	return nil
}

// schedulingBindingRollout 读出调度能力的灰度比例。未声明该能力时返回 0
// （等于不参与调度），这是"未启用"与"启用但灰度 0"的统一表示。
func schedulingBindingRollout(bindings []PluginBinding) int {
	for _, binding := range bindings {
		if binding.Enabled && binding.Capability == PluginCapabilityOpenAIAccountScheduling {
			return binding.RolloutPercent
		}
	}
	return 0
}

// ShouldRouteOpenAIAccountScheduling 判断该账号是否命中调度插件灰度。
//
// 复用与出站传输相同的稳定分桶函数：同一账号在两个能力上的分桶结果一致，
// 运维按账号灰度时行为可预期（命中出站插件的账号通常也希望命中调度插件）。
func (m *PluginManager) ShouldRouteOpenAIAccountScheduling(account *Account) bool {
	if m == nil || account == nil || account.Platform != PlatformOpenAI || account.Type != AccountTypeOAuth {
		return false
	}
	route := m.schedulingRoute()
	if route == nil {
		return false
	}
	return int(stablePluginBucket(account.ID)) < route.rolloutPercent
}

// openAIAccountSchedulingEnabled 报告调度能力当前是否有可用绑定。
// 调度器用它做一次快速短路，避免在没有插件时产生任何额外开销。
func (m *PluginManager) openAIAccountSchedulingEnabled() bool {
	return m != nil && m.schedulingRoute() != nil
}

// NominateOpenAIAccount 询问插件对候选集的偏好。
//
// 返回 (nominatedAccountID, affinityHit, err)。err 非空或 nominatedAccountID
// 为 0 时调用方必须完全忽略插件意见，沿用宿主排序。
func (m *PluginManager) NominateOpenAIAccount(
	ctx context.Context,
	req *pluginv1.NominateAccountRequest,
	candidateIDs map[int64]struct{},
) (int64, bool, error) {
	if m == nil || req == nil || len(candidateIDs) == 0 {
		return 0, false, nil
	}
	route := m.schedulingRoute()
	if route == nil {
		return 0, false, nil
	}
	if !route.runtime.beginRequest() {
		return 0, false, errors.New("调度插件正在停止")
	}
	defer route.runtime.finishRequest()

	callCtx, cancel := context.WithTimeout(ctx, pluginSchedulingTimeout)
	defer cancel()

	response, err := route.runtime.api.NominateAccount(callCtx, req)
	if err != nil {
		// 未实现该 RPC 说明插件是旧版本（或清单声明了但运行时没实现）。
		// 这是配置不一致而非故障，用一个稳定标记便于运维定位，且只记一次
		// debug 级日志，避免在选号热路径上刷日志。
		if status.Code(err) == codes.Unimplemented {
			slog.Debug("plugin_scheduling_unimplemented", "plugin", route.runtime.installation.PluginKey)
			return 0, false, err
		}
		slog.Warn("plugin_scheduling_nominate_failed",
			"plugin", route.runtime.installation.PluginKey,
			"error", err,
		)
		return 0, false, err
	}
	if response == nil || !response.GetHandled() {
		return 0, false, nil
	}
	nominated := response.GetNominatedAccountId()
	// 安全闸门：提名必须是宿主给过的候选。插件返回集外 ID 说明它内部有 bug
	// 或被篡改，这里直接丢弃 —— 宿主绝不接受一个自己没验证过的账号。
	if _, ok := candidateIDs[nominated]; !ok {
		slog.Warn("plugin_scheduling_nomination_out_of_scope",
			"plugin", route.runtime.installation.PluginKey,
			"nominated_account_id", nominated,
		)
		return 0, false, errors.New("插件提名了候选集之外的账号")
	}
	return nominated, response.GetAffinityHit(), nil
}

// NominateOpenAIAccountForScheduling 是给调度器使用的薄封装：
// 由调度器提供候选与请求上下文，这里负责组装 RPC 请求。
//
// 注意 context 只用于超时控制，不会因为 ctx 已取消就跳过整个选号 —— 调用方
// 应当传入仍然有效的 ctx；若已取消，提名会立刻失败并退回宿主排序。
func (m *PluginManager) NominateOpenAIAccountForScheduling(
	ctx context.Context,
	requestID string,
	platform string,
	sessionHash string,
	previousResponseID string,
	requestedModel string,
	groupID *int64,
	candidates []*pluginv1.ScheduleCandidate,
) (int64, bool, error) {
	if len(candidates) == 0 {
		return 0, false, nil
	}
	ids := make(map[int64]struct{}, len(candidates))
	for _, candidate := range candidates {
		if candidate != nil && candidate.GetAccountId() > 0 {
			ids[candidate.GetAccountId()] = struct{}{}
		}
	}
	req := &pluginv1.NominateAccountRequest{
		RequestId:          requestID,
		Platform:           platform,
		SessionHash:        sessionHash,
		PreviousResponseId: previousResponseID,
		RequestedModel:     requestedModel,
		HasGroup:           groupID != nil,
		Candidates:         candidates,
	}
	if groupID != nil {
		req.GroupId = *groupID
	}
	return m.NominateOpenAIAccount(ctx, req, ids)
}

// sanitizePluginReason 把插件返回的 reason 收敛成安全的短标记，供日志使用。
func sanitizePluginReason(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "unspecified"
	}
	var builder strings.Builder
	for _, ch := range trimmed {
		switch {
		case ch >= 'a' && ch <= 'z', ch >= 'A' && ch <= 'Z', ch >= '0' && ch <= '9', ch == '_', ch == '-':
			builder.WriteRune(ch)
		default:
			builder.WriteByte('_')
		}
		if builder.Len() >= 32 {
			break
		}
	}
	return builder.String()
}

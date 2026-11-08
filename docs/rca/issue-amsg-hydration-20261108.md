# 加密代理任务的软粘连遗漏

日期：2026-11-08。基线：ziyue67/sub2api main b7e282215da0d571f1780df7d1754788b36cda65。
独立于上游版本同步 PR #157；不包含数据库迁移、计费改动、生产部署。

## 故障与证据边界

截图错误为 The encrypted content for item amsg_... could not be verified，后接 encrypted content hydration failed: Encrypted content could not be decrypted or parsed。
这说明上游拒绝加密内容，但截图没有请求 ID、实际账号切换记录及原始协议证据，不能单凭截图确定根因为跨号。
前一轮生产只读采样（最近 24 小时内最多最后 20000 行 Docker 日志）未命中该字符串，不能排除未覆盖或未记录的故障。未向生产发送故障请求、收费请求。

## 修复前复现

WithOpenAIEncryptedReasoningAffinity 只识别顶层 reasoning.encrypted_content，漏掉 agent_message.content[] 内的加密块。
完整调度测试在 APIKey/OAuth 的 slow/errors 场景共四项失败：期望 StickySessionHit=true，实际 false。
质量逃逸后负载算法仍可能重新选中同一账号，所以测试同时检查粘连决策，不能只比较最终账号 ID。
原始证据：工作区父目录 encrypted-agent-hydration-red-expanded.log。

原版报告 #8301、#7708 仅为相关背景，不证明本站同源。魔改已存在有界 SSE reasoning 恢复，不能据旧报告说本站完全没有恢复功能。

## 实际修复

1. 新增 EncryptedTaskAffinity，识别 agent_message 顶层加密字段和 content 数组中的 encrypted_content 块，以及 compaction/compaction_summary 顶层加密历史。
2. 只读取协议字段，不递归扫描工具输出、字符串中的 JSON、任意嵌套对象。不修改请求内容。
3. 在 TTFT/错误率质量逃逸及优先级延迟降权逃逸中保留这些请求的会话粘连。
4. 标志只表示软偏好，不加入 priorityLatencyOwnershipBound。并发满、禁用、限流、排除、管理员显式延迟停用继续按既有规则选择可用账号。容量再均衡不改，不新增等待时长。
5. 原 reasoning 标志及 response/guardian 所有权规则不改。混合请求保留既有 reasoning 策略。

原 reasoning 标志在部分优先级策略里会继承硬账号约束，所以不能直接给 agent_message 复用它。新增独立软标志避免意外锁号。

## 内容与计费边界

- 不删 agent 密文，不把可读头部冒充任务正文，不重写 Usage、计费类别或缓存 TTL。
- 原有安全 reasoning 恢复、一次重试上限、已发送文字/工具后不得重放保持不变。
- 同形 amsg SSE 错误的 Forward 测试确认 APIKey/OAuth 返回失败、只调用一次上游、不产生账号 failover 错误、不删除任务。这是既有行为回归，并非本次新增的错误恢复。
- 正常选择到 Forward 用例核对返回 input/output tokens 原样保留。这不是生产账本结算测试，也没有证明所有失败流扣费正确。
- OAuth 原有 store=false 规范化会移除 item ID，测试保留该既有行为，分别核对 type/author/recipient/content 完整性。尚无证据证明 ID 移除造成截图故障。
- 必须换号后，新上游仍可能无法解密历史；第三方内部换号也不由本站软粘连控制。本地没有解密密钥或可信完整明文，不能承诺恢复旧任务，不能称作所有 hydration 错误彻底修复。

## 测试矩阵

新增 encrypted_agent_hydration_investigation_test.go，以及 priority_latency_modes_test.go 的 TestEncryptedTaskAffinityPriorityLatencyPolicy。
覆盖结构识别及负例、APIKey/OAuth 正常/慢/高错误率/满载/禁用/限流/排除、完整调度转发内容和 Usage 保留、软降权与硬停用差别。

本地 PowerShell 需设置 GOOS=windows、GOARCH=amd64、GOTOOLCHAIN=auto（环境原继承 GOOS=linux）。
后端定向命令：

go test -tags=unit ./internal/service -run 'TestEncryptedAgentHydration|TestEncryptedTaskAffinityPriorityLatencyPolicy|Test.*Encrypted|TestPriorityObservation|TestPriorityLatencyModes|TestForwardEncrypted' -count=1 -timeout=10m

未部署，未进行真实收费加解密验证。

## 当前验证证据

- 定向回归通过（service，2.850s）：新加密任务回归、既有 encrypted/lineage/SSE 恢复、priority observation/latency 模式。
- 全量 Windows 单测已出现平台不兼容：目录 sync 权限、创建符号链接权限、POSIX mode 和 shell fixture。首次默认并行运行因资源压力主动终止，随后限制包并行和测试并行重跑。
- modeltrace 固定样本校验失败已定位为工作区 CRLF：原始 SHA256 为 eeab6ff54d6ffb2b294ebfd6cce25bce387f7a268b66759144f64c16f6c66b9f；仅在内存中归一化 LF 后为测试要求的 f267d0513aa06981d024ec5394746fceddf44e4e8a75fa6bf8304255f4b32d6d。没有修改样本或削弱测试。
- 定向 race 通过（10.004s）：加密任务检测和完整调度转发、软/硬延迟策略、既有 encrypted sticky 和 SSE 恢复。首次 race 编译的内存限制偏低，中止后调高软限制并降低 CPU 并行，重跑成功。
- 全量中的两项 handler 计时失败单独重跑通过（5.326s）：TestPR610RealWaitNativeIngress、TestPR610RealBridgeRetryDenominator。没有修改阈值。
- 完整 service 单测执行结束但失败（487.685s）。除文件权限、目录 sync 等外，含 Mistral SDK 命令退出 9009、Windows connectex 代理拒绝连接，以及 TestQuality5xxDoesNotSkipRecentlyCompletedPlan 的结果条数断言失败。后两类单独复核仍失败；本修复未触及这些实现，没有做修复前全量对照，不把所有失败笼统归为既有问题。
- 全仓 ./... 检查没有获得一次全部完成且通过的结果：先因资源压力终止，限并行重跑在 service 完整失败后停止后续执行。另一次多余的 service 全量重跑已取消。相关日志保留在工作区父目录 encrypted-agent-hydration-*.log。
- 结论：确认的软粘连遗漏已修复，定向及 race 验证通过；全量未绿，禁止据此宣称达到生产发布验收。未修改测试断言以掩盖失败，未合并版本同步 PR，未发布到生产。

# 质量运维优化：界面复核

> PR 截图已从当前源码中清理，以减小源码包体积。[查看固定提交中的历史截图](https://github.com/ranxi2001/sub2api/tree/297e9201a2f6d6fe70073b370a5b71f49579accf/docs/quality-ops-evidence)；下列文件名与验证说明保留用于追溯。

基线为 `ranxi2001/sub2api` 的 `production` 提交 `0ae36e501952000c5c910a2e616c6e0861f66a49`。图片由本地 Vue 组件的隔离 mock 预览产生，使用虚构账号、分组和结果，不连接服务器、数据库、真实上游或付费接口。布局外壳和导航简化，质量页、规则表单和详情组件使用真实源码。截图仅证明组件的显示状态，不是生产验证或后端验收。

- [修改前概览](https://github.com/ranxi2001/sub2api/blob/297e9201a2f6d6fe70073b370a5b71f49579accf/docs/quality-ops-evidence/before-overview.jpg)：单模型规则，失败后关闭账号调度。
- [修改后概览](https://github.com/ranxi2001/sub2api/blob/297e9201a2f6d6fe70073b370a5b71f49579accf/docs/quality-ops-evidence/after-overview.jpg)：同一规则显示多个检测模型，区分配置目标和处理结果。
- [修改后编辑器](https://github.com/ranxi2001/sub2api/blob/297e9201a2f6d6fe70073b370a5b71f49579accf/docs/quality-ops-evidence/after-editor.jpg)：追加检测模型和完整轮次说明。
- [逐模型结果](https://github.com/ranxi2001/sub2api/blob/297e9201a2f6d6fe70073b370a5b71f49579accf/docs/quality-ops-evidence/after-model-results.jpg)：一个模型失败并冷却，另一个通过且没有新增冷却。

复核步骤：管理员打开智能运维 → 质量运维，编辑糖果题规则并添加第二个模型；选择“保持调度，仅冷却指定模型”，填写两个目标、恢复并发和自动恢复；保存后核对规则卡，打开运营记录详情，核对每个模型的判定和本轮新增冷却。状态探针仍只允许一个模型；BPS 观察不证明直连恢复。不支持模型显示跳过，跳过样本不进入通过率分母。

## 使用与兼容性

`model_ids` 可选，旧规则继续使用单个 `model_id`。每轮最多 50 个模型和 100 个样本，最多 8 个并行模型请求。`remove_models` 动作复用原生 `model_rate_limits`，只处理检测失败的目标模型，保持账号调度、其他模型和人工设置；每个模型独立恢复。`trigger_on_upstream_5xx` 默认关闭，只针对 OAuth 账号，HTTP 或流内被现有分类判定为 5xx 的失败会触发检测，手动/后台探测不递归触发。规则使用该开关和模型冷却动作时，立即降低 OAuth 并发（`recovery_concurrency` 未配置为 5，且不提高低于该值的账号），5xx 本身不判模型降智；通过恢复条件后由原生成功请求机制渐进恢复，并保留原并发上限。

质量规则写入的模型冷却在计划检查时间过去后仍保持限制，只有明确通过且满足恢复条件才释放。规则仍持有模型冷却或并发恢复状态时，普通编辑不能切换动作或删除质量策略；移除目标必须保留所有仍被该规则持有的上游模型，否则返回 409 所有权范围冲突。暂停、修改周期、调整自动恢复以及编辑未被持有的目标不会放行账号或丢弃快照，模板导入也遵循相同保护。模型映射变化后，编辑和探测都会重新核对上游目标；外部 owner 或人工改写的限制继续报告冲突，不会被覆盖或自动释放。

PR596 的定向远端验证见 [dedicated CI](../../.github/workflows/pr596-cooldown-ownership.yml)：仅 PR596 原 head 分支 `fix/quality-state-probe-targets-and-model-input` 的 pull_request 运行实际 repository 包和 testcontainers 数据库测试。RED 保留新增测试，仅替换固定原 head `23b0dbae040e0d0b8c40f25ddb012ce2444feba3` 的三个生产文件，并要求测试断言失败；GREEN 恢复当前生产文件后验证恢复路径及既有所有权保护，日志按当前 head 上传。工作流随本地提交交付，未经推送触发前不代表已动态验证。

没有数据库迁移，新字段存于现有规则/结果 JSON 和原生账号 Extra。回退代码前可先暂停相关规则以停止新增处置，再通过新版本明确恢复本规则拥有的限制；需要探测恢复时保留原动作与目标并重新启用规则，或使用支持该所有权布局的受保护人工解除流程。确认模型限制、并发恢复状态和 ownership 均已清理后，才能改动作关闭自动处置或删除规则；不支持的布局与外部 owner 冲突应保留待复核，不通过手工清理数据库绕过保护。旧版本无法识别新的动作/恢复状态，仅回退二进制不会撤销已持久化的模型冷却或并发变化。Unsupported 观察存于账号 Extra，人工确认支持已恢复后可移除对应的 `quality_unsupported_model_*` 观察，再重新保存规则。

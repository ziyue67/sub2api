# Ranxi production 同步与测试维护（2026-11-19）

本次基于 fork main `8ba68782844b2d2ac08475beed1e72857b57387b`，合入 Ranxi production `feb761541918d25a73779c6ea42ee28f61b5591e`。与前次固定上游 `c2a335ce59356b5f4bb323b34b42a5ce4c115195` 相比，新增 55 个提交、254 个文件变更。

## 功能整合

- Gemini / Vertex Responses 转发复用 Gemini 兼容层，保留工具选择、流式取消和用量；保留 fork 普通 Gemini 流的行大小限制。
- 接入 OpenAI 旧版兼容模式、单账号余额通知关闭、New API 采购成本换算及客户端身份。
- 接入质量结果保存确认、Grok 保存结果后再恢复限流及恢复代次隔离。
- 跟随上游退休旧凭证守护与手动采票流程，保留 V2 凭证巡检，并迁移到共享重登服务。
- 保留 fork VERSION `0.2.23`、邮件及图片归档、原始用量与缓存计费、工具附加费，以及 Sol 订阅 500,000 输入 token 边界。详细批量测试继续保留首字节延迟，并将 Grok 恢复证据传到调度持久化路径。

## 为什么精简历史回放

主 CI 已动态发现全部 Go 单元包并执行完整集成套件。维护成本较高的是另外的固定旧源码 RED：把旧提交的生产文件覆盖到当前依赖树，再复制当前测试并要求指定失败。当新接口签名或布局变化时，旧代码无法编译，必须维护额外兼容层，才能再次证明同一个历史缺陷。

普通 PR 应验证当前版本仍能正确工作；历史代码能否继续与新依赖编译是另一项维护工作。因此保留业务测试，把以下历史证据任务改为手动选择。

| 工作流 | 普通 PR 保留 | 手动 `historical_red=true` 额外运行 |
| --- | --- | --- |
| Account request proxy routing | 原 routing、race、真实 Redis、代理配置切换 | config-switch-red、refresh-oracle-red |
| WS unsent delta takeover | takeover / repair877 的 GREEN | 两个原 RED 矩阵实例 |
| WS plaintext response ownership | continuation / repair870 的 GREEN | RED、修复 RED、PLAINTEXT_RED |
| WS followup error capture | capture / review-close-repair GREEN、当前两轮归因 race | 两个原 RED 矩阵实例与旧两轮归因对照步骤 |
| HTTP recovery native controls | behavior GREEN 与完整性保护 | 全旧生产树恢复的 RED |

共将 10 个历史 job／矩阵实例及一个嵌入的历史对照步骤移出普通 PR。当前版本使用的特殊标签、race 和具名结果断言全部保留；主 CI、安全检查、真实数据库、浏览器功能验收继续自动执行。手动回放仍使用原始基线、原失败数量、控制场景和错误标记；不把编译失败当作预期业务失败。

## 哪些测试实际删除

仅接受上游退休提交 `782fd8fec95dcc85c21e1954e17bca39638c93b3` 对应的 19 份测试删除（10 Go、9 前端），它们测试的旧凭证守护、采票覆盖、手动票据和旧页面已一同删除。原账号守护 API 测试重命名并迁移到共享重登 API，保留其凭据目标和取消传播断言。

V2 凭证巡检仍存在，其工作流和测试继续保留。新增共享重登配置的旧设置迁移、敏感信息隔离、存储失败、非法地址／header、异常输入等测试也继续保留。没有按文件年龄或数量批量删除现行业务测试。

## 维护方式

相关工作流在 Actions 中手动运行时默认只检查当前版本；需要复核历史证据再勾选 `historical_red`。历史回放兼容层保留在仓库供该入口使用，不再要求每次上游同步修复所有历史组合。

测试和 CI 的具体命令与完成结果记录在本次 PR。合并提交前重新生成 Wire，检查工作流语法和选择器自检，并独立比较工作流原有命令、依赖及 GREEN 矩阵是否仍保留。

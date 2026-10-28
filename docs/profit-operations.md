# 独立利润运维（stacked）

Depends-on #585；base `fix/upstream-url-channel-operations`。本PR差异只新增利润页面/API、财务聚合/来源、导航、财务测试和本说明。URL身份解析复用渠道PR，不另建渠道分组。

**修正旧混合#584口径：估算成本 = 保存的usage.total_cost × 当前独立成本倍率extra.cost_multiplier；收入 = 保存actual_cost；利润 = 扣费金额 − 估算成本。** 用户扣费倍率已包含在保存actual_cost，不再乘；account_rate_multiplier不是成本倍率。account_stats_cost可由管理员自定义统计价卡或不同模型价生成，不一定等于标准模型收费基数，故不用于此用户确认的成本公式。旧profit scorer仍用account_stats_cost是其调度评分口径，本PR不改评分或账本。

默认0.1明确调度估算，零倍率保留；缺失/无效/未知不当0，零收入毛利率未知，汇总由金额总额计算。金额使用历史保存值，缓存/按次已含其中，不按当前价卡重算。当前成本倍率/归属无历史快照，逐账号来源及配置updated_at明确为估算，订阅计价不等于现金收款。

- [ ] 本head成本口径RED→GREEN：标准10、统计8、账号扣费倍率999、成本倍率.25、用户扣费4 → 成本2.5，利润1.5。
- [ ] 财务零值/亏损/未知/分页/半开区间/只读HTTP→PG。
- [ ] 本head前端完整/浏览器及全套CI。

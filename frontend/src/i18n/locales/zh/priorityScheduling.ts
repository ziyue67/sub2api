export default {
  teams: {
    enabled: 'Teams 固定成本回本优先', cost: '单号成本（元）', hours: '有效窗口（小时）', conversion: '每扣费单位折合人民币', window: '窗口起点',
    sources: { explicit: '账号明确启用时间', expiry: '账号到期时间往前推', first_usage: '首次使用时间（持久保存）' },
    hint: '默认从账号首次使用开始，4 小时固定成本 50；与用户扣费同单位直接比较。统计该窗口内跨模型、跨分组的用户扣费，未超过成本时优先回本；回本后恢复优先级和动态收益排序。质量和并发限制始终生效。',
    windowHint: '同币种填 1；美元扣费填写每美元对应人民币金额。明确启用时间可在下方批量设置。窗口到期不会自动滚动重置；首次使用时间确定后持久保存；已有账号首次初始化使用最早留存记录。',
    setStart: '同时设置选中 Teams 的启用时间', startHint: '只写入选中的 Teams；重新设置会改变这次成本窗口，请使用该账号本次实际启用时间。',
    recovering: '回本优先', recovered: '已超过成本', progress: '距回本 ¥{gap} · 剩余 {minutes} 分钟', windowProfit: '本窗口利润 ¥{profit}'
  },

  profit: '近期利润 / 利润率',
  economics: { usage: '使用记录', rate: '倍率估算', unknown: '利润样本不足' },
  priority: '优先级',
  batch: {
    typeOrder: '账号类型顺序', typeOrderHint: '拖动或使用箭头调整 Teams、Pro、Plus、API 的顺序，自动生成优先级；API 内按倍率从低到高。应用后写入账号，再次加载按已有优先级还原类型顺序。', moveUp: '上移 {type}', moveDown: '下移 {type}', resetOrder: '恢复 Teams → Pro → Plus → API',

    title: '按账号类别快捷设置', hint: '批量写入真实账号现有字段（不含影子账号），优先调度关闭时也会影响账号调度。加载后可查看命中账号，按 Teams、Pro、Plus 和 API 账号倍率分档设置；不修改计费倍率。',
    group: '限定分组 ID（可留空）', load: '加载账号并生成排序', category: '账号类别 / 倍率', accounts: '命中账号', current: '当前优先级 / 并发 / 负载因子', priority: '新优先级',
    priorityHint: '优先级数值越小越先使用；同一体验档内先比较优先级，再比较动态得分。可手动填写相同优先级，让多类账号一起参与动态竞争。',
    concurrency: '同时设置并发数', loadFactor: '同时设置负载因子', loadHint: '并发数是实际同时请求上限；提高负载因子可提高调度频率。即使并发数为 100、负载因子为 10000，拥堵判断和并发槽仍按实际并发上限执行。未勾选的字段保留原值。',
    preview: '将更新 {count} 个账号的优先级；并发数：{concurrency}；负载因子：{loadFactor}。', keep: '保持原值', apply: '应用到选中账号', empty: '没有待更新的账号。', otherOAuth: 'OAuth · 其他套餐',
    loadError: '加载账号失败或超过 10000 个，请限定分组后重试。', result: '已更新 {count} 个账号。', partial: '部分账号未完成更新，已保留在列表中，可重试。'
  },

  title: '优先调度',
  description: '先满足体验目标，再平衡质量、延迟、并发与成本。',
  enabled: '启用优先调度',
  scopeNote: '适用于 OpenAI 文本请求的自由选路。会话绑定、协议优先级、模型权限、限流与利润门槛继续生效；图片、视频及其他平台沿用原调度。',
  modes: { experience: '体验优先', balanced: '均衡', profit: '利润优先', custom: '自定义' },
  strategy: '调度策略',
  weights: '评分权重',
  quality_weight: '质量', latency_weight: '延迟', load_weight: '并发余量', cost_weight: '利润 / 成本',
  thresholds: '体验目标与统计窗口',
  target_ttft_ms: '首 token P90 目标（毫秒）', max_load_percent: '并发占用上限（%）', min_quality_percent: '质量通过率目标（%）',
  window_minutes: '使用记录窗口（分钟）', min_samples: '延迟 / 利润最少样本数', quality_max_age_hours: '质量结果有效期（小时）',
  groupIds: '生效分组 ID', groupHint: '逗号分隔；留空表示所有分组。',
  models: '生效模型', modelHint: '每行一个请求模型名，精确匹配；留空表示所有模型。',
  rule: '排序顺序：体验达标 → 数据不足 → 体验未达标；同档先按优先级（越小越先），再按策略评分。拥堵或质量不佳时，低倍率不会越过体验分档。',
  costHint: '利润＝用户扣费−理论成本；理论成本沿用使用记录的成本列（（账号统计定价或基础费用）× 当次账号倍率）。按同分组、同模型汇总，利润率＝利润÷用户扣费。Teams 启用固定成本模式时另按单号成本窗口计算；其他 OAuth 缺少样本时不以倍率代替利润，API 可回退倍率估算。',
  save: '保存配置', saving: '保存中…', saved: '配置已保存', loading: '加载中…', retry: '重试', error: '读取或保存失败，请重试', invalid: '请检查分组 ID 和参数范围',
  recent: '最近一次候选评分', refresh: '刷新评分', empty: '暂无评分。启用后，符合范围且需要自由选路的请求会生成评分。',
  historyPending: '历史统计尚未就绪，当前使用原有评分；后台每 30 秒刷新统计。',
  snapshotHint: '仅展示当前服务实例最近一次候选池，最多 100 个账号。分数用于同一协议与订阅优先池内排序，不代表最终选中；实时并发可能继续变化。',
  model: '模型', group: '分组', account: '账号', score: '得分', tier: '状态', latency: 'P90 首 token', load: '并发占用', rate: '成本倍率', quality: '质量通过', samples: '条样本', unknown: '未知',
  tiers: { eligible: '体验达标', insufficient: '数据不足', degraded: '体验未达标' },
  reasons: { quality_below_target: '质量低于目标', quality_unknown: '无有效质量结果', latency_above_target: '延迟超过目标', latency_insufficient: '延迟样本不足', busy: '并发偏高或有排队', load_unknown: '并发数据未知', cost_unknown: '成本未知', recent_errors: '近期错误偏多', historical_loss: '近期用户扣费低于理论成本', profit_insufficient: '利润样本不足', teams_recovery: '本窗口收入尚未超过单号成本', teams_window_unavailable: 'Teams 成本窗口未设置或已失效' }
}

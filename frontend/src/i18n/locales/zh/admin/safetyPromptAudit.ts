export default {
  safetyPromptAudit: {
    nav: '安全注入审计',
    title: '安全注入审计',
    description: '对 OpenAI OAuth 请求注入固定安全政策，并在模型按本次标识明确拒绝时由宿主拦截、记录可复核事件。',
    module: '模块设置',
    enabled: '启用安全注入',
    enabledHint: '启用后对本实例支持的 OpenAI OAuth 出站请求注入固定安全政策。默认关闭。',
    signalEnabled: '启用独立拒绝识别',
    signalHint: '模型完整返回本次 sp- 标识时，宿主替换为固定拒绝回答并记录事件。仅支持 Responses 链路。',
    retentionDays: '命中事件保留天数',
    retentionHint: '范围 1–90 天，最多保留最近 500 条；到期或容量淘汰后不可恢复。',
    save: '保存设置',
    saving: '保存中…',
    saved: '设置已保存',
    refresh: '刷新',
    loading: '正在读取…',
    loadFailed: '读取安全注入审计失败，请重试。',
    saveFailed: '保存安全注入设置失败，请检查输入后重试。',
    storageReady: '事件持久保存正常 · 当前 {count}/{limit} 条',
    storageWarning: '存储告警：{warning}',
    disabledNotice: '安全注入模块当前关闭；历史事件仍可查看。',
    filters: {
      from: '起始日期（本地时间）',
      to: '结束日期（含当天）',
      search: '搜索事件编号 / 请求编号 / 模型 / 摘要',
      all: '全部保留记录',
      today: '今天'
    },
    count: '显示 {shown} / {total} 条。均为模型精确命中，尚未人工确认。',
    badRange: '起始日期不能晚于结束日期。',
    empty: '没有符合条件的记录。',
    table: {
      time: '时间 / 事件编号',
      model: '模型 / 上游账号',
      user: '请求用户',
      summary: '脱敏摘要'
    },
    unknownUser: '未知（宿主未关联）',
    requestId: '请求编号',
    upstreamAccount: '上游账号',
    exportJson: '导出 JSON',
    exportCsv: '导出 CSV',
    noSummary: '无文本摘要',
    scopeNote: '仅覆盖经过支持发送点的 OpenAI OAuth 请求；不修改上游 cyber_policy 判定，不自动封号。'
  }
}

export default {
    emailBroadcast: {
        title: '批量公告邮件', description: '通过邮箱将公告内容发送给指定用户或所有已注册用户。', openComposer: '撰写并发送公告邮件',
        form: {
            subject: '邮件主题', subjectPlaceholder: '例如：本周日凌晨服务维护通知', body: '邮件正文', bodyFormat: '正文格式', bodyFormatText: '纯文本',
            bodyPlaceholderHtml: '可使用基本 HTML 标签，发送前服务端会进行安全清洗。', bodyPlaceholderText: '直接输入纯文本，换行会保留。',
            bodyHint: 'HTML 模式会进行 XSS 安全清洗；纯文本模式会自动处理换行。', recipients: '收件人', sendToAll: '发送给全部已注册用户',
            sendToAllHint: '勾选后将忽略手动选择的收件人。', searchRecipientsPlaceholder: '按邮箱或用户名搜索…', noRecipientsFound: '没有匹配的用户',
            noRecipientsSelected: '请至少选择一位收件人，或勾选发送给全部用户。', selectedRecipient: '指定收件人', singleRecipientHint: '邮件只会发送给此用户。', send: '发送公告邮件', sending: '发送中…'
        },
        recipientsMode: { all: '全部用户', selected: '指定收件人' }, status: { pending: '排队中', sending: '发送中', completed: '已完成', failed: '失败' },
        preview: { title: '实时预览', refreshing: '更新中…', error: '预览生成失败', hint: '预览结果与最终投递邮件一致。', iframeTitle: '公告邮件预览', placeholderSubject: '（在左侧填写邮件主题）', placeholderBody: '（在左侧填写邮件正文，预览会实时刷新。）' },
        toolbar: { p: '段落', b: '加粗', i: '斜体', a: '插入链接', ul: '无序列表', h2: '二级标题', hr: '分隔线', br: '换行' },
        history: { title: '发送历史（最近 10 条）', empty: '尚无发送记录', preview: '预览', delete: '删除', backToList: '返回历史列表', deleteConfirmTitle: '删除发送记录', deleteConfirm: '确定要删除「{subject}」的发送记录吗？', detail: { subject: '主题', status: '状态', recipients: '收件人', sentAt: '发送时间', errorMessage: '错误信息', iframeTitle: '历史邮件预览' } },
        notifications: { sendQueued: '公告邮件已加入发送队列（#{id}）。', loadHistoryFailed: '加载发送历史失败', deleteSuccess: '已删除发送记录' }
    }
};

// 质量运维账户筛选的选项，账户选择器和分组规则卡片共用同一套文案。
export const qualityAccountStatusOptions = [
  { value: 'active', label: 'admin.accounts.status.active' },
  { value: 'rate_limited', label: 'admin.accounts.status.rateLimited' },
  { value: 'temp_unschedulable', label: 'admin.accounts.status.tempUnschedulable' },
  { value: 'unschedulable', label: 'admin.accounts.status.unschedulable' },
  { value: 'error', label: 'admin.accounts.status.error' },
  { value: 'inactive', label: 'admin.accounts.status.inactive' },
]

export const qualityAccountTypeOptions = [
  { value: 'oauth', label: 'qualityOps.oauthAccounts' },
  { value: 'setup-token', label: 'qualityOps.setupTokenAccounts' },
  { value: 'apikey', label: 'qualityOps.apiKeyAccounts' },
]

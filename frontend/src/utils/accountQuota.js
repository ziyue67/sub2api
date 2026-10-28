/** 支持账号配额（总/日/周上限、quota_used 累加、超限停调度）的账号类型，与后端 AccountTypeSupportsQuota 一致。 */
export function supportsAccountQuota(type) {
    return type === 'apikey' || type === 'bedrock' || type === 'service_account';
}

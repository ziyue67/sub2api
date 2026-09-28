import { apiClient } from '../client'

export type TokenGuardV2CredentialMode = 'email_otp_url' | 'password_totp'
export type TokenGuardV2ProxySource = 'account' | 'managed_proxy' | 'mihomo'

export interface TokenGuardV2LoginConfig {
  account_id: number
  login_email: string
  credential_mode: TokenGuardV2CredentialMode
  proxy_source: TokenGuardV2ProxySource
  proxy_id?: number
  otp_url_masked?: string
  password_configured: boolean
  totp_configured: boolean
  configured: boolean
  updated_at?: string
}

export interface TokenGuardV2Task {
  id: number
  account_id: number
  status: string
  stage: string
  error?: string
  attempt: number
  created_at: string
  updated_at: string
  finished_at?: string
}

export interface TokenGuardV2Account {
  account_id: number
  account_name: string
  account_status: string
  schedulable: boolean
  enabled: boolean
  auto_relogin_enabled: boolean
  probe_state: 'pending' | 'ok' | 'auth' | 'transient' | string
  probe_detail: string
  fail_streak: number
  last_probe_at?: string
  last_reauth_at?: string
  next_probe_at: string
  cooldown_until?: string
  blocked_reason?: string
  login_config?: TokenGuardV2LoginConfig
  latest_task?: TokenGuardV2Task
}

export interface TokenGuardV2Rules {
  probe_interval_seconds: number
  retry_interval_seconds: number
  relogin_cooldown_seconds: number
  fail_streak_threshold: number
}

export interface TokenGuardV2Status extends TokenGuardV2Rules {
  accounts: TokenGuardV2Account[]
}

export interface SaveTokenGuardV2Account {
  account_id?: number
  login_email: string
  credential_mode: TokenGuardV2CredentialMode
  proxy_source: TokenGuardV2ProxySource
  proxy_id?: number | null
  password?: string
  totp_secret?: string
  otp_url?: string
  clear_password?: boolean
  clear_totp?: boolean
  enabled: boolean
  auto_relogin_enabled: boolean
}

export async function listTokenGuardV2Accounts(): Promise<TokenGuardV2Status> {
  return (await apiClient.get('/admin/account-ops/token-guard-v2/accounts')).data
}

export async function saveTokenGuardV2Rules(input: TokenGuardV2Rules): Promise<TokenGuardV2Rules> {
  return (await apiClient.put('/admin/account-ops/token-guard-v2/rules', input)).data
}

export async function createTokenGuardV2Account(input: SaveTokenGuardV2Account): Promise<TokenGuardV2Account> {
  return (await apiClient.post('/admin/account-ops/token-guard-v2/accounts', input)).data
}

export async function updateTokenGuardV2Account(accountId: number, input: SaveTokenGuardV2Account): Promise<TokenGuardV2Account> {
  return (await apiClient.put(`/admin/account-ops/token-guard-v2/accounts/${accountId}`, input)).data
}

export async function deleteTokenGuardV2Account(accountId: number): Promise<void> {
  await apiClient.delete(`/admin/account-ops/token-guard-v2/accounts/${accountId}`)
}

export async function probeTokenGuardV2Account(accountId: number): Promise<TokenGuardV2Account> {
  return (await apiClient.post(`/admin/account-ops/token-guard-v2/accounts/${accountId}/probe`)).data
}

export async function reloginTokenGuardV2Account(accountId: number): Promise<TokenGuardV2Task> {
  return (await apiClient.post(`/admin/account-ops/token-guard-v2/accounts/${accountId}/relogin`)).data
}

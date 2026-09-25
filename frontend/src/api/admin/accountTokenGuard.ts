import { apiClient } from '../client'

export interface TokenGuardReloginAccount {
  email: string
  password: string
  mfa_secret: string
}

export interface TokenGuardConfig {
  enabled: boolean
  group_ids: number[]
  interval_seconds: number
  probe_endpoint: string
  probe_model: string
  probe_headers: Record<string, string>
  probe_timeout_seconds: number
  probe_concurrency: number
  max_probe_per_cycle: number
  auto_relogin: boolean
  relogin_endpoint: string
  relogin_headers: Record<string, string>
  relogin_accounts: TokenGuardReloginAccount[]
  restore_schedulable: boolean
  fail_streak_threshold: number
  bark_key: string
  notify_on_fix: boolean
  notify_on_fail: boolean
}

export interface TokenGuardAccountState {
  account_id: number
  account_name: string
  account_status: string
  schedulable: boolean
  probe_state: 'ok' | 'auth' | 'transient' | string
  probe_detail: string
  latency_ms: number
  fail_streak: number
  last_probe_at: string | null
  last_fix_at: string | null
  last_fix_action: string
  last_fix_result: string
  needs_relogin: boolean
  updated_at: string
}

export interface TokenGuardEvent {
  id: number
  account_id: number
  account_name: string
  kind: string
  detail: string
  latency_ms: number
  created_at: string
}

export interface TokenGuardStats {
  probed: number
  healthy: number
  auth_failed: number
  transient: number
  repaired: number
  state_fixed: number
  failed: number
  duration_ms: number
  started_at: number
}

export interface TokenGuardStatus {
  config: TokenGuardConfig
  accounts: TokenGuardAccountState[]
  events: TokenGuardEvent[]
  runtime: {
    running: boolean
    last_run: string | null
    last_message: string
    stats: TokenGuardStats
  }
}

export async function getTokenGuardStatus(): Promise<TokenGuardStatus> {
  return (await apiClient.get('/admin/account-ops/token-guard/status')).data
}

export async function saveTokenGuardConfig(config: TokenGuardConfig): Promise<TokenGuardConfig> {
  return (await apiClient.put('/admin/account-ops/token-guard/config', config)).data
}

export async function runTokenGuard(): Promise<TokenGuardStats> {
  return (await apiClient.post('/admin/account-ops/token-guard/run')).data
}

export async function reloginTokenGuardAccount(accountId: number): Promise<{ account_id: number; action: string }> {
  return (await apiClient.post(`/admin/account-ops/token-guard/accounts/${accountId}/relogin`)).data
}

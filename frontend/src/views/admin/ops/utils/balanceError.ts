import type { OpsErrorDetail } from '@/api/admin/ops'

export type BalanceErrorSource = 'user' | 'upstream' | 'unknown'

const balanceCodes = new Set([
  'insufficient_balance', 'balance_not_enough', 'insufficient_credit',
  'credit_balance_too_low', 'account_balance_insufficient', 'balance_exhausted',
  'upstream_insufficient_balance',
])

function hasBalanceSignal(value: unknown): boolean {
  if (typeof value !== 'string' || value.length > 32768) return false
  const text = value.trim().toLowerCase()
  if (!text) return false
  if (text.startsWith('{')) {
    try {
      const body = JSON.parse(text)
      const error = body?.error ?? body
      return balanceCodes.has(error?.code) || balanceCodes.has(error?.type)
        || hasBalanceSignal(typeof error === 'string' ? error : error?.message)
    } catch { return false }
  }
  // A generic HTTP 402 explanation is not evidence that the upstream balance is exhausted.
  if (text.includes('insufficient balance or billing issue')) return false
  return /insufficient (?:user |account |credit )?balance|credit balance is too low|balance (?:is insufficient|has been exhausted|depleted)|(?:not enough|not sufficient|no enough) balance|余额(?:不足|已用尽|耗尽)|(?:账户|账号)欠费/.test(text)
}

// Source metadata is authoritative. A user's presence or a 402/403 status alone
// does not identify whose balance failed; old logs without provenance stay unknown.
export function balanceErrorSource(error: Partial<OpsErrorDetail> | null | undefined): BalanceErrorSource | null {
  if (!error) return null
  const upstream = error.error_owner === 'provider' || error.error_source === 'upstream_http'
    || (error.upstream_status_code != null && error.upstream_status_code >= 400)
  const signals = upstream
    ? [error.upstream_error_message, error.upstream_error_detail, error.message, error.error_body]
    : [error.message, error.error_body]
  if (!signals.some(hasBalanceSignal)) return null
  if (upstream) return 'upstream'
  if (error.error_source === 'client_request' && error.error_owner === 'client'
    && (error.phase === 'request' || error.phase === 'auth') && !error.account_id) return 'user'
  return 'unknown'
}

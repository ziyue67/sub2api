import { describe, expect, it } from 'vitest'
import { balanceErrorSource } from '../balanceError'
import type { OpsErrorDetail } from '@/api/admin/ops'

const local: Partial<OpsErrorDetail> = {
  phase: 'request', error_owner: 'client', error_source: 'client_request',
  status_code: 403, user_id: 7,
}
const upstream: Partial<OpsErrorDetail> = {
  phase: 'upstream', error_owner: 'provider', error_source: 'upstream_http',
  status_code: 403, user_id: 7, account_id: 9,
}

describe('balanceErrorSource', () => {
  it.each(['insufficient balance', 'Insufficient account balance', 'Insufficient user balance. Please top up your user balance.'])(
    'identifies local user balance for current and historical messages: %s', message => {
      expect(balanceErrorSource({ ...local, message })).toBe('user')
    })
  it.each(['insufficient balance', '账户余额不足，请充值', 'Your credit balance is too low to access the API.'])(
    'identifies the upstream account even when the request has a user: %s', message => {
      expect(balanceErrorSource({ ...upstream, message })).toBe('upstream')
    })
  it('uses upstream evidence when the client only received a generic gateway error', () => {
    expect(balanceErrorSource({
      ...upstream, phase: 'request', error_source: 'gateway', status_code: 502,
      upstream_status_code: 400, message: 'Upstream service unavailable',
      upstream_error_detail: '{"error":{"code":"insufficient_balance","message":"Payment required"}}',
    })).toBe('upstream')
  })
  it('does not infer source from the balance message or the user ID alone', () => {
    expect(balanceErrorSource({ message: 'insufficient balance', user_id: 7 })).toBe('unknown')
  })
  it.each([
    { message: 'Permission denied', status_code: 403 },
    { message: 'Payment required', status_code: 402 },
    { message: 'Upstream payment required: insufficient balance or billing issue', status_code: 502 },
    { message: 'insufficient_quota', status_code: 429 },
    { message: 'weekly quota exceeded', status_code: 429 },
    { message: '{"request":{"input":"余额不足"},"error":{"message":"Invalid request"}}' },
  ])('does not turn unrelated failures into a balance diagnosis: $message', error => {
    expect(balanceErrorSource({ ...upstream, ...error })).toBeNull()
  })
  it('does not replace the final error with an earlier failed attempt', () => {
    expect(balanceErrorSource({ ...upstream, message: 'Request timeout',
      upstream_errors: '[{"message":"insufficient balance"}]',
    })).toBeNull()
  })
})

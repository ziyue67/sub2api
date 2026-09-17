import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import AccountUpstreamBalanceCell from '../AccountUpstreamBalanceCell.vue'
import type { Account } from '@/types'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string) => key
  })
}))

const makeAccount = (overrides: Partial<Account> = {}): Account => ({
  id: 42,
  name: 'upstream',
  platform: 'openai',
  type: 'apikey',
  proxy_id: null,
  concurrency: 1,
  priority: 1,
  status: 'active',
  error_message: null,
  last_used_at: null,
  expires_at: null,
  auto_pause_on_expired: false,
  created_at: '2026-09-17T00:00:00Z',
  updated_at: '2026-09-17T00:00:00Z',
  schedulable: true,
  rate_limited_at: null,
  rate_limit_reset_at: null,
  overload_until: null,
  temp_unschedulable_until: null,
  temp_unschedulable_reason: null,
  session_window_start: null,
  session_window_end: null,
  session_window_status: null,
  ...overrides
})

describe('AccountUpstreamBalanceCell', () => {
  it('only renders for API key accounts', () => {
    const wrapper = mount(AccountUpstreamBalanceCell, { props: { account: makeAccount({ type: 'oauth' }), now: Date.now() } })
    expect(wrapper.text()).toBe('-')
    expect(wrapper.find('[data-testid="upstream-usage-probe"]').exists()).toBe(false)
  })

  it('shows a successful remaining amount and emits probe on refresh', async () => {
    const wrapper = mount(AccountUpstreamBalanceCell, {
      props: {
        account: makeAccount({
          extra: {
            upstream_usage_probe: {
              status: 'ok',
              data: { remaining: 12.5, unit: 'USD' },
              fetched_at: '2026-09-17T00:00:00Z',
              fresh_until: '2026-09-18T00:00:00Z',
              last_attempt_at: '2026-09-17T00:00:00Z',
              next_probe_at: '2026-09-17T01:00:00Z'
            }
          }
        }),
        now: Date.parse('2026-09-17T00:30:00Z')
      }
    })
    expect(wrapper.get('[data-testid="upstream-usage-value"]').text()).toContain('12.5 USD')
    expect(wrapper.text()).not.toContain('账号成本')
    expect(wrapper.text()).not.toContain('费用')
    await wrapper.get('[data-testid="upstream-usage-probe"]').trigger('click')
    expect(wrapper.emitted('probe')).toHaveLength(1)
  })

  it('marks an old successful snapshot as stale and handles failed or unsupported states', () => {
    const old = makeAccount({
      extra: {
        upstream_usage_probe: {
          status: 'ok',
          data: { balance: 8 },
          fetched_at: '2026-09-15T00:00:00Z',
          fresh_until: '2026-09-16T00:00:00Z',
          last_attempt_at: '2026-09-15T00:00:00Z',
          next_probe_at: '2026-09-16T00:00:00Z'
        }
      }
    })
    const stale = mount(AccountUpstreamBalanceCell, { props: { account: old, now: Date.parse('2026-09-17T00:00:00Z') } })
    expect(stale.text()).toContain('admin.accounts.upstreamUsage.stale')

    const failed = mount(AccountUpstreamBalanceCell, {
      props: {
        account: makeAccount({ extra: { upstream_usage_probe: { status: 'failed', last_attempt_at: '2026-09-17T00:00:00Z', next_probe_at: '2026-09-17T01:00:00Z' } } }),
        now: Date.now()
      }
    })
    expect(failed.text()).toContain('admin.accounts.upstreamUsage.failed')

    const unsupported = mount(AccountUpstreamBalanceCell, {
      props: {
        account: makeAccount({ extra: { upstream_usage_probe: { status: 'unsupported', last_attempt_at: '2026-09-17T00:00:00Z', next_probe_at: '2026-09-17T01:00:00Z' } } }),
        now: Date.now()
      }
    })
    expect(unsupported.text()).toContain('admin.accounts.upstreamUsage.unsupported')
  })

  it('disables refresh while a probe is running', () => {
    const wrapper = mount(AccountUpstreamBalanceCell, { props: { account: makeAccount(), now: Date.now(), probing: true } })
    expect(wrapper.get('[data-testid="upstream-usage-probe"]').attributes('disabled')).toBeDefined()
  })
})

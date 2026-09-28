import { mount } from '@vue/test-utils'
import { expect, it, vi } from 'vitest'
import { ref } from 'vue'
import MonitorStatusCards from '../MonitorStatusCards.vue'
import type { MonitorMatrixRow } from '@/api/channelMonitorV2'

vi.mock('vue-i18n', async (importOriginal) => ({ ...await importOriginal<typeof import('vue-i18n')>(), useI18n: () => ({ t: (key: string) => key, locale: ref('zh-CN') }) }))
it('renders platform groups, three passive metrics and observed-only candy bars', () => {
  const now = Date.parse('2026-09-28T01:00:00Z')
  const row = { platform: 'openai', group_id: 7, group_name: 'Group One', group_rate_multiplier: 0.2,
    metrics: { has_samples: true, request_count: 0, cache_rate: 0.837, error_rate: 0.001, ttft: { p50_ms: 15000 } },
    health: { overall: 'healthy', error_rate: 'healthy' }, buckets: [],
    candy: { model: 'model', reasoning_effort: 'medium', interval_minutes: 1, results: [{ checked_at: '2026-09-28T00:59:00Z', verdict: 'incorrect', latency_ms: 50 }] },
  } as MonitorMatrixRow
  const wrapper = mount(MonitorStatusCards, { props: { items: [row], countdown: 32, loading: false, now }, global: { stubs: { ProviderIcon: true } } })
  expect(wrapper.text()).toContain('Group One')
  expect(wrapper.text()).toContain('83.7%')
  expect(wrapper.text()).toContain('99.9%')
  expect(wrapper.text()).toContain('15.0s')
  expect(wrapper.text()).toContain('channelMonitorV2.candy.states.incorrect')
  expect(wrapper.findAll('[data-testid="candy-history"] span[tabindex="0"]')).toHaveLength(1)
  expect(wrapper.find('span[tabindex="0"]').classes()).toContain('bg-amber-400')
  wrapper.unmount()
})

it('does not show zero traffic as 100 percent availability or invent candy records', () => {
  const row = { platform: 'openai', group_id: 7, group_name: 'No samples', metrics: { has_samples: false, request_count: 0, cache_rate: 0, error_rate: 0, ttft: { p50_ms: null } }, health: { overall: 'unknown' }, buckets: [], candy: { model: 'model', reasoning_effort: 'medium', interval_minutes: 1, results: [] } } as unknown as MonitorMatrixRow
  const wrapper = mount(MonitorStatusCards, { props: { items: [row], countdown: 32, loading: false, now: Date.now() }, global: { stubs: { ProviderIcon: true } } })
  expect(wrapper.text()).not.toContain('100.0%')
  expect(wrapper.text()).toContain('channelMonitorV2.candy.waiting')
  expect(wrapper.findAll('span[tabindex="0"]')).toHaveLength(0)
  wrapper.unmount()
})

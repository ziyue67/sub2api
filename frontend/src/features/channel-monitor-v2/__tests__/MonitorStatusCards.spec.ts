import { mount } from '@vue/test-utils'
import { afterEach, expect, it, vi } from 'vitest'
import { ref } from 'vue'
import MonitorStatusCards from '../MonitorStatusCards.vue'
import type { MonitorCoverage, MonitorMatrixRow } from '@/api/channelMonitorV2'

vi.mock('vue-i18n', async (importOriginal) => ({ ...await importOriginal<typeof import('vue-i18n')>(), useI18n: () => ({ t: (key: string, values?: Record<string, unknown>) => key + (values ? ' ' + Object.values(values).join(' ') : ''), locale: ref('zh-CN') }) }))
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
  expect(wrapper.findAll('[data-testid="candy-history-bar"]')).toHaveLength(1)
  expect(wrapper.find('[data-testid="candy-history-bar"]').classes()).toContain('bg-amber-400')
  wrapper.unmount()
})

it('does not show zero traffic as 100 percent availability or invent candy records', () => {
  const row = { platform: 'openai', group_id: 7, group_name: 'No samples', metrics: { has_samples: false, request_count: 0, cache_rate: 0, error_rate: 0, ttft: { p50_ms: null } }, health: { overall: 'unknown' }, buckets: [], candy: { model: 'model', reasoning_effort: 'medium', interval_minutes: 1, results: [] } } as unknown as MonitorMatrixRow
  const wrapper = mount(MonitorStatusCards, { props: { items: [row], countdown: 32, loading: false, now: Date.now() }, global: { stubs: { ProviderIcon: true } } })
  expect(wrapper.text()).not.toContain('100.0%')
  expect(wrapper.text()).toContain('channelMonitorV2.candy.waiting')
  expect(wrapper.findAll('[data-testid="candy-history-bar"]')).toHaveLength(0)
  wrapper.unmount()
})

const mounted: ReturnType<typeof mount>[] = []
afterEach(() => {
  for (const wrapper of mounted.splice(0)) wrapper.unmount()
  vi.useRealTimers()
  document.body.innerHTML = ''
})

function hoverFixture() {
  const metrics = { has_samples: true, request_count: 0, cache_rate: 0.881, error_rate: 0, ttft: { p50_ms: 8000 } }
  const row = {
    platform: 'openai', group_id: 7, group_name: 'Demo group', metrics,
    health: { overall: 'healthy', error_rate: 'healthy' },
    buckets: [
      { bucket_start: '2026-09-28T00:06:00Z', metrics, health: { overall: 'healthy' } },
      { bucket_start: '2026-09-28T00:07:00Z', metrics: { ...metrics, cache_rate: 0.5, error_rate: 0.25, ttft: { p50_ms: null } }, health: { overall: 'warning' } },
    ],
    candy: { model: 'demo', reasoning_effort: 'medium', interval_minutes: 1, results: [{ checked_at: '2026-09-28T00:59:00Z', verdict: 'error', latency_ms: 1500 }] },
  } as MonitorMatrixRow
  const coverage = { requested_start: '2026-09-28T00:00:00Z', requested_end: '2026-09-28T01:30:00Z' } as MonitorCoverage
  const wrapper = mount(MonitorStatusCards, { attachTo: document.body, props: { items: [row], coverage, countdown: 32, loading: false, now: Date.now() }, global: { stubs: { ProviderIcon: true } } })
  mounted.push(wrapper)
  return { wrapper, row }
}
const tooltip = () => document.querySelector<HTMLElement>('[role="tooltip"]')

it('shows each original bucket on hover, with rates and P50 but no absolute volume', async () => {
  const { wrapper } = hoverFixture()
  const bar = wrapper.findAll('[data-testid="traffic-history-bar"]')[1]
  await bar.trigger('mouseenter')
  expect(tooltip()?.textContent).toContain('Demo group')
  const samples = tooltip()!.querySelectorAll('[data-testid="monitor-card-tooltip-sample"]')
  expect(samples).toHaveLength(2)
  expect(samples[0].textContent).toContain('100.0% 88.1% 8.0s')
  expect(samples[1].textContent).toContain('75.0% 50.0% -')
  expect(tooltip()?.textContent).not.toMatch(/request_count|token_count|RPM|TPM/)
  expect(bar.attributes('aria-describedby')).toBe(tooltip()?.id)
  expect(bar.attributes('title')).toBeUndefined()
})

it('shows empty history without inventing availability or latency', async () => {
  const { wrapper } = hoverFixture()
  await wrapper.find('[data-testid="traffic-history-bar"]').trigger('mouseenter')
  expect(tooltip()?.textContent).toContain('channelMonitorV2.matrix.noTraffic')
  expect(tooltip()?.textContent).not.toContain('100.0%')
  expect(tooltip()?.querySelectorAll('[data-testid="monitor-card-tooltip-sample"]')).toHaveLength(0)
})

it('supports keyboard focus, Escape, touch-style clicks and outside dismissal', async () => {
  const { wrapper } = hoverFixture()
  const bar = wrapper.findAll('[data-testid="traffic-history-bar"]')[1]
  await bar.trigger('focus')
  expect(tooltip()).not.toBeNull()
  document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
  await wrapper.vm.$nextTick()
  expect(tooltip()).toBeNull()
  await bar.trigger('click')
  expect(tooltip()).not.toBeNull()
  document.body.dispatchEvent(new Event('pointerdown', { bubbles: true }))
  await wrapper.vm.$nextTick()
  expect(tooltip()).toBeNull()
})

it('keeps details open when entering or scrolling the tooltip and closes on leave', async () => {
  vi.useFakeTimers()
  const { wrapper } = hoverFixture()
  const bar = wrapper.findAll('[data-testid="traffic-history-bar"]')[1]
  await bar.trigger('mouseenter')
  await bar.trigger('mouseleave')
  tooltip()!.dispatchEvent(new MouseEvent('mouseenter'))
  await vi.advanceTimersByTimeAsync(150)
  expect(tooltip()).not.toBeNull()
  tooltip()!.querySelector('[data-testid="monitor-card-tooltip-content"]')!.dispatchEvent(new Event('scroll'))
  expect(tooltip()).not.toBeNull()
  tooltip()!.dispatchEvent(new MouseEvent('mouseleave'))
  await vi.advanceTimersByTimeAsync(150)
  expect(tooltip()).toBeNull()
})

it('dismisses stale details when data changes or the viewport moves', async () => {
  const { wrapper, row } = hoverFixture()
  const bar = wrapper.findAll('[data-testid="traffic-history-bar"]')[1]
  await bar.trigger('mouseenter')
  await wrapper.setProps({ items: [{ ...row, group_name: 'Refreshed group' }] })
  expect(tooltip()).toBeNull()
  await bar.trigger('mouseenter')
  window.dispatchEvent(new Event('resize'))
  await wrapper.vm.$nextTick()
  expect(tooltip()).toBeNull()
  await bar.trigger('mouseenter')
  window.dispatchEvent(new Event('scroll'))
  await wrapper.vm.$nextTick()
  expect(tooltip()).toBeNull()
})

it('shows candy time, verdict and duration, rendering an authorized preview only as text', async () => {
  const { wrapper, row } = hoverFixture()
  const result = row.candy!.results[0]
  await wrapper.find('[data-testid="candy-history-bar"]').trigger('mouseenter')
  expect(tooltip()?.textContent).toContain('channelMonitorV2.candy.states.error')
  expect(tooltip()?.textContent).toContain('1.5s')
  const preview = '<img src=x onerror=alert(1)>'
  await wrapper.setProps({ items: [{ ...row, candy: { ...row.candy!, results: [{ ...result, answer_preview: preview }] } }] })
  await wrapper.find('[data-testid="candy-history-bar"]').trigger('mouseenter')
  expect(tooltip()?.textContent).toContain(preview)
  expect(tooltip()?.querySelector('img')).toBeNull()
})

it('removes the teleported tooltip and pending close timer on unmount', async () => {
  vi.useFakeTimers()
  const { wrapper } = hoverFixture()
  const bar = wrapper.findAll('[data-testid="traffic-history-bar"]')[1]
  await bar.trigger('mouseenter')
  await bar.trigger('mouseleave')
  wrapper.unmount()
  mounted.splice(mounted.indexOf(wrapper), 1)
  expect(tooltip()).toBeNull()
  expect(vi.getTimerCount()).toBe(0)
})

it('keeps the intelligence section visible when the API omits candy instead of treating missing checks as healthy', async () => {
  const { wrapper, row } = hoverFixture()
  await wrapper.setProps({ items: [{ ...row, candy: undefined }] })
  const section = wrapper.get('[data-testid="candy-history"]')
  expect(section.text()).toContain('channelMonitorV2.candy.title')
  expect(section.text()).toContain('channelMonitorV2.candy.disabled')
  expect(section.find('[data-testid="candy-not-configured"]').exists()).toBe(true)
  expect(section.findAll('[data-testid="candy-history-bar"]')).toHaveLength(0)
  expect(section.text()).not.toContain('channelMonitorV2.candy.states.correct')
  expect(section.text()).not.toContain('channelMonitorV2.candy.waiting')
})

it('distinguishes an enabled check awaiting its first result from a disabled check', async () => {
  const { wrapper, row } = hoverFixture()
  await wrapper.setProps({ items: [{ ...row, candy: { ...row.candy!, results: [] } }] })
  const section = wrapper.get('[data-testid="candy-history"]')
  expect(section.text()).toContain('channelMonitorV2.candy.waiting')
  expect(section.text()).toContain('channelMonitorV2.candy.states.unknown')
  expect(section.text()).not.toContain('channelMonitorV2.candy.disabled')
  expect(section.findAll('[data-testid="candy-history-bar"]')).toHaveLength(0)
})

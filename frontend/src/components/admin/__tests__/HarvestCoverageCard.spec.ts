import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import Card from '../HarvestCoverageCard.vue'
import AccountCoverage from '../HarvestAccountCoverage.vue'
vi.mock('vue-i18n', async () => {
  const { default: strings } = await import('@/i18n/locales/zh/admin/harvestFlow')
  return { useI18n: () => ({ t: (key: string, values: Record<string, unknown> = {}) => {
    const message = strings.harvestFlow[key.split('.').pop() as keyof typeof strings.harvestFlow]
    return String(message).replace(/\{(\w+)\}/g, (_, name) => String(values[name]))
  } }) }
})
const coverage = { account_count: 2, average_ready_seconds: 900, average_missing_seconds: 3600, average_percent: 20, sample_interval_seconds: 5, started_at: '2026-10-12T00:00:00Z', updated_at: '2026-10-12T01:15:00Z' }
function render(value: typeof coverage | undefined = coverage, now = '2026-10-12T01:15:05Z') {
  return mount(Card, { props: { coverage: value, generatedAt: now } })
}
describe('HarvestCoverageCard', () => {
  it('shows individual account coverage separately from the aggregate', () => {
    const w = mount(AccountCoverage, { props: { generatedAt: coverage.updated_at, coverage: { started_at: coverage.started_at, updated_at: coverage.updated_at, ready_seconds: 900, missing_seconds: 3600, percent: 20 } } })
    expect(w.text()).toContain('账号持票占比 · 20.0%')
    expect(w.text()).toContain('有票 / 无票：15.0min / 60.0min')
    expect(w.text()).not.toContain('平均有票')
    expect(w.text()).toContain('本账号统计始于')
    expect(mount(AccountCoverage, { props: { generatedAt: coverage.updated_at } }).text()).toContain('等待有效统计样本')
  })
  it('shows the agreed time denominator and account count', () => {
    const w = render()
    expect(w.text()).toContain('20.0%')
    expect(w.text()).toContain('15.0min / 60.0min')
    expect(w.text()).toContain('2 个账号')
    expect(w.text()).not.toContain('后台采样已延迟')
  })
  it('distinguishes unavailable samples from genuine zero coverage', () => {
    const w = render({ ...coverage, account_count: 0 })
    expect(w.text()).toContain('等待有效统计样本')
    expect(w.text()).not.toContain('20.0%')
    expect(render({ ...coverage, average_percent: 0 }).text()).toContain('0.0%')
  })
  it('warns on stale observations', () => {
    expect(render(coverage, '2026-10-12T01:16:00Z').text()).toContain('后台采样已延迟')
  })
})

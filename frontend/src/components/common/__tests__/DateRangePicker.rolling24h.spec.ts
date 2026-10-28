import { enableAutoUnmount, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ref } from 'vue'
import DateRangePicker from '../DateRangePicker.vue'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key, locale: ref('en') })
}))
enableAutoUnmount(afterEach)
beforeEach(() => vi.useFakeTimers())
afterEach(() => vi.useRealTimers())

const mountPicker = (startDate = '2026-09-30', endDate = '2026-10-01') => mount(DateRangePicker, {
  props: { startDate, endDate, rolling24Hours: true },
  global: { stubs: { Icon: true } }
})

describe('DateRangePicker rolling 24 hours', () => {
  it.each([
    '2026-10-01T00:00:00.123+09:00',
    '2026-10-01T23:59:59.999+09:00',
    // A rolling day remains 24 hours across both DST transitions.
    '2026-03-08T03:30:00.123-04:00',
    '2026-11-01T01:30:00.123-05:00'
  ])('emits precise instants for [now - 24h, now) at %s', async (instant) => {
    const now = new Date(instant)
    vi.setSystemTime(now)
    const wrapper = mountPicker()
    await wrapper.get('.date-picker-trigger').trigger('click')
    await wrapper.findAll('.date-picker-preset').find(b => b.text() === 'dates.last24Hours')!.trigger('click')
    await wrapper.get('.date-picker-apply').trigger('click')

    const startDate = new Date(now.getTime() - 86_400_000).toISOString()
    const endDate = now.toISOString()
    expect(wrapper.emitted('change')?.[0]).toEqual([{ startDate, endDate, preset: 'last24Hours' }])
    expect(wrapper.emitted('update:startDate')?.[0]).toEqual([startDate])
    expect(wrapper.emitted('update:endDate')?.[0]).toEqual([endDate])
    expect(Date.parse(endDate) - Date.parse(startDate)).toBe(86_400_000)
  })

  it('anchors the window to Apply even when midnight passes after selecting the preset', async () => {
    vi.setSystemTime(new Date('2026-09-30T23:59:59.999+09:00'))
    const wrapper = mountPicker()
    await wrapper.get('.date-picker-trigger').trigger('click')
    await wrapper.findAll('.date-picker-preset').find(b => b.text() === 'dates.last24Hours')!.trigger('click')
    const appliedAt = new Date('2026-10-01T00:00:00.123+09:00')
    vi.setSystemTime(appliedAt)
    await wrapper.get('.date-picker-apply').trigger('click')
    expect(wrapper.emitted('change')?.[0]?.[0]).toEqual({
      startDate: new Date(appliedAt.getTime() - 86_400_000).toISOString(),
      endDate: appliedAt.toISOString(),
      preset: 'last24Hours'
    })
  })

  it('does not recognize two manually supplied dates as a rolling window', () => {
    vi.setSystemTime(new Date(2026, 9, 1, 12))
    expect(mountPicker().get('.date-picker-trigger').text()).not.toContain('dates.last24Hours')
  })

  it('keeps timestamp props usable in date inputs and manual edits switch to calendar dates', async () => {
    const now = new Date(2026, 9, 1, 0, 1, 2, 123)
    vi.setSystemTime(now)
    const wrapper = mountPicker(new Date(now.getTime() - 86_400_000).toISOString(), now.toISOString())
    expect(wrapper.get('.date-picker-trigger').text()).toContain('dates.last24Hours')
    await wrapper.get('.date-picker-trigger').trigger('click')
    const inputs = wrapper.findAll<HTMLInputElement>('input[type="date"]')
    expect(inputs.map(i => i.element.value)).toEqual(['2026-09-30', '2026-10-01'])
    await inputs[0].setValue('2026-09-29')
    await wrapper.get('.date-picker-apply').trigger('click')
    expect(wrapper.emitted('change')?.[0]?.[0]).toEqual({
      startDate: '2026-09-29', endDate: '2026-10-01', preset: null
    })
  })

  it.each([
    ['dates.today', '2026-10-01', '2026-10-01'],
    ['dates.yesterday', '2026-09-30', '2026-09-30'],
    ['dates.last7Days', '2026-09-25', '2026-10-01'],
    ['dates.last14Days', '2026-09-18', '2026-10-01'],
    ['dates.last30Days', '2026-09-02', '2026-10-01'],
    ['dates.thisMonth', '2026-10-01', '2026-10-01'],
    ['dates.lastMonth', '2026-09-01', '2026-09-30']
  ])('preserves calendar dates for %s', async (label, startDate, endDate) => {
    vi.setSystemTime(new Date(2026, 9, 1, 0, 1))
    const wrapper = mountPicker()
    await wrapper.get('.date-picker-trigger').trigger('click')
    await wrapper.findAll('.date-picker-preset').find(b => b.text() === label)!.trigger('click')
    await wrapper.get('.date-picker-apply').trigger('click')
    expect(wrapper.emitted('change')?.[0]?.[0]).toMatchObject({ startDate, endDate })
  })
})

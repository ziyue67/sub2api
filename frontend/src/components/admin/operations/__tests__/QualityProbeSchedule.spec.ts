import { afterEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, ref } from 'vue'
import { enableAutoUnmount, mount } from '@vue/test-utils'
import QualityProbeSchedule from '../QualityProbeSchedule.vue'

vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

enableAutoUnmount(afterEach)

function mountSchedule(initial = '*/2 * * * *') {
  return mount(defineComponent({
    components: { QualityProbeSchedule },
    setup: () => ({ cron: ref(initial) }),
    template: '<QualityProbeSchedule v-model="cron" />',
  }))
}

describe('QualityProbeSchedule', () => {
  it('shows the two-minute preset and updates the schedule on selection', async () => {
    const wrapper = mountSchedule()
    const select = wrapper.get<HTMLSelectElement>('[data-testid="quality-probe-interval"]')
    expect(select.element.value).toBe('*/2 * * * *')
    expect(wrapper.find('[data-testid="quality-probe-cron"]').exists()).toBe(false)
    await select.setValue('0 */2 * * *')
    expect(wrapper.vm.cron).toBe('0 */2 * * *')
    await select.setValue('0 0 * * *')
    expect(wrapper.vm.cron).toBe('0 0 * * *')
  })

  it('reads custom schedules without changing them and supports editing or returning to a preset', async () => {
    const wrapper = mountSchedule('13 9 * * 1-5')
    expect(wrapper.vm.cron).toBe('13 9 * * 1-5')
    expect(wrapper.get<HTMLSelectElement>('select').element.value).toBe('custom')
    const cron = wrapper.get<HTMLInputElement>('[data-testid="quality-probe-cron"]')
    expect(cron.element.value).toBe('13 9 * * 1-5')
    await cron.setValue(' 7 8 * * 1 ')
    expect(wrapper.vm.cron).toBe('7 8 * * 1')
    await wrapper.get('select').setValue('*/5 * * * *')
    expect(wrapper.vm.cron).toBe('*/5 * * * *')
    expect(wrapper.find('[data-testid="quality-probe-cron"]').exists()).toBe(false)
  })

  it('keeps the existing expression when entering custom mode', async () => {
    const wrapper = mountSchedule('*/30 * * * *')
    await wrapper.get('select').setValue('custom')
    expect(wrapper.vm.cron).toBe('*/30 * * * *')
    await wrapper.get('[data-testid="quality-probe-cron"]').setValue('*/2 * * * *')
    expect(wrapper.get<HTMLSelectElement>('select').element.value).toBe('custom')
    expect(wrapper.find('[data-testid="quality-probe-cron"]').exists()).toBe(true)
  })
})

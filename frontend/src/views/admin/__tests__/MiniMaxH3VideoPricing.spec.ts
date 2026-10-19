import { mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import { describe, expect, it } from 'vitest'
import MiniMaxH3VideoPricing from '../MiniMaxH3VideoPricing.vue'
import { createVideoModelPricesForm, serializeVideoModelPrices } from '../groupsVideoModelPricing'

function render(prices: Record<string, Record<string, number>> = {}) {
  const i18n = createI18n({ legacy: false, locale: 'en', missingWarn: false, fallbackWarn: false })
  return mount(MiniMaxH3VideoPricing, { props: { modelValue: createVideoModelPricesForm(prices), multiplier: 0.1 }, global: { plugins: [i18n] } })
}

describe('MiniMax-H3 video pricing', () => {
  it.each(['per_second', 'request_5s'])('keeps decimal input editable and saves numeric prices for %s', async (key) => {
    const existing = { 'grok-imagine-video': { '720p': 0.09 } }
    const wrapper = render({ ...existing, 'minimax-h3': { [key]: 0 } })
    const input = wrapper.get('input[min="0"]')

    for (const value of ['0.0', '0.07', '0.005', '0', '']) {
      await input.setValue(value)
      const next = wrapper.emitted('update:modelValue')!.at(-1)![0] as ReturnType<typeof createVideoModelPricesForm>
      await wrapper.setProps({ modelValue: next })
      expect((input.element as HTMLInputElement).value).toBe(value)
      const saved = serializeVideoModelPrices(next)
      expect(saved['grok-imagine-video']).toEqual(existing['grok-imagine-video'])
      expect(saved['minimax-h3']).toEqual(value === '' ? undefined : { [key]: Number(value) })
      if (value === '0.07') {
        const reloaded = render(saved)
        expect((reloaded.get('input[min="0"]').element as HTMLInputElement).value).toBe('0.07')
        reloaded.unmount()
      }
    }
  })

  it('does not enable video or change existing Grok prices by default', async () => {
    const existing = { 'grok-imagine-video': { '720p': 0.07 } }
    const wrapper = render(existing)
    expect(wrapper.findAll('input[type="number"]')).toHaveLength(0)
    await wrapper.get('input[type="checkbox"]').setValue(true)
    const next = wrapper.emitted('update:modelValue')![0][0] as ReturnType<typeof createVideoModelPricesForm>
    expect(next['minimax-h3']).toEqual({ per_second: null })
    expect(serializeVideoModelPrices(next)).toEqual(existing)
  })

  it('round-trips a fixed duration per-request price and removes only H3 on disable', async () => {
    const wrapper = render({ 'minimax-h3': { request_5s: 0.08 }, 'grok-imagine-video': { '720p': 0.07 } })
    expect((wrapper.get('select').element as HTMLSelectElement).value).toBe('per_request')
    expect((wrapper.get('input[min="1"]').element as HTMLInputElement).value).toBe('5')
    expect((wrapper.get('input[min="0"]').element as HTMLInputElement).value).toBe('0.08')
    await wrapper.get('input[min="1"]').setValue('10')
    const next = wrapper.emitted('update:modelValue')![0][0] as ReturnType<typeof createVideoModelPricesForm>
    expect(serializeVideoModelPrices(next)['minimax-h3']).toEqual({ request_10s: 0.08 })
    await wrapper.get('input[type="checkbox"]').setValue(false)
    const disabled = wrapper.emitted('update:modelValue')!.at(-1)![0] as ReturnType<typeof createVideoModelPricesForm>
    expect(serializeVideoModelPrices(disabled)).toEqual({ 'grok-imagine-video': { '720p': 0.07 } })
  })

  it('preserves zero prices and switches between billing modes', async () => {
    const wrapper = render({ 'minimax-h3': { per_second: 0 } })
    await wrapper.get('select').setValue('per_request')
    const next = wrapper.emitted('update:modelValue')![0][0] as ReturnType<typeof createVideoModelPricesForm>
    expect(serializeVideoModelPrices(next)['minimax-h3']).toEqual({ request_1s: 0 })
    expect(wrapper.get('input[min="0"]').attributes('required')).toBeDefined()
  })
})

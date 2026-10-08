import { DOMWrapper, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
import TestModelSelect from '../TestModelSelect.vue'

vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
let wrapper: ReturnType<typeof mount>
afterEach(() => { wrapper?.unmount(); document.body.innerHTML = '' })

function mountPicker() {
  wrapper = mount(TestModelSelect, {
    attachTo: document.body,
    props: {
      id: 'test-model', modelValue: 'gpt-6-astra', label: 'Model', hint: 'Choose or enter a model',
      options: [
        { value: 'gpt-6-astra', label: 'gpt-6-astra (GPT-6 Astra)' },
        { value: 'gpt-6.1-sol', label: 'gpt-6.1-sol (GPT 6.1)' }
      ]
    }
  })
}

describe('TestModelSelect', () => {
  it('finds and selects the full model ID even when the display name differs', async () => {
    mountPicker()
    await wrapper.get('#test-model').trigger('click')
    await new DOMWrapper(document.querySelector('.select-search-input')!).setValue('gpt-6.1-sol')
    const option = [...document.querySelectorAll('[role="option"]')].find(el => el.textContent?.includes('(GPT 6.1)'))!
    expect(option).toBeDefined()
    await new DOMWrapper(option).trigger('click')
    expect(wrapper.emitted('update:modelValue')).toEqual([['gpt-6.1-sol']])
  })

  it('accepts a model absent from the list and disables editing while running', async () => {
    mountPicker()
    await wrapper.get('[data-testid="model-input-toggle"]').trigger('click')
    await wrapper.get('input').setValue('future-model')
    expect(wrapper.emitted('update:modelValue')).toEqual([['future-model']])
    await wrapper.setProps({ modelValue: 'future-model' })
    await wrapper.get('[data-testid="model-input-toggle"]').trigger('click')
    expect(wrapper.get('#test-model').text()).toContain('future-model')
    await wrapper.get('[data-testid="model-input-toggle"]').trigger('click')
    await wrapper.setProps({ disabled: true })
    expect(wrapper.get('input').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[data-testid="model-input-toggle"]').attributes('disabled')).toBeDefined()
  })
})

import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'

import GroupSelector from '../GroupSelector.vue'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key }),
  }
})

describe('GroupSelector DeepSeek groups', () => {
  it('shows DeepSeek and composite groups while excluding other concrete platforms', () => {
    const wrapper = mount(GroupSelector, {
      props: {
        modelValue: [],
        platform: 'deepseek',
        groups: [
          { id: 1, name: 'DeepSeek', platform: 'deepseek', rate_multiplier: 1 },
          { id: 2, name: 'Composite', platform: 'composite', rate_multiplier: 1 },
          { id: 3, name: 'OpenAI', platform: 'openai', rate_multiplier: 1 },
        ] as any,
      },
      global: {
        stubs: {
          GroupBadge: { props: ['name'], template: '<span>{{ name }}</span>' },
          Icon: true,
        },
      },
    })

    expect(wrapper.text()).toContain('DeepSeek')
    expect(wrapper.text()).toContain('Composite')
    expect(wrapper.text()).not.toContain('OpenAI')
    expect(wrapper.findAll('input[type="checkbox"]')).toHaveLength(2)
  })
})

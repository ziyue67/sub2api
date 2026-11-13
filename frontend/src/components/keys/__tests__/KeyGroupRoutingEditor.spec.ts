import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia } from 'pinia'
import Select from '@/components/common/Select.vue'
import type { ApiKey, ApiKeyGroupRouting } from '@/types'
import { cloneGroupRouting, legacyGroupRouting, validGroupRouting } from '@/utils/keyGroupRouting'
import KeyGroupRoutingEditor from '../KeyGroupRoutingEditor.vue'

vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ cachedPublicSettings: null }) }))

const groups = [{ value: 1, label: 'First', rate: 1, userRate: null }, { value: 2, label: 'Second', rate: 0.5, userRate: null }]
const policy = (): ApiKeyGroupRouting => ({ strategy: 'lowest_rate', failover: false, routes: [{ group_id: 1, priority: 2, weight: 3, enabled: true, cooldown_seconds: 60 }] })
function render(value: ApiKeyGroupRouting | null = policy()) {
  return mount(KeyGroupRoutingEditor, { props: { modelValue: value, legacy: policy(), groups }, global: { stubs: { Select: true } } })
}

describe('unified group routing', () => {
  const protocolGroups = [
    { value: 1, label: 'Same name', platform: 'anthropic' as const, rate: 1, userRate: 0 },
    { value: 2, label: 'Same name', platform: 'openai' as const, rate: 0.5, userRate: 0.3 },
    { value: 3, label: 'DeepSeek', platform: 'deepseek' as const, rate: 0.2, userRate: null }
  ]

  it('filters additions by exact platform without changing existing routes', async () => {
    const wrapper = mount(KeyGroupRoutingEditor, { props: { modelValue: policy(), legacy: policy(), groups: protocolGroups }, global: { stubs: { Select: true } } })
    await wrapper.get('[data-testid="routing-platform"]').setValue('openai')
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
    expect(wrapper.getComponent(Select).props('options').map((g: { value: number }) => g.value)).toEqual([1, 2])
    await wrapper.get('[data-testid="routing-add"]').trigger('click')
    const next = wrapper.emitted('update:modelValue')![0][0] as ApiKeyGroupRouting
    expect(next.routes.map(r => r.group_id)).toEqual([1, 2])
    expect(next.routes[0]).toEqual(policy().routes[0])
    await wrapper.setProps({ modelValue: next })
    expect(wrapper.get('[data-testid="routing-add"]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-testid="routing-platform"]').setValue('deepseek')
    expect(wrapper.findAllComponents(Select)[1].props('options').map((g: { value: number }) => g.value)).toEqual([2, 3])
  })

  it('shows explicit platform and user rates in the selected row and searchable dropdown', async () => {
    const wrapper = mount(KeyGroupRoutingEditor, { props: { modelValue: policy(), legacy: policy(), groups: protocolGroups }, global: { plugins: [createPinia()], stubs: { teleport: true, transition: false } } })
    expect(wrapper.get('[data-testid="routing-row"]').text()).toContain('0x')
    expect(wrapper.get('[data-testid="routing-row"]').text()).toContain('Anthropic')
    await wrapper.get('.select-trigger').trigger('click')
    const options = wrapper.findAll('[role="option"]')
    expect(options[1].text()).toContain('0.3x')
    expect(options[1].text()).toContain('OpenAI')
    await wrapper.get('.select-search-input').setValue('Same name')
    expect(wrapper.findAll('[role="option"]')).toHaveLength(2)
  })

  it('can filter legacy keys without converting them or losing unknown selected groups', async () => {
    const legacy = { ...policy(), routes: [{ ...policy().routes[0], group_id: 999 }] }
    const wrapper = mount(KeyGroupRoutingEditor, { props: { modelValue: null, legacy, groups: protocolGroups }, global: { stubs: { Select: true } } })
    await wrapper.get('[data-testid="routing-platform"]').setValue('openai')
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
    expect(wrapper.getComponent(Select).props('options')).toEqual(expect.arrayContaining([expect.objectContaining({ value: 999, label: '#999', disabled: true })]))
  })

  it('shows one list and preserves route settings across strategy changes', async () => {
    const wrapper = render()
    expect(wrapper.findAll('[data-testid="key-group-routing"]')).toHaveLength(1)
    expect(wrapper.find('[data-testid="key-additional-groups"]').exists()).toBe(false)
    await wrapper.get('[data-testid="routing-strategy"]').setValue('priority_weighted')
    const next = wrapper.emitted('update:modelValue')![0][0] as ApiKeyGroupRouting
    expect(next.strategy).toBe('priority_weighted')
    expect(next.routes).toEqual(policy().routes)
    await wrapper.setProps({ modelValue: next })
    await wrapper.get('[data-testid="routing-strategy"]').setValue('lowest_rate')
    expect((wrapper.emitted('update:modelValue')![1][0] as ApiKeyGroupRouting).routes).toEqual(next.routes)
  })

  it('does not convert legacy keys just by opening the editor', async () => {
    const wrapper = render(null)
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
    expect(wrapper.find('[data-testid="routing-add"]').exists()).toBe(false)
    expect(wrapper.get('input[type="checkbox"]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-testid="routing-strategy"]').setValue('lowest_rate')
    expect((wrapper.emitted('update:modelValue')![0][0] as ApiKeyGroupRouting).routes).toEqual(policy().routes)
  })

  it('adds unique candidates and updates only one policy', async () => {
    const initial = policy()
    const wrapper = render(initial)
    await wrapper.get('[data-testid="routing-add"]').trigger('click')
    const next = wrapper.emitted('update:modelValue')![0][0] as ApiKeyGroupRouting
    expect(next.routes.map(route => route.group_id)).toEqual([1, 2])
    expect(initial.routes).toHaveLength(1)
    await wrapper.setProps({ modelValue: next })
    expect(wrapper.get('[data-testid="routing-add"]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-testid="routing-failover"]').setValue(true)
    expect((wrapper.emitted('update:modelValue')![1][0] as ApiKeyGroupRouting).failover).toBe(true)
  })

  it('retains all legacy candidates for explicit conversion without mutating the key', () => {
    const key = { group_id: 1, group_ids: [1, 2], group_routes: [{ ...policy().routes[0], group_id: 3 }] } as ApiKey
    const preview = legacyGroupRouting(key)
    expect(preview.routes.map(route => route.group_id)).toEqual([3, 1, 2])
    expect(preview.strategy).toBe('priority_weighted')
    preview.routes[0].enabled = false
    expect(key.group_routes[0].enabled).toBe(true)
  })

  it('rejects duplicate groups and invalid numeric controls', () => {
    const p = policy()
    expect(validGroupRouting(p)).toBe(true)
    p.routes.push({ ...p.routes[0] })
    expect(validGroupRouting(p)).toBe(false)
    const bad = cloneGroupRouting(policy())
    bad.routes[0].weight = 0
    expect(validGroupRouting(bad)).toBe(false)
  })
})

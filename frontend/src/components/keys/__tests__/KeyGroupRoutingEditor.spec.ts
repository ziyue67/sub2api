import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import type { ApiKey, ApiKeyGroupRouting } from '@/types'
import { cloneGroupRouting, legacyGroupRouting, validGroupRouting } from '@/utils/keyGroupRouting'
import KeyGroupRoutingEditor from '../KeyGroupRoutingEditor.vue'

vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

const groups = [{ value: 1, label: 'First', rate: 1, userRate: null }, { value: 2, label: 'Second', rate: 0.5, userRate: null }]
const policy = (): ApiKeyGroupRouting => ({ strategy: 'lowest_rate', failover: false, routes: [{ group_id: 1, priority: 2, weight: 3, enabled: true, cooldown_seconds: 60 }] })
function render(value: ApiKeyGroupRouting | null = policy()) {
  return mount(KeyGroupRoutingEditor, { props: { modelValue: value, legacy: policy(), groups }, global: { stubs: { Select: true } } })
}

describe('unified group routing', () => {
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

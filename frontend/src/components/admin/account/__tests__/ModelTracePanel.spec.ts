import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import type { Account } from '@/types'
import type { ModelTraceResult } from '@/api/admin/modelTrace'
import ModelTracePanel from '../ModelTracePanel.vue'

const { runModelTrace } = vi.hoisted(() => ({ runModelTrace: vi.fn() }))
vi.mock('@/api/admin/modelTrace', () => ({ runModelTrace }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

const record: ModelTraceResult = {
  requested_model: 'public-model', reasoning_effort: '', status: 'partial', disagreement: false,
  created_at: '2026-10-18T12:00:00Z',
  bank: { source_commit: 'fixture', sha256: 'fixture', algorithm: 'fixture', built_at: '', models: ['gpt-6-astra'] },
  samples: [{ challenge: { id: 'sample1', expected_count: 300, prompt: 'mock question' }, text: 'mock numbers',
    environment: { protocol: 'responses', system_prompt: 'none', oauth: false }, parsed_numbers: 200, minimum_numbers: 165,
    accepted: true, mapped_model: 'gpt-6-astra', started_at: '2026-10-18T12:00:00Z', latency_ms: 1000 }],
  analysis: { prediction: 'gpt-6-astra', probability: 0.9, used_outputs: 1,
    candidates: [{ model: 'gpt-6-astra', family: 'gpt', probability: 0.9 }], families: [{ model: '', family: 'gpt', probability: 1 }] }
}

function mountPanel(account = {}) {
  return mount(ModelTracePanel, { props: {
    account: { id: 42, platform: 'openai', type: 'apikey', ...account } as Account,
    modelId: 'public-model', modelOptions: [], reasoningLevels: ['low', 'medium', 'high']
  }, global: { stubs: { TestModelSelect: true, Select: true } } })
}

beforeEach(() => { localStorage.clear(); runModelTrace.mockReset() })

describe('ModelTracePanel', () => {
  it('displays partial attribution and restores browser history without making a model request', async () => {
    runModelTrace.mockResolvedValue(record)
    const wrapper = mountPanel()
    await wrapper.get('[data-testid="modeltrace-start"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="modeltrace-result"]').text()).toContain('modelTrace.status.partial')
    expect(wrapper.text()).toContain('modelTrace.partialHint')
    expect(wrapper.text()).toContain('gpt-6-astra')
    expect(JSON.parse(localStorage.getItem('sub2api-modeltrace-test:42')!)).toEqual([record])
    wrapper.unmount()
    const restored = mountPanel()
    expect(restored.text()).toContain('modelTrace.history')
    expect(runModelTrace).toHaveBeenCalledTimes(1)
    restored.unmount()
  })

  it('aborts on stop and never saves a late completed result', async () => {
    let finish!: (record: ModelTraceResult) => void
    runModelTrace.mockImplementation(() => new Promise(resolve => { finish = resolve }))
    const wrapper = mountPanel()
    await wrapper.get('[data-testid="modeltrace-start"]').trigger('click')
    const signal = runModelTrace.mock.calls[0]![3] as AbortSignal
    await wrapper.get('[data-testid="modeltrace-cancel"]').trigger('click')
    expect(signal.aborted).toBe(true)
    finish(record)
    await flushPromises()
    expect(localStorage.getItem('sub2api-modeltrace-test:42')).toBeNull()
    expect(wrapper.find('[data-testid="modeltrace-result"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('aborts when the panel closes', async () => {
    runModelTrace.mockImplementation(() => new Promise(() => undefined))
    const wrapper = mountPanel()
    await wrapper.get('[data-testid="modeltrace-start"]').trigger('click')
    const signal = runModelTrace.mock.calls[0]![3] as AbortSignal
    wrapper.unmount()
    expect(signal.aborted).toBe(true)
  })

  it('warns about retained Claude OAuth prompts and disables unsupported platforms', () => {
    const claude = mountPanel({ platform: 'anthropic', type: 'oauth' })
    expect(claude.find('[data-testid="modeltrace-system-warning"]').exists()).toBe(true)
    claude.unmount()
    const unsupported = mountPanel({ platform: 'gemini' })
    expect((unsupported.get('[data-testid="modeltrace-start"]').element as HTMLButtonElement).disabled).toBe(true)
    unsupported.unmount()
  })
})

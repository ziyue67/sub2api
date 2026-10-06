import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import AstraGatewayHistory from '../AstraGatewayHistory.vue'
const mocks = vi.hoisted(() => ({ get: vi.fn() }))
vi.mock('@/api/admin/astraGateway', () => ({ getAstraGatewayHistory: mocks.get }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key, te: () => true }) }))
beforeEach(() => { vi.useFakeTimers(); vi.clearAllMocks(); mocks.get.mockResolvedValue({ items: [{ gateway: 'chat.gateway.unified-196.api.openai.com', source_account_id: 299, target_account_id: 300, passes: 1, failures: 2, last_answer: '29', last_reason: 'target_candy_failed', last_pass: '2026-09-23T20:00:00Z' }], total: 21, unique_gateways: 3 }) })
afterEach(() => vi.useRealTimers())
describe('persistent gateway history', () => {
  it('defaults to target pass history, searches host and paginates', async () => {
    const w = mount(AstraGatewayHistory); await flushPromises()
    expect(mocks.get).toHaveBeenCalledWith('', true, 1)
    expect(w.text()).toContain('unified-196'); expect(w.text()).toContain('#300'); expect(w.text()).toContain('29')
    await w.get('input[type="checkbox"]').setValue(false); await flushPromises()
    await w.get('input[maxlength]').setValue('unified-196'); await w.get('form').trigger('submit'); await flushPromises()
    expect(mocks.get).toHaveBeenLastCalledWith('unified-196', false, 1)
    await w.findAll('button').at(-1)!.trigger('click'); await flushPromises()
    expect(mocks.get).toHaveBeenLastCalledWith('unified-196', false, 2)
    w.unmount(); const count = mocks.get.mock.calls.length; await vi.advanceTimersByTimeAsync(15000); expect(mocks.get).toHaveBeenCalledTimes(count)
  })
  it('reports load failures instead of claiming an empty history', async () => {
    mocks.get.mockRejectedValue(new Error('offline'))
    const w = mount(AstraGatewayHistory); await flushPromises(); expect(w.find('[role="alert"]').exists()).toBe(true); w.unmount()
  })
})

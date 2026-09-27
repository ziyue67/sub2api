import { mount, flushPromises, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import MihomoSettings from './MihomoSettings.vue'
const { get, post, put } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: { get, post, put } }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ locale: { value: 'zh' }, t: (key: string) => key }) }))
const base = { installed: true, supported: true, running: true, busy: false, nodes: 1, subscriptions: 1, phase: 'running', endpoint: 'http://127.0.0.1:3101', dynamic_proxies: 1, subscription_items: [{ id: 'source-one', label: '机场 A', enabled: true, cached: true, nodes: 1 }], node_states: [{ name: 'node-one', display_name: '东京节点', state: 'enabled', subscription_ids: ['source-one'], country_code: 'JP' }] }
let wrapper: VueWrapper
const mountPanel = (section: 'subscriptions' | 'dynamic' | 'nodes' | 'kernel' = 'subscriptions') => mount(MihomoSettings, {
  props: { section },
  global: { stubs: {
    BaseDialog: { props: ['show'], emits: ['close'], template: '<div v-if="show" role="dialog"><button aria-label="Close modal" @click="$emit(\'close\')">关闭</button><slot /><slot name="footer" /></div>' },
    ConfirmDialog: { props: ['show', 'message'], emits: ['confirm', 'cancel'], template: '<div v-if="show" data-test="confirm"><p>{{ message }}</p><button @click="$emit(\'confirm\')">确认</button><button @click="$emit(\'cancel\')">取消</button></div>' },
    DataTable: { props: ['columns', 'data', 'selectedKeys'], emits: ['update:selectedKeys'], template: '<div data-test="table"><button v-if="selectedKeys" @click="$emit(\'update:selectedKeys\', data.map(row => row.id))">全选</button><div v-for="row in data" :key="row.id || row.name" data-test="row"><template v-for="column in columns" :key="column.key"><slot :name="\'cell-\' + column.key" :row="row" :value="row[column.key]">{{ row[column.key] }}</slot></template></div><slot v-if="!data.length" name="empty" /></div>' }
  } }
})
async function click(label: string) {
  const button = wrapper.findAll('button').find(b => b.text() === label)
  expect(button, label).toBeDefined()
  await button!.trigger('click'); await flushPromises()
}
beforeEach(() => { vi.resetAllMocks(); get.mockResolvedValue({ data: base }); post.mockResolvedValue({ data: base }); put.mockResolvedValue({ data: { ...base, subscription_download_mode: 'proxy' } }) })
afterEach(() => { wrapper?.unmount(); vi.useRealTimers() })
describe('Mihomo IP management', () => {
  it('loads a redacted subscription list without mutating configuration', async () => {
    wrapper = mountPanel(); await flushPromises()
    expect(wrapper.text()).toContain('机场 A'); expect(wrapper.text()).toContain('source-o')
    expect(wrapper.find('#mihomo-subscriptions').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('设为打票代理'); expect(post).not.toHaveBeenCalled()
  })
  it('adds subscriptions without replacing existing sources and clears a successful draft', async () => {
    wrapper = mountPanel(); await flushPromises(); await click('添加订阅')
    await wrapper.get('#mihomo-subscriptions').setValue('https://example.org/a?token=secret\nhttps://example.org/b')
    await click('保存并应用')
    expect(post).toHaveBeenCalledWith('/admin/system/mihomo', expect.objectContaining({ action: 'subscription_add', subscriptions: ['https://example.org/a?token=secret', 'https://example.org/b'], dynamic_proxies: [] }))
    expect(wrapper.find('[role="dialog"]').exists()).toBe(false)
  })
  it('renames without revealing or resending a saved URL', async () => {
    wrapper = mountPanel(); await flushPromises(); await click('编辑')
    expect(wrapper.get<HTMLTextAreaElement>('#mihomo-subscriptions').element.value).toBe('')
    await wrapper.get('[data-testid="subscription-name"]').setValue('新名称'); await click('保存并应用')
    expect(post).toHaveBeenCalledWith('/admin/system/mihomo', expect.objectContaining({ action: 'subscription_rename/source-one', name: '新名称', subscriptions: [] }))
  })
  it('updates only the selected subscription URL', async () => {
    wrapper = mountPanel(); await flushPromises(); await click('编辑')
    await wrapper.get('#mihomo-subscriptions').setValue('https://example.org/replacement'); await click('保存并应用')
    expect(post).toHaveBeenCalledWith('/admin/system/mihomo', expect.objectContaining({ action: 'subscription_update/source-one', subscriptions: ['https://example.org/replacement'] }))
  })
  it('confirms source removal and leaves configuration unchanged on cancel', async () => {
    wrapper = mountPanel(); await flushPromises(); await click('移除')
    expect(post).not.toHaveBeenCalled(); expect(wrapper.text()).toContain('其他来源会保留')
    await click('取消'); expect(post).not.toHaveBeenCalled()
    await click('移除'); await click('确认')
    expect(post).toHaveBeenCalledWith('/admin/system/mihomo', expect.objectContaining({ action: 'subscription_remove/source-one', subscriptions: [] }))
  })
  it('shows batch actions only for selected subscriptions', async () => {
    wrapper = mountPanel(); await flushPromises()
    expect(wrapper.text()).not.toContain('更新所选'); await click('全选'); await click('停用所选')
    expect(post).toHaveBeenCalledWith('/admin/system/mihomo', expect.objectContaining({ action: 'subscription_disable/source-one' }))
  })
  it('waits for asynchronous failure and preserves the draft', async () => {
    vi.useFakeTimers()
    wrapper = mountPanel(); await flushPromises(); await click('添加订阅')
    await wrapper.get('#mihomo-subscriptions').setValue('https://example.org/new')
    post.mockResolvedValue({ data: { ...base, busy: true } })
    get.mockResolvedValue({ data: { ...base, error: 'subscription must contain Clash/Mihomo YAML proxies' } })
    await click('保存并应用')
    expect(wrapper.text()).not.toContain('变更已应用')
    await vi.advanceTimersByTimeAsync(1500); await flushPromises()
    expect(wrapper.get<HTMLTextAreaElement>('#mihomo-subscriptions').element.value).toBe('https://example.org/new')
    expect(wrapper.text()).toContain('subscription must contain Clash/Mihomo YAML proxies')
  })
  it('appends dynamic proxies with the chosen protocol without deleting subscriptions', async () => {
    wrapper = mountPanel('dynamic'); await flushPromises(); await click('导入动态代理')
    await wrapper.get('#mihomo-dynamic-protocol').setValue('socks5')
    await wrapper.get('#mihomo-dynamic-proxies').setValue('proxy.example:2000:user:pass\nhttps://user:pass@proxy.example:2001')
    await click('应用动态代理')
    expect(post).toHaveBeenCalledWith('/admin/system/mihomo', expect.objectContaining({ action: 'dynamic_append', subscriptions: [], dynamic_proxies: ['socks5://proxy.example:2000:user:pass', 'https://user:pass@proxy.example:2001'] }))
  })
  it('requires confirmation for replacing dynamic proxies', async () => {
    wrapper = mountPanel('dynamic'); await flushPromises(); await click('导入动态代理')
    await wrapper.get('#mihomo-dynamic-proxies').setValue('user:pass@proxy.example:2000')
    await wrapper.get('[role="dialog"] input[type="checkbox"]').setValue(true); await click('应用动态代理')
    expect(post).not.toHaveBeenCalled(); await click('确认')
    expect(post).toHaveBeenCalledWith('/admin/system/mihomo', expect.objectContaining({ action: 'dynamic_replace' }))
  })
  it('keeps failed dynamic imports editable', async () => {
    post.mockRejectedValue({ message: 'invalid proxy format' })
    wrapper = mountPanel('dynamic'); await flushPromises(); await click('导入动态代理')
    await wrapper.get('#mihomo-dynamic-proxies').setValue('invalid'); await click('应用动态代理')
    expect(wrapper.get<HTMLTextAreaElement>('#mihomo-dynamic-proxies').element.value).toBe('invalid'); expect(wrapper.text()).toContain('invalid proxy format')
  })
  it('paginates large pools and filters by source and status', async () => {
    get.mockResolvedValue({ data: { ...base, node_states: Array.from({ length: 1024 }, (_, i) => ({ name: 'node-' + i, display_name: '节点 ' + i, state: i === 900 ? 'failed' : 'enabled', dynamic: i >= 512, subscription_ids: i < 512 ? ['source-one'] : [] })) } })
    wrapper = mountPanel('nodes'); await flushPromises()
    expect(wrapper.findAll('[data-test="row"]')).toHaveLength(50); expect(wrapper.text()).toContain('1024')
    await click('下一页'); expect(wrapper.text()).toContain('节点 50')
    await wrapper.get('select[aria-label="节点来源"]').setValue('source-one'); expect(wrapper.text()).toContain('512')
    await wrapper.get('select[aria-label="节点来源"]').setValue('dynamic'); await wrapper.get('select[aria-label="节点状态"]').setValue('failed')
    expect(wrapper.findAll('[data-test="row"]')).toHaveLength(1); expect(wrapper.text()).toContain('节点 900')
  })
  it('uses stable node IDs and isolates country rule updates', async () => {
    wrapper = mountPanel('nodes'); await flushPromises(); await click('停用')
    expect(post).toHaveBeenCalledWith('/admin/system/mihomo', expect.objectContaining({ action: 'disable/node-one' }))
    await wrapper.setProps({ section: 'kernel' }); await click('保存地区规则')
    expect(post).toHaveBeenLastCalledWith('/admin/system/mihomo', expect.objectContaining({ action: 'country_filter', subscriptions: [], dynamic_proxies: [] }))
  })
  it('displays warm pool status only when returned by the backend', async () => {
    wrapper = mountPanel(); await flushPromises(); expect(wrapper.find('[data-testid="bps-warm-pool"]').exists()).toBe(false)
    get.mockResolvedValue({ data: { ...base, bps_warm_pool: { ready: 0, target: 0, checking: 0, cooling: 0 }, bps_ip_warm_pool: { ready: 2, target: 0, checking: 0, cooling: 0 } } })
    await click('检测状态'); expect(wrapper.findAll('[data-testid="bps-warm-pool"]')).toHaveLength(2)
  })
  it('loads and saves the mode through the existing dedicated backend endpoint', async () => {
    get.mockResolvedValue({ data: { ...base, subscription_download_mode: 'direct' } })
    wrapper = mountPanel(); await flushPromises()
    expect(wrapper.get<HTMLSelectElement>('#subscription-download-mode').element.value).toBe('direct')
    await wrapper.get('#subscription-download-mode').setValue('proxy')
    await click('保存下载模式')
    expect(put).toHaveBeenCalledWith('/admin/system/mihomo/download-mode', { mode: 'proxy' })
    expect(post).not.toHaveBeenCalled()
  })
  it('keeps an unsaved download mode while polling status', async () => {
    wrapper = mountPanel(); await flushPromises()
    await wrapper.get('#subscription-download-mode').setValue('proxy')
    get.mockResolvedValue({ data: { ...base, subscription_download_mode: 'direct' } })
    await click('检测状态')
    expect(wrapper.get<HTMLSelectElement>('#subscription-download-mode').element.value).toBe('proxy')
    put.mockRejectedValue({ message: 'cannot save subscription settings' })
    await click('保存下载模式')
    expect(wrapper.get<HTMLSelectElement>('#subscription-download-mode').element.value).toBe('proxy')
    expect(wrapper.text()).toContain('cannot save subscription settings')
  })
  it('renders source readiness from backend counters without client-side inference', async () => {
    get.mockResolvedValue({ data: { ...base, bps_warm_pool: { ready: 7, target: 0, checking: 0, cooling: 1, ready_subscription: 5, ready_dynamic: 2 } } })
    wrapper = mountPanel(); await flushPromises()
    const pool = wrapper.get('[data-testid="bps-warm-pool"]')
    expect(pool.text()).toContain('订阅就绪 5')
    expect(pool.text()).toContain('动态就绪 2')
  })
  it.each(['subscriptions', 'dynamic'] as const)('closes %s after polling fails while backend status remains busy', async section => {
    vi.useFakeTimers()
    wrapper = mountPanel(section); await flushPromises()
    await click(section === 'subscriptions' ? '添加订阅' : '导入动态代理')
    const input = section === 'subscriptions' ? '#mihomo-subscriptions' : '#mihomo-dynamic-proxies'
    await wrapper.get(input).setValue(section === 'subscriptions' ? 'https://example.org/test-subscription' : 'user:pass@localhost:1080')
    post.mockResolvedValue({ data: { ...base, busy: true } })
    get.mockRejectedValue(new Error('polling unavailable'))
    await click(section === 'subscriptions' ? '保存并应用' : '应用动态代理')
    await vi.advanceTimersByTimeAsync(1500); await flushPromises()
    expect(wrapper.text()).toContain('polling unavailable')
    await wrapper.get('[aria-label="Close modal"]').trigger('click')
    expect(wrapper.find('[role="dialog"]').exists()).toBe(false)
    expect(post).toHaveBeenCalledTimes(1)
  })
  it('closes the subscription dialog while its submitted operation is still pending', async () => {
    let complete!: (value: unknown) => void
    post.mockImplementation(() => new Promise(resolve => { complete = resolve }))
    wrapper = mountPanel(); await flushPromises(); await click('添加订阅')
    await wrapper.get('#mihomo-subscriptions').setValue('https://example.org/test-subscription')
    await click('保存并应用')
    await wrapper.get('[aria-label="Close modal"]').trigger('click')
    expect(wrapper.find('[role="dialog"]').exists()).toBe(false)
    complete({ data: base }); await flushPromises()
    expect(post).toHaveBeenCalledTimes(1)
  })
  it('does not offer installation when status loading fails', async () => {
    get.mockRejectedValue(new Error('network'))
    wrapper = mountPanel('kernel'); await flushPromises(); expect(wrapper.text()).toContain('无法读取内核状态'); expect(wrapper.text()).not.toContain('检测并安装')
  })
})

it('shows warm target and shortage reuse without probing from the UI', async () => {
  get.mockResolvedValue({data:{...base,bps_warm_pool:{target:8,ready:2,checking:1,cooling:3,failure_reasons:{bps_access_denied:3}}}})
  const wrapper=mount(MihomoSettings)
  try {
    await flushPromises()
    const pool=wrapper.get('[data-testid="bps-warm-pool"]')
    expect(pool.text()).toContain('目标 8')
    expect(pool.text()).toContain('就绪 2')
    expect(pool.text()).toContain('不足时复用就绪出口')
    expect(pool.text()).toContain('bps_access_denied: 3')
  } finally { wrapper.unmount() }
})

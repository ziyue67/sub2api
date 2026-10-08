import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import TicketMessageBody from '../TicketMessageBody.vue'
import TicketConversation from '../TicketConversation.vue'
import TicketReplyBox from '../TicketReplyBox.vue'
import CreateTicketDialog from '../CreateTicketDialog.vue'
import type { SupportTicketMessage, SupportTicketUserSummary } from '@/api/supportTickets'

const mocks = vi.hoisted(() => ({ createTicket: vi.fn(), success: vi.fn(), error: vi.fn() }))
vi.mock('vue-i18n', async () => ({
  ...(await vi.importActual<typeof import('vue-i18n')>('vue-i18n')),
  useI18n: () => ({ t: (key: string, params?: Record<string, unknown>) => (params ? `${key}:${JSON.stringify(params)}` : key) }),
}))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess: mocks.success, showError: mocks.error }) }))
vi.mock('@/api/supportTickets', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/api/supportTickets')>()),
  createTicket: mocks.createTicket,
}))

const DialogStub = { props: ['show', 'title'], template: '<div v-if="show"><slot /><slot name="footer" /></div>' }
const SelectStub = {
  props: ['modelValue', 'options'],
  emits: ['update:modelValue'],
  template: '<div><button v-for="o in options" :key="o.value" type="button" :data-value="o.value" @click="$emit(\'update:modelValue\', o.value)">{{ o.label }}</button></div>',
}
const stubs = { BaseDialog: DialogStub, Select: SelectStub }

const message = (id: number, role: 'user' | 'admin', extra: Partial<SupportTicketMessage> = {}): SupportTicketMessage => ({
  id, author_role: role, body: `message ${id}`, created_at: '2026-10-07T06:00:00Z', ...extra,
})

beforeEach(() => {
  vi.clearAllMocks()
})

describe('ticket message body', () => {
  it('shows HTML in a message as text and only links http(s)', () => {
    const wrapper = mount(TicketMessageBody, { props: { text: '<img src=x onerror=alert(1)> see https://img.test/a.png\nnext line javascript:alert(1)' } })
    expect(wrapper.find('img').exists()).toBe(false)
    expect(wrapper.text()).toContain('<img src=x onerror=alert(1)>')
    const links = wrapper.findAll('[data-testid="ticket-link"]')
    expect(links).toHaveLength(1)
    expect(links[0].attributes()).toMatchObject({ href: 'https://img.test/a.png', target: '_blank', rel: 'noopener noreferrer nofollow' })
    expect(wrapper.get('p').classes()).toContain('whitespace-pre-wrap')
  })
})

describe('ticket conversation', () => {
  const messages = [message(1, 'user'), message(2, 'admin', { author_name: 'root' })]

  it('names the sides from the user point of view', () => {
    const wrapper = mount(TicketConversation, { props: { messages, viewer: 'user' } })
    const rows = wrapper.findAll('[data-testid="ticket-message"]')
    expect(rows.map((row) => row.get('[data-testid="ticket-message-author"]').text())).toEqual(['supportTickets.detail.you', 'supportTickets.detail.staff'])
    expect(rows[0].classes()).toContain('justify-end')
    expect(rows[1].classes()).toContain('justify-start')
  })

  it('shows admins which admin answered', () => {
    const wrapper = mount(TicketConversation, { props: { messages: [...messages, message(3, 'admin')], viewer: 'admin' } })
    const rows = wrapper.findAll('[data-testid="ticket-message"]')
    expect(rows.map((row) => row.get('[data-testid="ticket-message-author"]').text())).toEqual(['supportTickets.detail.user', 'root', 'supportTickets.detail.staff'])
    expect(rows[1].classes()).toContain('justify-end')
  })
})

describe('ticket reply box', () => {
  it('sends the trimmed draft and refuses empty or too long text', async () => {
    const wrapper = mount(TicketReplyBox, { props: { modelValue: '', placeholder: 'p', 'onUpdate:modelValue': (value: string) => wrapper.setProps({ modelValue: value }) } })
    const send = wrapper.get('[data-testid="ticket-reply-send"]')
    expect(send.attributes('disabled')).toBeDefined()
    await wrapper.get('textarea').setValue('  fixed?  ')
    expect(send.attributes('disabled')).toBeUndefined()
    await wrapper.get('form').trigger('submit')
    expect(wrapper.emitted('submit')).toEqual([['fixed?']])

    await wrapper.get('textarea').trigger('keydown', { key: 'Enter', ctrlKey: true })
    expect(wrapper.emitted('submit')).toHaveLength(2)

    await wrapper.get('textarea').setValue('字'.repeat(5001))
    expect(wrapper.get('[data-testid="ticket-reply-counter"]').text()).toContain('"count":5001')
    expect(wrapper.get('[data-testid="ticket-reply-counter"]').classes()).toContain('text-red-600')
    expect(send.attributes('disabled')).toBeDefined()

    await wrapper.setProps({ modelValue: 'ok', sending: true })
    expect(send.text()).toBe('supportTickets.detail.sending')
    expect(send.attributes('disabled')).toBeDefined()
  })
})

describe('create ticket dialog', () => {
  const summary = (extra: Partial<SupportTicketUserSummary> = {}): SupportTicketUserSummary => ({
    unread_count: 0, open_count: 1, max_open: 5, categories: ['账户与充值', '其他'], notice: '工作时间 9–21 点', ...extra,
  })

  it('needs a category, title and body, then submits trimmed values', async () => {
    const detail = { ticket: { id: 9 }, messages: [] }
    mocks.createTicket.mockResolvedValue(detail)
    const wrapper = mount(CreateTicketDialog, { props: { show: false, summary: summary() }, global: { stubs } })
    await wrapper.setProps({ show: true })
    expect(wrapper.get('[data-testid="ticket-notice"]').text()).toBe('工作时间 9–21 点')
    const submit = wrapper.get('[data-testid="ticket-submit"]')
    await wrapper.get('[data-testid="ticket-title"]').setValue('  401 报错 ')
    await wrapper.get('[data-testid="ticket-body"]').setValue(' key 用不了 ')
    expect(submit.attributes('disabled')).toBeDefined()
    await wrapper.get('[data-value="其他"]').trigger('click')
    expect(submit.attributes('disabled')).toBeUndefined()
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(mocks.createTicket).toHaveBeenCalledWith({ category: '其他', title: '401 报错', body: 'key 用不了' })
    expect(mocks.success).toHaveBeenCalledWith('supportTickets.form.created')
    expect(wrapper.emitted('created')).toEqual([[detail]])
  })

  it('skips the category without categories and blocks at the open limit', async () => {
    mocks.createTicket.mockResolvedValue({ ticket: { id: 1 }, messages: [] })
    const wrapper = mount(CreateTicketDialog, { props: { show: true, summary: summary({ categories: [], notice: '' }) }, global: { stubs } })
    expect(wrapper.find('[data-testid="ticket-category"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="ticket-notice"]').exists()).toBe(false)
    await wrapper.get('[data-testid="ticket-title"]').setValue('t')
    await wrapper.get('[data-testid="ticket-body"]').setValue('b')
    expect(wrapper.get('[data-testid="ticket-submit"]').attributes('disabled')).toBeUndefined()

    await wrapper.setProps({ summary: summary({ categories: [], open_count: 5 }) })
    expect(wrapper.get('[data-testid="ticket-open-limit"]').text()).toContain('"max":5')
    expect(wrapper.get('[data-testid="ticket-submit"]').attributes('disabled')).toBeDefined()
  })

  it('shows the server reason when submitting fails', async () => {
    mocks.createTicket.mockRejectedValue({ reason: 'SUPPORT_TICKET_TOO_FAST', metadata: { max: '5' }, message: 'fast' })
    const wrapper = mount(CreateTicketDialog, { props: { show: true, summary: summary({ categories: [] }) }, global: { stubs } })
    await wrapper.get('[data-testid="ticket-title"]').setValue('t')
    await wrapper.get('[data-testid="ticket-body"]').setValue('b')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(mocks.error).toHaveBeenCalledWith('supportTickets.errors.SUPPORT_TICKET_TOO_FAST:{"max":"5"}')
    expect(wrapper.emitted('created')).toBeUndefined()
  })
})

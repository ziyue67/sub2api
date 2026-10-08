import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import SupportTicketsView from '../SupportTicketsView.vue'
import SupportTicketDetailView from '../SupportTicketDetailView.vue'
import type { SupportTicket, SupportTicketDetail } from '@/api/supportTickets'

const mocks = vi.hoisted(() => ({
  listTickets: vi.fn(), getAdminSummary: vi.fn(), getTicket: vi.fn(), replyTicket: vi.fn(), setTicketStatus: vi.fn(), deleteTicket: vi.fn(),
  push: vi.fn(), route: { params: { id: '7' } }, success: vi.fn(), error: vi.fn(), setAdminPending: vi.fn(), refresh: vi.fn(),
}))
vi.mock('vue-i18n', async () => ({
  ...(await vi.importActual<typeof import('vue-i18n')>('vue-i18n')),
  useI18n: () => ({ t: (key: string, params?: Record<string, unknown>) => (params ? `${key}:${JSON.stringify(params)}` : key) }),
}))
vi.mock('vue-router', async () => ({
  ...(await vi.importActual<typeof import('vue-router')>('vue-router')),
  useRouter: () => ({ push: mocks.push }),
  useRoute: () => mocks.route,
}))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess: mocks.success, showError: mocks.error }) }))
vi.mock('@/stores/supportTickets', () => ({ useSupportTicketStore: () => ({ setAdminPending: mocks.setAdminPending, refresh: mocks.refresh }) }))
vi.mock('@/api/supportTickets', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/api/supportTickets')>()),
  listTickets: mocks.listTickets, getAdminSummary: mocks.getAdminSummary, getTicket: mocks.getTicket,
  replyTicket: mocks.replyTicket, setTicketStatus: mocks.setTicketStatus, deleteTicket: mocks.deleteTicket,
}))

const SelectStub = {
  props: ['modelValue', 'options'],
  emits: ['update:modelValue'],
  template: '<div><button v-for="o in options" :key="String(o.value)" type="button" :data-value="String(o.value)" @click="$emit(\'update:modelValue\', o.value)">{{ o.label }}</button></div>',
}
const DataTableStub = {
  props: ['columns', 'data', 'loading'],
  emits: ['rowClick'],
  template: `<table><tr v-for="row in data" :key="row.id" data-testid="row" @click="$emit('rowClick', row)">
    <td v-for="column in columns" :key="column.key"><slot :name="'cell-' + column.key" :row="row" :value="row[column.key]">{{ row[column.key] }}</slot></td>
  </tr><tr v-if="!data.length"><td><slot name="empty" /></td></tr></table>`,
}
const stubs = {
  AppLayout: { template: '<div><slot /></div>' },
  TablePageLayout: { template: '<div><slot name="filters" /><slot name="table" /><slot name="pagination" /></div>' },
  DataTable: DataTableStub,
  Select: SelectStub,
  RouterLink: { props: ['to'], template: '<a :href="to"><slot /></a>' },
  ConfirmDialog: {
    props: ['show', 'message'],
    emits: ['confirm', 'cancel'],
    template: '<div v-if="show" data-testid="confirm"><span>{{ message }}</span><button type="button" data-testid="confirm-yes" @click="$emit(\'confirm\')">ok</button></div>',
  },
  Pagination: true,
  EmptyState: { props: ['title'], template: '<p>{{ title }}</p>' },
  Icon: true,
}

const ticket = (id: number, extra: Partial<SupportTicket> = {}): SupportTicket => ({
  id, user_id: 42, category: '其他', title: `ticket ${id}`, status: 'pending', message_count: 2, last_message_at: '2026-10-07T06:00:00Z',
  last_message_role: 'user', user_unread: false, admin_unread: false, created_at: '2026-10-07T05:00:00Z', updated_at: '2026-10-07T06:00:00Z',
  user: { id: 42, email: 'alice@example.com', username: 'alice', status: 'active', balance: 12.5, deleted: false, created_at: '2026-09-01T00:00:00Z' },
  ...extra,
})
const detail = (extra: Partial<SupportTicket> = {}): SupportTicketDetail => ({
  ticket: ticket(7, extra),
  messages: [
    { id: 1, author_role: 'user', body: 'help', created_at: '2026-10-07T05:00:00Z' },
    { id: 2, author_role: 'admin', author_name: 'root', body: 'on it', created_at: '2026-10-07T05:30:00Z' },
  ],
})

beforeEach(() => {
  vi.clearAllMocks()
  mocks.getAdminSummary.mockResolvedValue({ pending_count: 4, categories: ['账户与充值', '其他'] })
  mocks.listTickets.mockResolvedValue({ items: [ticket(3, { admin_unread: true }), ticket(2, { status: 'replied', last_message_role: 'admin' })], total: 2, page: 1, page_size: 20, pages: 1 })
  mocks.getTicket.mockResolvedValue(detail())
})

afterEach(() => {
  vi.useRealTimers()
})

describe('admin ticket list', () => {
  it('starts with unclosed tickets and filters by status, category and keyword', async () => {
    vi.useFakeTimers()
    const wrapper = mount(SupportTicketsView, { global: { stubs } })
    await flushPromises()
    expect(mocks.listTickets).toHaveBeenCalledWith({ status: 'open', category: undefined, keyword: undefined, page: 1, page_size: 20 })
    expect(mocks.setAdminPending).toHaveBeenCalledWith(4)
    const rows = wrapper.findAll('[data-testid="row"]')
    expect(rows).toHaveLength(2)
    expect(rows[0].find('[data-testid="ticket-unread"]').exists()).toBe(true)
    expect(rows[0].text()).toContain('alice@example.com')
    expect(rows[1].text()).toContain('supportTickets.admin.lastFromAdmin')

    await wrapper.get('[data-testid="ticket-status-filter"] [data-value="closed"]').trigger('click')
    await flushPromises()
    expect(mocks.listTickets).toHaveBeenLastCalledWith(expect.objectContaining({ status: 'closed', page: 1 }))
    await wrapper.get('[data-testid="ticket-category-filter"] [data-value="其他"]').trigger('click')
    await flushPromises()
    expect(mocks.listTickets).toHaveBeenLastCalledWith(expect.objectContaining({ category: '其他' }))

    const calls = mocks.listTickets.mock.calls.length
    await wrapper.get('[data-testid="ticket-keyword"]').setValue(' alice ')
    expect(mocks.listTickets).toHaveBeenCalledTimes(calls)
    await vi.advanceTimersByTimeAsync(300)
    expect(mocks.listTickets).toHaveBeenLastCalledWith(expect.objectContaining({ keyword: 'alice' }))

    await rows[1].trigger('click')
    expect(mocks.push).toHaveBeenCalledWith('/admin/support-tickets/2')
  })
})

describe('admin ticket detail', () => {
  it('shows the author and answers with the chosen status', async () => {
    mocks.replyTicket.mockResolvedValue(detail({ status: 'processing' }))
    const wrapper = mount(SupportTicketDetailView, { global: { stubs } })
    await flushPromises()
    expect(mocks.getTicket).toHaveBeenCalledWith(7)
    const user = wrapper.get('[data-testid="ticket-user"]')
    expect(user.text()).toContain('alice@example.com')
    expect(user.text()).toContain('alice')
    expect(user.text()).toContain('$12.50')
    expect(wrapper.findAll('[data-testid="ticket-message-author"]').map((item) => item.text())).toEqual(['supportTickets.detail.user', 'root'])

    await wrapper.get('[data-testid="ticket-reply-status"] [data-value="processing"]').trigger('click')
    await wrapper.get('[data-testid="ticket-reply-body"]').setValue('checking')
    await wrapper.get('[data-testid="ticket-reply"]').trigger('submit')
    await flushPromises()
    expect(mocks.replyTicket).toHaveBeenCalledWith(7, 'checking', 'processing')
    expect(mocks.refresh).toHaveBeenLastCalledWith(true)
    expect(wrapper.find('[data-testid="ticket-mark-processing"]').exists()).toBe(false)
  })

  it('marks processing, closes, reopens and deletes', async () => {
    mocks.setTicketStatus.mockImplementation(async (_id: number, status: string) => detail({ status: status as SupportTicket['status'], closed_by_role: 'admin' }))
    mocks.deleteTicket.mockResolvedValue(undefined)
    const wrapper = mount(SupportTicketDetailView, { global: { stubs } })
    await flushPromises()

    await wrapper.get('[data-testid="ticket-mark-processing"]').trigger('click')
    await flushPromises()
    expect(mocks.setTicketStatus).toHaveBeenLastCalledWith(7, 'processing')
    await wrapper.get('[data-testid="ticket-close"]').trigger('click')
    await flushPromises()
    expect(mocks.setTicketStatus).toHaveBeenLastCalledWith(7, 'closed')
    expect(wrapper.find('[data-testid="ticket-reply"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="ticket-closed"]').text()).toContain('supportTickets.admin.closedByAdmin')
    await wrapper.get('[data-testid="ticket-reopen"]').trigger('click')
    await flushPromises()
    expect(mocks.setTicketStatus).toHaveBeenLastCalledWith(7, 'pending')

    await wrapper.get('[data-testid="ticket-delete"]').trigger('click')
    expect(wrapper.get('[data-testid="confirm"]').text()).toContain('"id":7')
    await wrapper.get('[data-testid="confirm-yes"]').trigger('click')
    await flushPromises()
    expect(mocks.deleteTicket).toHaveBeenCalledWith(7)
    expect(mocks.push).toHaveBeenCalledWith('/admin/support-tickets')
  })

  it('flags a deleted author', async () => {
    mocks.getTicket.mockResolvedValue(detail({ user: { ...ticket(7).user!, deleted: true } }))
    const wrapper = mount(SupportTicketDetailView, { global: { stubs } })
    await flushPromises()
    expect(wrapper.get('[data-testid="ticket-user"]').text()).toContain('supportTickets.admin.deletedUser')
  })
})

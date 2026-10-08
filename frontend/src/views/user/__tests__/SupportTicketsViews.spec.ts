import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import SupportTicketsView from '../SupportTicketsView.vue'
import SupportTicketDetailView from '../SupportTicketDetailView.vue'
import type { SupportTicket, SupportTicketDetail } from '@/api/supportTickets'

const mocks = vi.hoisted(() => ({
  listMyTickets: vi.fn(), getMySummary: vi.fn(), getMyTicket: vi.fn(), replyMyTicket: vi.fn(), closeMyTicket: vi.fn(), reopenMyTicket: vi.fn(),
  push: vi.fn(), route: { params: { id: '7' } }, success: vi.fn(), error: vi.fn(), setUserUnread: vi.fn(), refresh: vi.fn(),
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
vi.mock('@/stores/supportTickets', () => ({ useSupportTicketStore: () => ({ setUserUnread: mocks.setUserUnread, refresh: mocks.refresh }) }))
vi.mock('@/api/supportTickets', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/api/supportTickets')>()),
  listMyTickets: mocks.listMyTickets, getMySummary: mocks.getMySummary, getMyTicket: mocks.getMyTicket,
  replyMyTicket: mocks.replyMyTicket, closeMyTicket: mocks.closeMyTicket, reopenMyTicket: mocks.reopenMyTicket,
}))

const stubs = {
  AppLayout: { template: '<div><slot /></div>' },
  RouterLink: { props: ['to'], template: '<a :href="to"><slot /></a>' },
  CreateTicketDialog: { props: ['show', 'summary'], emits: ['close', 'created'], template: '<div v-if="show" data-testid="create-dialog" />' },
  ConfirmDialog: {
    props: ['show', 'message'],
    emits: ['confirm', 'cancel'],
    template: '<div v-if="show" data-testid="confirm"><button type="button" data-testid="confirm-yes" @click="$emit(\'confirm\')">ok</button></div>',
  },
  Pagination: true,
  EmptyState: { props: ['title'], template: '<p>{{ title }}</p>' },
  Icon: true,
}

const ticket = (id: number, extra: Partial<SupportTicket> = {}): SupportTicket => ({
  id, user_id: 42, category: '其他', title: `ticket ${id}`, status: 'pending', message_count: 1, last_message_at: '2026-10-07T06:00:00Z',
  last_message_role: 'user', user_unread: false, admin_unread: true, created_at: '2026-10-07T05:00:00Z', updated_at: '2026-10-07T06:00:00Z', ...extra,
})
const detail = (extra: Partial<SupportTicket> = {}): SupportTicketDetail => ({
  ticket: ticket(7, extra),
  messages: [
    { id: 1, author_role: 'user', body: 'help', created_at: '2026-10-07T05:00:00Z' },
    { id: 2, author_role: 'admin', body: 'on it', created_at: '2026-10-07T05:30:00Z' },
  ],
})

beforeEach(() => {
  vi.clearAllMocks()
  mocks.getMySummary.mockResolvedValue({ unread_count: 2, open_count: 1, max_open: 5, categories: ['其他'], notice: '' })
  mocks.listMyTickets.mockResolvedValue({ items: [ticket(3, { user_unread: true, status: 'replied' }), ticket(2)], total: 2, page: 1, page_size: 20, pages: 1 })
  mocks.getMyTicket.mockResolvedValue(detail())
})

afterEach(() => {
  vi.useRealTimers()
})

describe('my tickets', () => {
  it('lists tickets with unread marks and keeps the badge in sync', async () => {
    const wrapper = mount(SupportTicketsView, { global: { stubs } })
    await flushPromises()
    expect(mocks.listMyTickets).toHaveBeenCalledWith({ status: '', page: 1, page_size: 20 })
    const rows = wrapper.findAll('[data-testid="ticket-row"]')
    expect(rows).toHaveLength(2)
    expect(rows[0].attributes('href')).toBe('/support-tickets/3')
    expect(rows[0].find('[data-testid="ticket-unread"]').exists()).toBe(true)
    expect(rows[1].find('[data-testid="ticket-unread"]').exists()).toBe(false)
    expect(rows[0].get('[data-testid="ticket-status"]').attributes('data-status')).toBe('replied')
    expect(wrapper.get('[data-testid="ticket-open-count"]').text()).toContain('"count":1')
    expect(mocks.setUserUnread).toHaveBeenCalledWith(2)

    await wrapper.findAll('[data-testid="ticket-filter"]')[1].trigger('click')
    await flushPromises()
    expect(mocks.listMyTickets).toHaveBeenLastCalledWith({ status: 'open', page: 1, page_size: 20 })

    await wrapper.get('[data-testid="ticket-new"]').trigger('click')
    expect(wrapper.find('[data-testid="create-dialog"]').exists()).toBe(true)
    wrapper.findComponent(stubs.CreateTicketDialog).vm.$emit('created', detail())
    expect(mocks.push).toHaveBeenCalledWith('/support-tickets/7')
  })

  it('shows the empty state and load errors', async () => {
    mocks.listMyTickets.mockResolvedValueOnce({ items: [], total: 0, page: 1, page_size: 20, pages: 1 })
    let wrapper = mount(SupportTicketsView, { global: { stubs } })
    await flushPromises()
    expect(wrapper.get('[data-testid="ticket-empty"]').text()).toContain('supportTickets.empty')
    wrapper.unmount()

    mocks.listMyTickets.mockRejectedValueOnce({ reason: 'SUPPORT_TICKET_DISABLED', message: 'off' })
    wrapper = mount(SupportTicketsView, { global: { stubs } })
    await flushPromises()
    expect(wrapper.text()).toContain('supportTickets.errors.SUPPORT_TICKET_DISABLED')
  })
})

describe('my ticket', () => {
  it('shows the conversation and replies', async () => {
    mocks.replyMyTicket.mockResolvedValue(detail({ message_count: 3 }))
    const wrapper = mount(SupportTicketDetailView, { global: { stubs } })
    await flushPromises()
    expect(mocks.getMyTicket).toHaveBeenCalledWith(7)
    expect(mocks.refresh).toHaveBeenCalledWith(true)
    expect(wrapper.get('[data-testid="ticket-title"]').text()).toBe('ticket 7')
    expect(wrapper.findAll('[data-testid="ticket-message"]')).toHaveLength(2)

    await wrapper.get('[data-testid="ticket-reply-body"]').setValue(' more ')
    await wrapper.get('[data-testid="ticket-reply"]').trigger('submit')
    await flushPromises()
    expect(mocks.replyMyTicket).toHaveBeenCalledWith(7, 'more')
    expect(mocks.success).toHaveBeenCalledWith('supportTickets.detail.sent')
    expect((wrapper.get('[data-testid="ticket-reply-body"]').element as HTMLTextAreaElement).value).toBe('')
  })

  it('closes after confirming and offers to reopen', async () => {
    mocks.closeMyTicket.mockResolvedValue(detail({ status: 'closed', closed_by_role: 'user' }))
    mocks.reopenMyTicket.mockResolvedValue(detail())
    const wrapper = mount(SupportTicketDetailView, { global: { stubs } })
    await flushPromises()
    await wrapper.get('[data-testid="ticket-close"]').trigger('click')
    await wrapper.get('[data-testid="confirm-yes"]').trigger('click')
    await flushPromises()
    expect(mocks.closeMyTicket).toHaveBeenCalledWith(7)
    expect(wrapper.find('[data-testid="ticket-reply"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="ticket-closed"]').text()).toContain('supportTickets.detail.closedByYou')

    await wrapper.get('[data-testid="ticket-reopen"]').trigger('click')
    await flushPromises()
    expect(mocks.reopenMyTicket).toHaveBeenCalledWith(7)
    expect(wrapper.find('[data-testid="ticket-reply"]').exists()).toBe(true)
  })

  it('keeps the draft when sending fails', async () => {
    mocks.replyMyTicket.mockRejectedValue({ reason: 'SUPPORT_TICKET_CLOSED', message: 'closed' })
    const wrapper = mount(SupportTicketDetailView, { global: { stubs } })
    await flushPromises()
    await wrapper.get('[data-testid="ticket-reply-body"]').setValue('more')
    await wrapper.get('[data-testid="ticket-reply"]').trigger('submit')
    await flushPromises()
    expect(mocks.error).toHaveBeenCalledWith(expect.stringContaining('supportTickets.errors.SUPPORT_TICKET_CLOSED'))
    expect((wrapper.get('[data-testid="ticket-reply-body"]').element as HTMLTextAreaElement).value).toBe('more')
  })

  it('refreshes quietly every 30 seconds while the page is visible', async () => {
    vi.useFakeTimers()
    let visibility: DocumentVisibilityState = 'visible'
    vi.spyOn(document, 'visibilityState', 'get').mockImplementation(() => visibility)
    const wrapper = mount(SupportTicketDetailView, { global: { stubs } })
    await flushPromises()
    expect(mocks.getMyTicket).toHaveBeenCalledTimes(1)

    mocks.getMyTicket.mockResolvedValueOnce({ ...detail(), messages: [...detail().messages, { id: 3, author_role: 'admin', body: 'fixed', created_at: '2026-10-07T06:10:00Z' }] })
    await vi.advanceTimersByTimeAsync(30_000)
    expect(mocks.getMyTicket).toHaveBeenCalledTimes(2)
    expect(wrapper.findAll('[data-testid="ticket-message"]')).toHaveLength(3)
    expect(mocks.refresh).toHaveBeenCalledTimes(1) // quiet reloads do not hit the badge

    visibility = 'hidden'
    await vi.advanceTimersByTimeAsync(30_000)
    expect(mocks.getMyTicket).toHaveBeenCalledTimes(2)

    mocks.getMyTicket.mockRejectedValueOnce(new Error('offline'))
    visibility = 'visible'
    await vi.advanceTimersByTimeAsync(30_000)
    expect(wrapper.find('[data-testid="ticket-load-error"]').exists()).toBe(false)
    expect(wrapper.findAll('[data-testid="ticket-message"]')).toHaveLength(3)

    wrapper.unmount()
    await vi.advanceTimersByTimeAsync(60_000)
    expect(mocks.getMyTicket).toHaveBeenCalledTimes(3)
  })
})

import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useSupportTicketStore } from '../supportTickets'

const mocks = vi.hoisted(() => ({
  getMySummary: vi.fn(),
  getAdminSummary: vi.fn(),
  auth: { isAuthenticated: true, isAdmin: false, isObserver: false },
  app: { cachedPublicSettings: { support_ticket_enabled: true } as Record<string, unknown> | null },
}))
vi.mock('@/api/supportTickets', () => ({ getMySummary: mocks.getMySummary, getAdminSummary: mocks.getAdminSummary }))
vi.mock('../auth', () => ({ useAuthStore: () => mocks.auth }))
vi.mock('../app', () => ({ useAppStore: () => mocks.app }))

beforeEach(() => {
  setActivePinia(createPinia())
  vi.clearAllMocks()
  vi.useFakeTimers()
  mocks.auth.isAuthenticated = true
  mocks.auth.isAdmin = false
  mocks.auth.isObserver = false
  mocks.app.cachedPublicSettings = { support_ticket_enabled: true }
  mocks.getMySummary.mockResolvedValue({ unread_count: 3 })
  mocks.getAdminSummary.mockResolvedValue({ pending_count: 8 })
})

afterEach(() => {
  vi.useRealTimers()
})

describe('support ticket badge store', () => {
  it('reads the user summary at most once a minute unless forced', async () => {
    const store = useSupportTicketStore()
    await store.refresh()
    expect(store.userUnread).toBe(3)
    await store.refresh()
    expect(mocks.getMySummary).toHaveBeenCalledTimes(1)
    vi.advanceTimersByTime(60_000)
    await store.refresh()
    expect(mocks.getMySummary).toHaveBeenCalledTimes(2)
    await store.refresh(true)
    expect(mocks.getMySummary).toHaveBeenCalledTimes(3)
    expect(mocks.getAdminSummary).not.toHaveBeenCalled()
  })

  it('reads the admin queue for admins', async () => {
    mocks.auth.isAdmin = true
    const store = useSupportTicketStore()
    await store.refresh()
    expect(store.adminPending).toBe(8)
    expect(mocks.getMySummary).not.toHaveBeenCalled()
  })

  it('skips observers, who have no ticket menu', async () => {
    mocks.auth.isObserver = true
    const store = useSupportTicketStore()
    await store.refresh(true)
    expect(mocks.getMySummary).not.toHaveBeenCalled()
    expect(mocks.getAdminSummary).not.toHaveBeenCalled()
  })

  it('stays at zero without a session or while the feature is off', async () => {
    const store = useSupportTicketStore()
    store.setUserUnread(2)
    mocks.app.cachedPublicSettings = { support_ticket_enabled: false }
    await store.refresh(true)
    expect(store.userUnread).toBe(0)
    mocks.app.cachedPublicSettings = null
    await store.refresh(true)
    mocks.app.cachedPublicSettings = { support_ticket_enabled: true }
    mocks.auth.isAuthenticated = false
    await store.refresh(true)
    expect(mocks.getMySummary).not.toHaveBeenCalled()
  })

  it('retries on the next navigation after a failure and resets on logout', async () => {
    mocks.getMySummary.mockRejectedValueOnce(new Error('offline'))
    const store = useSupportTicketStore()
    await store.refresh()
    await store.refresh()
    expect(mocks.getMySummary).toHaveBeenCalledTimes(2)
    expect(store.userUnread).toBe(3)
    store.setAdminPending(-1)
    expect(store.adminPending).toBe(0)
    store.reset()
    expect(store.userUnread).toBe(0)
    await store.refresh()
    expect(mocks.getMySummary).toHaveBeenCalledTimes(3)
  })
})

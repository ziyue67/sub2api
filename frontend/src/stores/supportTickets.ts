/**
 * Sidebar badge counts for support tickets: tickets with unread admin replies
 * (users) and tickets waiting for an admin (admins). Refreshed on login, on
 * navigation at most once a minute, and right after ticket actions — no timer.
 */
import { defineStore } from 'pinia'
import { ref } from 'vue'
import { getAdminSummary, getMySummary } from '@/api/supportTickets'
import { FeatureFlags, resolveFeatureFlag } from '@/utils/featureFlags'
import { useAppStore } from './app'
import { useAuthStore } from './auth'

const THROTTLE_MS = 60_000

export const useSupportTicketStore = defineStore('supportTickets', () => {
  const userUnread = ref(0)
  const adminPending = ref(0)
  let lastFetch = 0
  let generation = 0

  async function refresh(force = false) {
    const authStore = useAuthStore()
    const appStore = useAppStore()
    // Observers only manage accounts and have no ticket menu.
    if (!authStore.isAuthenticated || authStore.isObserver || !resolveFeatureFlag(appStore.cachedPublicSettings, FeatureFlags.supportTickets)) {
      userUnread.value = 0
      adminPending.value = 0
      return
    }
    const now = Date.now()
    if (!force && lastFetch > 0 && now - lastFetch < THROTTLE_MS) return
    lastFetch = now
    const current = ++generation
    try {
      if (authStore.isAdmin) {
        const summary = await getAdminSummary()
        if (current === generation) adminPending.value = summary.pending_count
      } else {
        const summary = await getMySummary()
        if (current === generation) userUnread.value = summary.unread_count
      }
    } catch {
      // Let the next navigation retry.
      if (current === generation) lastFetch = 0
    }
  }

  function setUserUnread(count: number) {
    userUnread.value = Math.max(0, count)
  }

  function setAdminPending(count: number) {
    adminPending.value = Math.max(0, count)
  }

  function reset() {
    generation++
    lastFetch = 0
    userUnread.value = 0
    adminPending.value = 0
  }

  return { userUnread, adminPending, refresh, setUserUnread, setAdminPending, reset }
})

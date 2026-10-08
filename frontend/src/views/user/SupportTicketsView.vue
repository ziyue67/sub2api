<template>
  <AppLayout>
    <div class="mx-auto max-w-5xl space-y-4">
      <div class="flex flex-wrap items-center justify-between gap-3">
        <div class="inline-flex rounded-xl bg-gray-100 p-1 dark:bg-dark-800" role="group" :aria-label="t('supportTickets.columns.status')">
          <button
            v-for="option in statusOptions"
            :key="option.value"
            type="button"
            class="rounded-lg px-3 py-1.5 text-sm font-medium transition"
            :class="status === option.value
              ? 'bg-white text-gray-900 shadow-sm dark:bg-dark-600 dark:text-white'
              : 'text-gray-500 hover:text-gray-900 dark:text-gray-400 dark:hover:text-white'"
            :aria-pressed="status === option.value"
            data-testid="ticket-filter"
            @click="setStatus(option.value)"
          >
            {{ option.label }}
          </button>
        </div>
        <div class="flex items-center gap-2">
          <button type="button" class="btn btn-secondary" :disabled="loading" :title="t('supportTickets.refresh')" :aria-label="t('supportTickets.refresh')" @click="load">
            <Icon name="refresh" size="md" :class="loading ? 'animate-spin' : ''" />
          </button>
          <button type="button" class="btn btn-primary" data-testid="ticket-new" @click="createOpen = true">
            <Icon name="plus" size="md" class="mr-1" />
            {{ t('supportTickets.newTicket') }}
          </button>
        </div>
      </div>
      <p v-if="summary" class="text-xs text-gray-500 dark:text-gray-400" data-testid="ticket-open-count">
        {{ t('supportTickets.form.openCount', { count: summary.open_count, max: summary.max_open }) }}
      </p>

      <div v-if="loading && !tickets.length" class="space-y-3" aria-busy="true">
        <div v-for="index in 3" :key="index" class="card h-20 animate-pulse"></div>
      </div>
      <p v-else-if="loadError" class="card p-6 text-sm text-red-600 dark:text-red-400">{{ loadError }}</p>
      <div v-else-if="!tickets.length" class="card" data-testid="ticket-empty">
        <EmptyState :title="t('supportTickets.empty')" :description="t('supportTickets.emptyHint')" />
      </div>
      <ul v-else class="space-y-3">
        <li v-for="ticket in tickets" :key="ticket.id">
          <router-link
            :to="`/support-tickets/${ticket.id}`"
            class="card block p-4 transition hover:ring-2 hover:ring-primary-200 dark:hover:ring-primary-800"
            data-testid="ticket-row"
          >
            <div class="flex items-start justify-between gap-3">
              <div class="min-w-0">
                <div class="flex items-center gap-2">
                  <span
                    v-if="ticket.user_unread"
                    class="h-2 w-2 shrink-0 rounded-full bg-red-500"
                    :title="t('supportTickets.unread')"
                    data-testid="ticket-unread"
                  ></span>
                  <span class="truncate font-medium text-gray-900 dark:text-white">{{ ticket.title }}</span>
                </div>
                <div class="mt-1 flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-gray-500 dark:text-gray-400">
                  <span>#{{ ticket.id }}</span>
                  <span v-if="ticket.category" class="rounded-md bg-gray-100 px-1.5 py-0.5 dark:bg-dark-700">{{ ticket.category }}</span>
                  <span>{{ t('supportTickets.messageCount', { count: ticket.message_count }) }}</span>
                  <span>{{ formatRelativeTime(ticket.last_message_at) }}</span>
                  <span v-if="ticket.user_unread" class="font-medium text-red-600 dark:text-red-400">{{ t('supportTickets.unread') }}</span>
                </div>
              </div>
              <TicketStatusBadge :status="ticket.status" />
            </div>
          </router-link>
        </li>
      </ul>

      <Pagination
        v-if="total > pageSize"
        :page="page"
        :total="total"
        :page-size="pageSize"
        :show-page-size-selector="false"
        @update:page="changePage"
      />
    </div>

    <CreateTicketDialog :show="createOpen" :summary="summary" @close="createOpen = false" @created="onCreated" />
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import Pagination from '@/components/common/Pagination.vue'
import Icon from '@/components/icons/Icon.vue'
import CreateTicketDialog from '@/features/support-tickets/CreateTicketDialog.vue'
import TicketStatusBadge from '@/features/support-tickets/TicketStatusBadge.vue'
import { supportTicketErrorMessage } from '@/features/support-tickets/supportTickets'
import {
  getMySummary,
  listMyTickets,
  type SupportTicket,
  type SupportTicketDetail,
  type SupportTicketStatusFilter,
  type SupportTicketUserSummary,
} from '@/api/supportTickets'
import { useSupportTicketStore } from '@/stores/supportTickets'
import { formatRelativeTime } from '@/utils/format'

const { t } = useI18n()
const router = useRouter()
const ticketStore = useSupportTicketStore()

const pageSize = 20
const status = ref<SupportTicketStatusFilter>('')
const page = ref(1)
const total = ref(0)
const tickets = ref<SupportTicket[]>([])
const summary = ref<SupportTicketUserSummary | null>(null)
const loading = ref(false)
const loadError = ref('')
const createOpen = ref(false)

const statusOptions = computed<Array<{ value: SupportTicketStatusFilter; label: string }>>(() => [
  { value: '', label: t('supportTickets.filters.all') },
  { value: 'open', label: t('supportTickets.filters.open') },
  { value: 'closed', label: t('supportTickets.status.closed') },
])

async function load() {
  loading.value = true
  loadError.value = ''
  try {
    const [list, mine] = await Promise.all([
      listMyTickets({ status: status.value, page: page.value, page_size: pageSize }),
      getMySummary(),
    ])
    tickets.value = list.items
    total.value = list.total
    summary.value = mine
    ticketStore.setUserUnread(mine.unread_count)
  } catch (err) {
    loadError.value = supportTicketErrorMessage(err, t, 'supportTickets.loadFailed')
  } finally {
    loading.value = false
  }
}

function setStatus(value: SupportTicketStatusFilter) {
  if (status.value === value) return
  status.value = value
  page.value = 1
  load()
}

function changePage(value: number) {
  page.value = value
  load()
}

function onCreated(detail: SupportTicketDetail) {
  createOpen.value = false
  router.push(`/support-tickets/${detail.ticket.id}`)
}

onMounted(load)
</script>

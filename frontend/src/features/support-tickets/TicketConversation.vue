<template>
  <ol class="space-y-5" data-testid="ticket-conversation">
    <li
      v-for="message in messages"
      :key="message.id"
      class="flex"
      :class="isMine(message) ? 'justify-end' : 'justify-start'"
      data-testid="ticket-message"
      :data-role="message.author_role"
    >
      <div class="min-w-0 max-w-[88%] sm:max-w-[75%]">
        <div class="mb-1 flex items-center gap-2 text-xs text-gray-500 dark:text-gray-400" :class="isMine(message) ? 'justify-end' : ''">
          <span class="font-medium text-gray-700 dark:text-gray-200" data-testid="ticket-message-author">{{ authorLabel(message) }}</span>
          <time :datetime="message.created_at">{{ formatDateTimeToMinute(message.created_at) }}</time>
        </div>
        <div
          class="rounded-2xl px-4 py-3"
          :class="isMine(message)
            ? 'rounded-tr-md bg-primary-50 text-gray-900 dark:bg-primary-900/30 dark:text-gray-100'
            : 'rounded-tl-md bg-gray-50 text-gray-900 ring-1 ring-gray-200 dark:bg-dark-800 dark:text-gray-100 dark:ring-dark-700'"
        >
          <TicketMessageBody :text="message.body" />
        </div>
      </div>
    </li>
  </ol>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { SupportTicketMessage, SupportTicketRole } from '@/api/supportTickets'
import { formatDateTimeToMinute } from '@/utils/format'
import TicketMessageBody from './TicketMessageBody.vue'

// viewer decides which side is "mine" and how authors are named.
const props = defineProps<{ messages: SupportTicketMessage[]; viewer: SupportTicketRole }>()
const { t } = useI18n()

function isMine(message: SupportTicketMessage) {
  return message.author_role === props.viewer
}

function authorLabel(message: SupportTicketMessage) {
  if (props.viewer === 'user') {
    return message.author_role === 'user' ? t('supportTickets.detail.you') : t('supportTickets.detail.staff')
  }
  return message.author_role === 'admin'
    ? message.author_name || t('supportTickets.detail.staff')
    : t('supportTickets.detail.user')
}
</script>

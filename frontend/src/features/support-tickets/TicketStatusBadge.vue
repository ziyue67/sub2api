<template>
  <span
    class="inline-flex shrink-0 items-center rounded-full px-2.5 py-0.5 text-xs font-medium ring-1 ring-inset"
    :class="SUPPORT_TICKET_STATUS_CLASSES[status]"
    data-testid="ticket-status"
    :data-status="status"
  >{{ t(`supportTickets.status.${status}`) }}</span>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { SupportTicketStatus } from '@/api/supportTickets'
import { SUPPORT_TICKET_STATUS_CLASSES } from './supportTickets'

defineProps<{ status: SupportTicketStatus }>()
const { t } = useI18n()
</script>

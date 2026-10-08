<template>
  <form class="space-y-3" data-testid="ticket-reply" @submit.prevent="submit">
    <textarea
      v-model="body"
      rows="4"
      class="input resize-y"
      :placeholder="placeholder"
      :disabled="sending"
      :aria-label="placeholder"
      data-testid="ticket-reply-body"
      @keydown.enter.ctrl.prevent="submit"
      @keydown.enter.meta.prevent="submit"
    ></textarea>
    <div class="flex flex-wrap items-center justify-between gap-3">
      <span class="text-xs" :class="tooLong ? 'font-medium text-red-600 dark:text-red-400' : 'text-gray-400'" data-testid="ticket-reply-counter">
        {{ t('supportTickets.form.counter', { count: length, max: SUPPORT_TICKET_BODY_MAX }) }}
      </span>
      <div class="flex flex-wrap items-center gap-2">
        <slot name="extra" />
        <button type="submit" class="btn btn-primary" :disabled="!canSend" data-testid="ticket-reply-send">
          {{ sending ? t('supportTickets.detail.sending') : t('supportTickets.detail.send') }}
        </button>
      </div>
    </div>
  </form>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { SUPPORT_TICKET_BODY_MAX, supportTicketLength } from '@/api/supportTickets'

const props = defineProps<{ placeholder: string; sending?: boolean }>()
const emit = defineEmits<{ (e: 'submit', body: string): void }>()
// The parent owns the draft so it can clear it after a successful send.
const body = defineModel<string>({ required: true })
const { t } = useI18n()

const length = computed(() => supportTicketLength(body.value))
const tooLong = computed(() => length.value > SUPPORT_TICKET_BODY_MAX)
const canSend = computed(() => !props.sending && body.value.trim() !== '' && !tooLong.value)

function submit() {
  if (canSend.value) emit('submit', body.value.trim())
}
</script>

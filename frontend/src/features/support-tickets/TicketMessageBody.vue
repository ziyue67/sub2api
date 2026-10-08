<template>
  <!-- Plain text only: links are real <a> nodes, nothing goes through v-html. -->
  <p class="whitespace-pre-wrap break-words text-sm leading-relaxed" data-testid="ticket-message-body"><template v-for="(part, index) in parts" :key="index"><a v-if="part.kind === 'link'" :href="part.href" target="_blank" rel="noopener noreferrer nofollow" class="break-all text-primary-600 underline decoration-primary-300 underline-offset-2 hover:text-primary-700 dark:text-primary-400 dark:hover:text-primary-300" data-testid="ticket-link">{{ part.text }}</a><template v-else>{{ part.text }}</template></template></p>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { splitSupportTicketText } from './supportTickets'

const props = defineProps<{ text: string }>()
const parts = computed(() => splitSupportTicketText(props.text))
</script>

<template>
  <section class="space-y-3 rounded-lg border p-3" aria-labelledby="pelican-personal-bank-title" data-testid="pelican-personal-bank">
    <div class="flex flex-wrap items-center justify-between gap-2">
      <h3 id="pelican-personal-bank-title" class="text-sm font-semibold">{{ t('pelicanTests.personalBank.manage') }}</h3>
      <button type="button" class="btn btn-secondary btn-sm" :disabled="!ready || saving || items.length >= 50" data-testid="pelican-personal-add" @click="add">
        {{ t('pelicanTests.personalBank.add') }}
      </button>
    </div>
    <p v-if="loading" role="status" class="text-sm">{{ t('pelicanTests.personalBank.loading') }}</p>
    <p v-if="error" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ error }}</p>
    <button v-if="!ready && !loading" type="button" class="btn btn-secondary" @click="load">{{ t('pelicanTests.personalBank.retry') }}</button>
    <p v-if="ready && !items.length" class="text-sm text-gray-500">{{ t('pelicanTests.personalBank.empty') }}</p>
    <div v-if="items.length" class="max-h-80 divide-y overflow-y-auto">
      <div v-for="item in items" :key="item.draftKey" class="space-y-2 py-3">
        <div class="flex flex-wrap items-center gap-2">
          <label class="min-w-40 flex-1 text-sm">
            {{ t('pelicanTests.personalBank.title') }}
            <input v-model="item.title" maxlength="80" class="input mt-1 w-full" :disabled="saving" data-testid="pelican-personal-title" />
          </label>
          <label class="flex items-center gap-2 text-sm">
            <input v-model="item.enabled" type="checkbox" :disabled="saving" data-testid="pelican-personal-enabled" />
            {{ t('pelicanTests.personalBank.enabled') }}
          </label>
          <button v-if="!item.id" type="button" class="btn btn-secondary btn-sm" :disabled="saving" @click="removeDraft(item.draftKey)">
            {{ t('pelicanTests.personalBank.removeDraft') }}
          </button>
        </div>
        <label class="block text-sm">
          {{ t('pelicanTests.personalBank.prompt') }}
          <textarea v-model="item.prompt" rows="3" maxlength="4000" class="input mt-1 w-full" :disabled="saving" data-testid="pelican-personal-prompt" />
        </label>
      </div>
    </div>
    <div class="flex flex-wrap justify-end gap-2">
      <button type="button" class="btn btn-secondary" :disabled="saving" @click="emit('close')">{{ t('common.cancel') }}</button>
      <button type="button" class="btn btn-primary" :disabled="!canSave" data-testid="pelican-personal-save" @click="save">{{ t('common.save') }}</button>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import type { PelicanPersonalQuestion } from '@/types'
import { loadPelicanPersonalBank, savePelicanPersonalBank } from '@/utils/pelicanQuestionRound'

const emit = defineEmits<{ close: []; saved: [] }>()
const { t } = useI18n()
const controller = new AbortController()
const items = ref<Array<PelicanPersonalQuestion & { draftKey: number }>>([])
const loading = ref(false)
const saving = ref(false)
const ready = ref(false)
const error = ref('')
let nextKey = 0
const canSave = computed(() => ready.value && !saving.value && items.value.every(item => item.title.trim() && item.prompt.trim()))

async function load() {
  loading.value = true
  error.value = ''
  try {
    const result = await loadPelicanPersonalBank(controller.signal)
    if (controller.signal.aborted) return
    items.value = result.map(item => ({ ...item, draftKey: ++nextKey }))
    ready.value = true
  } catch {
    if (!controller.signal.aborted) error.value = t('pelicanTests.personalBank.loadError')
  } finally {
    loading.value = false
  }
}

function add() {
  if (!ready.value || saving.value || items.value.length >= 50) return
  items.value.push({ id: '', title: '', prompt: '', enabled: true, draftKey: ++nextKey })
}

function removeDraft(draftKey: number) {
  items.value = items.value.filter(item => item.id || item.draftKey !== draftKey)
}

async function save() {
  if (!canSave.value) return
  saving.value = true
  error.value = ''
  try {
    await savePelicanPersonalBank(items.value.map(({ id, title, prompt, enabled }) => ({ id, title, prompt, enabled })), controller.signal)
    if (!controller.signal.aborted) emit('saved')
  } catch {
    if (!controller.signal.aborted) error.value = t('pelicanTests.personalBank.saveError')
  } finally {
    saving.value = false
  }
}

onMounted(load)
onUnmounted(() => controller.abort())
</script>

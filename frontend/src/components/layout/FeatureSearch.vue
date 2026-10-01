<template>
  <div class="px-3 pb-2">
    <button
      type="button"
      class="flex h-10 w-full items-center gap-2 rounded-xl border border-gray-200 bg-gray-50 px-3 text-sm text-gray-500 transition-colors hover:border-primary-300 hover:text-primary-600 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500 dark:border-dark-600 dark:bg-dark-800 dark:text-dark-300 dark:hover:text-primary-400"
      :class="{ 'justify-center px-0': collapsed }"
      :title="`${t('common.featureSearch.title')} (${shortcut})`"
      :aria-label="t('common.featureSearch.title')"
      aria-haspopup="dialog"
      :aria-expanded="show"
      @click="open"
    >
      <Icon name="search" size="md" class="shrink-0" aria-hidden="true" />
      <template v-if="!collapsed">
        <span class="flex-1 text-left">{{ t('common.featureSearch.title') }}</span>
        <kbd class="whitespace-nowrap rounded border border-gray-200 px-1 text-xs dark:border-dark-600">{{ shortcut }}</kbd>
      </template>
    </button>
  </div>
  <BaseDialog :show="show" :title="t('common.featureSearch.title')" :close-on-click-outside="true" @close="show = false">
    <div class="relative">
      <Icon name="search" class="pointer-events-none absolute left-3 top-3 text-gray-400" aria-hidden="true" />
      <input
        ref="inputRef"
        v-model="query"
        class="input w-full pl-10"
        type="text"
        role="combobox"
        autocomplete="off"
        spellcheck="false"
        :placeholder="t('common.featureSearch.placeholder')"
        :aria-label="t('common.featureSearch.placeholder')"
        aria-autocomplete="list"
        aria-controls="feature-search-results"
        :aria-expanded="show"
        :aria-activedescendant="results.length ? `feature-search-option-${selectedIndex}` : undefined"
        @keydown="handleInputKeydown"
      />
    </div>
    <p class="my-3 text-xs text-gray-500 dark:text-dark-400" role="status" aria-live="polite">
      {{ t('common.featureSearch.resultCount', { count: results.length }) }}
    </p>
    <div id="feature-search-results" ref="resultsRef" role="listbox" :aria-label="t('common.featureSearch.title')" class="max-h-[45vh] space-y-1 overflow-y-auto">
      <button
        v-for="(item, index) in results"
        :id="`feature-search-option-${index}`"
        :key="item.path"
        type="button"
        role="option"
        tabindex="-1"
        :aria-selected="selectedIndex === index"
        class="flex w-full items-center gap-3 rounded-lg px-3 py-2.5 text-left transition-colors"
        :class="selectedIndex === index ? 'bg-primary-50 text-primary-700 dark:bg-primary-900/30 dark:text-primary-300' : 'text-gray-700 hover:bg-gray-50 dark:text-dark-200 dark:hover:bg-dark-800'"
        @mousemove="selectedIndex = index"
        @mousedown.prevent
        @click="navigate(item)"
      >
        <div class="min-w-0 flex-1">
          <div class="truncate text-sm font-medium">{{ item.label }}</div>
          <div class="mt-0.5 truncate text-xs text-gray-500 dark:text-dark-400">{{ item.group || item.path }}</div>
        </div>
        <Icon name="arrowRight" size="sm" aria-hidden="true" />
      </button>
    </div>
    <p v-if="!results.length" class="py-8 text-center text-sm text-gray-500 dark:text-dark-400">
      {{ t('common.featureSearch.noResults') }}
    </p>
    <template #footer>
      <p class="text-xs text-gray-500 dark:text-dark-400">{{ t('common.featureSearch.hint') }}</p>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import { buildFeatureSearchEntries, searchFeatures, type FeatureSearchEntry, type SearchNavItem } from '@/utils/featureSearch'
import { withSettingsSearch } from '@/utils/settingsSearch'
import { focusSettingsLocation } from '@/composables/useSettingsNavigation'

const props = defineProps<{ items: SearchNavItem[]; collapsed?: boolean }>()
const emit = defineEmits<{ navigate: [path: string] }>()
const { t, locale } = useI18n()
const router = useRouter()
const show = ref(false)
const query = ref('')
const selectedIndex = ref(0)
const inputRef = ref<HTMLInputElement | null>(null)
const resultsRef = ref<HTMLElement | null>(null)
const shortcut = /Mac|iPhone|iPad/.test(navigator.platform) ? '⌘ K' : 'Ctrl K'
const entries = computed(() => buildFeatureSearchEntries(withSettingsSearch(props.items, t, locale.value), path => {
  const meta = router.resolve(path).meta
  return [meta.title, meta.titleKey ? t(meta.titleKey) : ''].filter(Boolean).join(' ')
}))
const results = computed(() => searchFeatures(entries.value, query.value))
watch(results, () => { selectedIndex.value = 0 })

async function open() {
  // Avoid stacking a command dialog over an active form or confirmation.
  if (document.querySelector('[role="dialog"][aria-modal="true"]')) return
  query.value = ''
  selectedIndex.value = 0
  show.value = true
  await nextTick()
  await nextTick() // BaseDialog restores its own initial focus first.
  inputRef.value?.focus()
}

async function navigate(item: FeatureSearchEntry) {
  show.value = false
  emit('navigate', item.path)
  const target = router.resolve(item.path)
  const sameLocation = router.currentRoute.value.fullPath === target.fullPath
  await router.push(item.path)
  if (sameLocation && target.path === '/admin/settings') {
    await nextTick()
    focusSettingsLocation(target.query.tab, target.hash)
  }
}

async function handleInputKeydown(event: KeyboardEvent) {
  if (event.isComposing || event.keyCode === 229) return
  if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
    event.preventDefault()
    if (!results.value.length) return
    selectedIndex.value = (selectedIndex.value + (event.key === 'ArrowDown' ? 1 : -1) + results.value.length) % results.value.length
    await nextTick()
    resultsRef.value?.querySelector('[aria-selected="true"]')?.scrollIntoView({ block: 'nearest' })
  } else if (event.key === 'Enter') {
    event.preventDefault()
    const item = results.value[selectedIndex.value]
    if (item) navigate(item)
  }
}

function handleDocumentKeydown(event: KeyboardEvent) {
  if (event.isComposing || event.keyCode === 229) return
  if ((event.ctrlKey || event.metaKey) && !event.altKey && !event.shiftKey && event.key.toLowerCase() === 'k') {
    event.preventDefault()
    if (!event.repeat) {
      if (show.value) show.value = false
      else void open()
    }
  }
  if (show.value && event.key === 'Tab') {
    const dialog = inputRef.value?.closest('[role="dialog"]')
    const targets = dialog?.querySelectorAll<HTMLElement>('button:not([disabled]):not([tabindex="-1"]), input')
    if (!targets?.length) return
    const first = targets[0]
    const last = targets[targets.length - 1]
    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault()
      last?.focus()
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault()
      first?.focus()
    }
  }
}

onMounted(() => document.addEventListener('keydown', handleDocumentKeydown))
onUnmounted(() => document.removeEventListener('keydown', handleDocumentKeydown))
</script>

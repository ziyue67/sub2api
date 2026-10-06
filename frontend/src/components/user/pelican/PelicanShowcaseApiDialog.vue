<template>
  <BaseDialog
    :show="show"
    :title="t('pelicanShowcase.api.title')"
    width="wide"
    close-on-click-outside
    @close="emit('close')"
  >
    <div class="min-w-0 space-y-5" data-testid="showcase-api-dialog">
      <div class="flex flex-wrap items-center justify-between gap-3">
        <span
          class="inline-flex items-center gap-1.5 text-sm font-medium"
          :class="enabled ? 'text-emerald-700 dark:text-emerald-400' : 'text-amber-700 dark:text-amber-400'"
          data-testid="showcase-api-status"
        >
          <Icon :name="enabled ? 'checkCircle' : 'exclamationCircle'" size="sm" />
          {{ t(enabled ? 'pelicanShowcase.api.available' : 'pelicanShowcase.api.unavailable') }}
        </span>
        <RouterLink
          to="/keys"
          class="inline-flex items-center gap-1.5 text-sm font-medium text-primary-600 hover:text-primary-700 dark:text-primary-400 dark:hover:text-primary-300"
          data-testid="showcase-api-keys"
          @click="emit('close')"
        >
          <Icon name="key" size="sm" />{{ t('pelicanShowcase.api.manageKeys') }}
        </RouterLink>
      </div>

      <p v-if="!enabled" class="border-l-2 border-amber-400 bg-amber-50 px-3 py-2 text-sm text-amber-800 dark:bg-amber-950/30 dark:text-amber-200" role="status">
        {{ t('pelicanShowcase.api.unavailableHint') }}
      </p>
      <p class="text-sm leading-6 text-gray-600 dark:text-gray-400">{{ t('pelicanShowcase.api.readOnly') }}</p>

      <dl class="space-y-3 text-sm">
        <div v-for="endpoint in endpoints" :key="endpoint.key" class="min-w-0">
          <dt class="mb-1 text-xs font-medium text-gray-500 dark:text-gray-400">{{ endpoint.label }}</dt>
          <dd class="flex min-w-0 items-start gap-2">
            <code class="min-w-0 flex-1 break-all font-mono text-xs leading-5 text-gray-900 dark:text-gray-100" :data-testid="`showcase-api-${endpoint.key}-url`">GET {{ endpoint.url }}</code>
            <button
              type="button"
              class="flex h-7 w-7 shrink-0 items-center justify-center rounded-md text-gray-500 hover:bg-gray-100 hover:text-gray-800 dark:text-gray-400 dark:hover:bg-dark-700 dark:hover:text-white"
              :title="t('pelicanShowcase.api.copyUrl')"
              :aria-label="t('pelicanShowcase.api.copyUrl')"
              :data-testid="`showcase-api-copy-${endpoint.key}-url`"
              @click="copyToClipboard(endpoint.url)"
            >
              <Icon name="copy" size="sm" />
            </button>
          </dd>
        </div>
      </dl>
      <p class="text-xs leading-5 text-gray-500 dark:text-gray-400">{{ t('pelicanShowcase.api.authentication') }}</p>

      <section class="min-w-0 space-y-3">
        <nav class="flex flex-wrap gap-1 border-b border-gray-200 dark:border-dark-700" role="tablist" :aria-label="t('pelicanShowcase.api.examples')">
          <button
            v-for="example in examples"
            :key="example.key"
            type="button"
            role="tab"
            :id="`${id}-${example.key}-tab`"
            :aria-controls="`${id}-example`"
            :aria-selected="activeExample === example.key"
            class="border-b-2 px-3 py-2 text-sm font-medium transition-colors"
            :class="activeExample === example.key ? 'border-primary-500 text-primary-600 dark:text-primary-400' : 'border-transparent text-gray-500 hover:text-gray-800 dark:text-gray-400 dark:hover:text-gray-200'"
            :data-testid="`showcase-api-example-${example.key}`"
            @click="activeExample = example.key"
          >
            {{ example.label }}
          </button>
        </nav>
        <div class="overflow-hidden rounded-lg bg-gray-900 dark:bg-dark-900" role="tabpanel" :id="`${id}-example`" :aria-labelledby="`${id}-${activeExample}-tab`">
          <div class="flex items-center justify-between gap-2 border-b border-gray-700 bg-gray-800 px-3 py-2 dark:border-dark-700 dark:bg-dark-800">
            <span class="font-mono text-xs text-gray-400">curl</span>
            <button
              type="button"
              class="flex h-7 w-7 shrink-0 items-center justify-center rounded-md text-gray-300 hover:bg-gray-700 hover:text-white"
              :title="t('pelicanShowcase.api.copyExample')"
              :aria-label="t('pelicanShowcase.api.copyExample')"
              data-testid="showcase-api-copy-example"
              @click="copyToClipboard(currentExample.command)"
            >
              <Icon name="copy" size="sm" />
            </button>
          </div>
          <pre class="min-h-32 max-w-full overflow-x-auto p-4 font-mono text-xs leading-6 text-gray-100"><code data-testid="showcase-api-command" v-text="currentExample.command" /></pre>
        </div>
        <p class="text-xs leading-5 text-gray-500 dark:text-gray-400">{{ t(currentExample.hint) }}</p>
      </section>

      <div class="space-y-2 border-t border-gray-200 pt-4 text-xs leading-5 text-gray-500 dark:border-dark-700 dark:text-gray-400">
        <p>{{ t('pelicanShowcase.api.polling') }}</p>
        <p>{{ t('pelicanShowcase.api.keySafety') }}</p>
      </div>
    </div>
    <template #footer>
      <button type="button" class="btn btn-secondary" @click="emit('close')">{{ t('common.close') }}</button>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref, useId, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import { useClipboard } from '@/composables/useClipboard'

const props = defineProps<{ show: boolean; enabled: boolean; itemId?: number }>()
const emit = defineEmits<{ (e: 'close'): void }>()
const { t } = useI18n()
const { copyToClipboard } = useClipboard()
const id = `pelican-api-${useId()}`
type ExampleKey = 'manifest' | 'item' | 'cache'
const activeExample = ref<ExampleKey>('manifest')
const manifestUrl = new URL('/api/v1/public/pelican-showcase', window.location.origin).href
const itemUrl = computed(() => `${manifestUrl}/items/${Number.isSafeInteger(props.itemId) && props.itemId! > 0 ? props.itemId : 'RESULT_ID'}`)
const endpoints = computed(() => [
  { key: 'manifest', label: t('pelicanShowcase.api.manifestEndpoint'), url: manifestUrl },
  { key: 'item', label: t('pelicanShowcase.api.itemEndpoint'), url: itemUrl.value },
])
const examples = computed(() => [
  {
    key: 'manifest' as const,
    label: t('pelicanShowcase.api.manifestExample'),
    command: ['curl --compressed -i', '  -H "Authorization: Bearer YOUR_API_KEY"', '  "' + manifestUrl + '"'].join(' \\\n'),
    hint: 'pelicanShowcase.api.manifestHint',
  },
  {
    key: 'item' as const,
    label: t('pelicanShowcase.api.itemExample'),
    command: ['curl --compressed -sS', '  -H "Authorization: Bearer YOUR_API_KEY"', '  "' + itemUrl.value + '"'].join(' \\\n'),
    hint: 'pelicanShowcase.api.itemHint',
  },
  {
    key: 'cache' as const,
    label: t('pelicanShowcase.api.cacheExample'),
    command: ['curl --compressed -i', '  -H "Authorization: Bearer YOUR_API_KEY"', "  -H 'If-None-Match: YOUR_ETAG'", '  "' + manifestUrl + '"'].join(' \\\n'),
    hint: 'pelicanShowcase.api.cacheHint',
  },
])
const currentExample = computed(() => examples.value.find((example) => example.key === activeExample.value)!)
watch(() => props.show, (show) => {
  if (show) activeExample.value = 'manifest'
})
</script>

<template>
  <section class="space-y-2 rounded-lg border p-3 dark:border-dark-600" data-testid="upstream-channel-selector">
    <label class="block font-medium">{{ t('upstreamEntities.accountChannel') }}</label>
    <p class="text-xs text-gray-500">{{ t('upstreamEntities.history') }}</p>
    <div class="flex flex-wrap gap-2"><input v-model="search" class="input min-w-0 flex-1" :placeholder="t('upstreamEntities.search')" /><button type="button" class="btn btn-secondary" :disabled="loading || disabled" @click="load(1)">{{ t('upstreamEntities.load') }}</button><a href="/admin/upstream-channels" target="_blank" rel="noopener" class="btn btn-secondary">{{ t('upstreamEntities.manage') }}</a></div>
    <select :disabled="disabled" :value="modelValue" class="input w-full" data-testid="upstream-channel-select" @focus="ensureLoaded" @change="emit('update:modelValue', Number(($event.target as HTMLSelectElement).value))">
      <option v-if="allowKeep" :value="-1">{{ t('upstreamEntities.keep') }}</option><option :value="0">{{ t('upstreamEntities.unbound') }}</option>
      <option v-if="selected && !items.some(c => c.id === selected?.id)" :value="selected.id">{{ selected.name }} · {{ selected.code }} (#{{ selected.id }})</option>
      <option v-for="c in items" :key="c.id" :value="c.id">{{ c.name }} · {{ c.code }} (#{{ c.id }}) · {{ c.members }}</option>
    </select>
    <div v-if="loaded" class="flex gap-2 text-sm"><button type="button" :disabled="loading || page <= 1" @click="load(page - 1)">{{ t('profitOps.previous') }}</button><span>{{ page }} / {{ total }}</span><button type="button" :disabled="loading || page * 20 >= total" @click="load(page + 1)">{{ t('profitOps.next') }}</button></div>
    <p v-if="error" role="alert" class="text-red-600">{{ t('upstreamEntities.failed') }}</p>
  </section>
</template>
<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { listUpstreamChannels, type UpstreamChannel } from '@/api/admin/upstreamChannels'
defineProps<{ modelValue: number; allowKeep?: boolean; selected?: UpstreamChannel; disabled?: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [value: number] }>()
const { t } = useI18n()
const items = ref<UpstreamChannel[]>([]), search = ref(''), total = ref(0), page = ref(1), loaded = ref(false), loading = ref(false), error = ref(false)
async function load(next: number) { loading.value = true; error.value = false; try { const result = await listUpstreamChannels(search.value, next); items.value = result.items; total.value = result.total; page.value = next; loaded.value = true } catch { error.value = true } finally { loading.value = false } }
function ensureLoaded() { if (!loaded.value) void load(1) }
</script>

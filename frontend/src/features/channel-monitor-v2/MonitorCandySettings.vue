<template>
  <section class="card overflow-hidden !rounded-3xl !border-0 shadow-sm ring-1 ring-gray-900/5 dark:!bg-dark-800 dark:ring-dark-700" data-testid="monitor-candy-settings">
    <header class="card-header !py-3">
      <h3 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('channelMonitorV2.candy.settingsTitle') }}</h3>
      <p class="mt-1 text-xs leading-relaxed text-gray-500 dark:text-gray-400">{{ t('channelMonitorV2.candy.settingsHint') }}</p>
    </header>
    <div class="space-y-4 p-5">
      <p class="rounded-xl bg-amber-50 p-3 text-xs text-amber-800 dark:bg-amber-900/20 dark:text-amber-200">{{ t('channelMonitorV2.candy.costHint') }}</p>
      <div v-for="(probe, index) in modelValue || []" :key="probe.group_id" class="rounded-2xl border border-gray-200 p-4 dark:border-dark-700">
        <div class="mb-3 flex items-center gap-3">
          <Toggle :model-value="probe.enabled" @update:model-value="patch(index, { enabled: $event })" />
          <strong class="min-w-0 flex-1 truncate text-sm text-gray-800 dark:text-gray-100">{{ groupName(probe.group_id) }}</strong>
          <button type="button" class="btn btn-ghost btn-sm text-red-600" @click="remove(index)">{{ t('common.delete') }}</button>
        </div>
        <div class="grid gap-3 sm:grid-cols-3">
          <label><span class="input-label">{{ t('channelMonitorV2.candy.model') }}</span><input :value="probe.model" class="input" maxlength="100" required :placeholder="t('channelMonitorV2.candy.modelHint')" @input="patch(index, { model: ($event.target as HTMLInputElement).value })" /></label>
          <label><span class="input-label">{{ t('channelMonitorV2.candy.effort') }}</span><select :value="probe.reasoning_effort" class="input" @change="patch(index, { reasoning_effort: ($event.target as HTMLSelectElement).value })"><option v-for="effort in ['minimal', 'low', 'medium', 'high', 'xhigh']" :key="effort" :value="effort">{{ effort }}</option></select></label>
          <label><span class="input-label">{{ t('channelMonitorV2.candy.interval') }}</span><input :value="probe.interval_minutes" type="number" class="input" min="1" max="1440" step="1" required @input="patch(index, { interval_minutes: Number(($event.target as HTMLInputElement).value) })" /></label>
        </div>
      </div>
      <div class="flex flex-wrap items-center gap-2">
        <select v-model.number="selectedGroup" class="input min-w-0 flex-1" data-testid="candy-group-select"><option :value="0">{{ t('channelMonitorV2.candy.selectGroup') }}</option><option v-for="group in available" :key="group.id" :value="group.id">{{ group.name }} · {{ group.platform }}</option></select>
        <button type="button" class="btn btn-secondary" :disabled="!selectedGroup || (modelValue?.length || 0) >= 64" data-testid="add-candy-probe" @click="add">{{ t('channelMonitorV2.candy.addGroup') }}</button>
      </div>
      <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('channelMonitorV2.candy.ruleHint') }}</p>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Toggle from '@/components/common/Toggle.vue'
import type { MonitorCandyProbe } from '@/api/channelMonitorV2'

const props = defineProps<{ modelValue?: MonitorCandyProbe[]; groups: Array<{ id: number; name: string; platform: string }> }>()
const emit = defineEmits<{ 'update:modelValue': [MonitorCandyProbe[]] }>()
const { t } = useI18n()
const selectedGroup = ref(0)
const available = computed(() => props.groups.filter(group => !props.modelValue?.some(probe => probe.group_id === group.id)))
const groupName = (id: number) => props.groups.find(group => group.id === id)?.name || `#${id}`
function patch(index: number, value: Partial<MonitorCandyProbe>) { emit('update:modelValue', (props.modelValue || []).map((probe, i) => i === index ? { ...probe, ...value } : probe)) }
function remove(index: number) { emit('update:modelValue', (props.modelValue || []).filter((_, i) => i !== index)) }
function add() {
  if (!available.value.some(group => group.id === selectedGroup.value) || (props.modelValue?.length || 0) >= 64) return
  emit('update:modelValue', [...(props.modelValue || []), { group_id: selectedGroup.value, enabled: true, model: '', reasoning_effort: 'medium', interval_minutes: 1 }])
  selectedGroup.value = 0
}
</script>

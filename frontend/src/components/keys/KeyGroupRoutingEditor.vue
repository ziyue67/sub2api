<template>
  <fieldset class="space-y-3" data-testid="key-group-routing">
    <legend class="input-label">{{ t('keys.unifiedRouting') }}</legend>
    <label class="block text-sm font-medium">
      {{ t('keys.routingPlatform') }}
      <select v-model="platformFilter" class="input mt-1 w-full" data-testid="routing-platform">
        <option value="">{{ t('keys.routingAllPlatforms') }}</option>
        <option v-for="platform in platforms" :key="platform" :value="platform">{{ platformLabel(platform) }}</option>
      </select>
    </label>
    <p class="input-hint">{{ t('keys.routingPlatformHint') }}</p>
    <label class="block text-sm">
      {{ t('keys.routingStrategy') }}
      <select class="input mt-1 w-full" :value="modelValue?.strategy ?? 'legacy'" data-testid="routing-strategy" @change="chooseStrategy">
        <option v-if="!modelValue" value="legacy">{{ t('keys.routingLegacy') }}</option>
        <option value="lowest_rate">{{ t('keys.routingLowestRate') }}</option>
        <option value="priority_weighted">{{ t('keys.routingPriorityWeighted') }}</option>
      </select>
    </label>
    <p class="input-hint">{{ !modelValue ? t('keys.routingLegacyHint') : t('keys.routingUnifiedHint') }}</p>
    <div v-for="(route, index) in current.routes" :key="index" class="rounded-lg border border-gray-200 p-3 dark:border-dark-600" data-testid="routing-row">
      <div class="flex items-center gap-2">
        <input type="checkbox" :checked="route.enabled" :disabled="!modelValue" :aria-label="t('common.enabled')" @change="update(index, 'enabled', ($event.target as HTMLInputElement).checked)" />
        <Select :model-value="route.group_id" :options="optionsFor(route.group_id)" :disabled="!modelValue" :searchable="true" class="min-w-0 flex-1" :aria-label="t('keys.group')" @update:model-value="update(index, 'group_id', Number($event))">
          <template #selected="{ option }">
            <RoutingGroupOption v-if="option" :group="option as RoutingGroupChoice" />
          </template>
          <template #option="{ option }">
            <RoutingGroupOption :group="option as RoutingGroupChoice" />
          </template>
        </Select>
        <button v-if="modelValue" type="button" class="btn btn-secondary !px-2" :aria-label="t('keys.removeRouteGroup')" @click="remove(index)">×</button>
      </div>
      <div v-if="modelValue" class="mt-2 grid grid-cols-1 gap-2 sm:grid-cols-3">
        <label v-if="modelValue.strategy === 'priority_weighted'" class="text-xs">{{ t('keys.routePriority') }}<input class="input mt-1 w-full" type="number" min="1" max="1000000" :value="route.priority" @input="update(index, 'priority', Number(($event.target as HTMLInputElement).value))" /></label>
        <label v-if="modelValue.strategy === 'priority_weighted'" class="text-xs">{{ t('keys.routeWeight') }}<input class="input mt-1 w-full" type="number" min="1" max="1000000" :value="route.weight" @input="update(index, 'weight', Number(($event.target as HTMLInputElement).value))" /></label>
        <label class="text-xs">{{ t('keys.routeCooldown') }}<input class="input mt-1 w-full" type="number" min="0" max="86400" :value="route.cooldown_seconds" @input="update(index, 'cooldown_seconds', Number(($event.target as HTMLInputElement).value))" /></label>
      </div>
    </div>
    <template v-if="modelValue">
      <button type="button" class="btn btn-secondary text-sm" :disabled="!unusedGroup || current.routes.length >= 20" data-testid="routing-add" @click="add">{{ t('keys.addRouteGroup') }}</button>
      <label class="flex items-center gap-2 text-sm"><input type="checkbox" :checked="modelValue.failover" data-testid="routing-failover" @change="emit('update:modelValue', { ...cloneGroupRouting(current), failover: ($event.target as HTMLInputElement).checked })" />{{ t('keys.routingFailover') }}</label>
    </template>
  </fieldset>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Select from '@/components/common/Select.vue'
import RoutingGroupOption from './RoutingGroupOption.vue'
import type { RoutingGroupChoice } from './RoutingGroupOption.vue'
import { platformDisplayName } from '@/constants/platformCatalog'
import type { ApiKeyGroupRoute, ApiKeyGroupRouting } from '@/types'
import { cloneGroupRouting } from '@/utils/keyGroupRouting'

const props = defineProps<{
  modelValue: ApiKeyGroupRouting | null
  legacy: ApiKeyGroupRouting
  groups: RoutingGroupChoice[]
}>()
const emit = defineEmits<{ 'update:modelValue': [value: ApiKeyGroupRouting] }>()
const { t } = useI18n()
const current = computed(() => props.modelValue ?? props.legacy)
const platformFilter = ref('')
const platforms = computed(() => [...new Set(props.groups.map(group => group.platform).filter((platform): platform is string => !!platform))])
const filteredGroups = computed(() => props.groups.filter(group => !platformFilter.value || group.platform === platformFilter.value))
const unusedGroup = computed(() => filteredGroups.value.find(group => !current.value.routes.some(route => route.group_id === group.value)))
watch(platforms, available => {
  if (!available.includes(platformFilter.value)) platformFilter.value = ''
})
function platformLabel(platform: string) {
  return platform === 'composite' ? t('keys.routingCompositePlatform') : platformDisplayName(platform)
}
function optionsFor(id: number): RoutingGroupChoice[] {
  // Keep this row's selection visible even when it is outside the filter.
  // Filtering changes additions only, never the submitted routing policy.
  const options = props.groups.filter(group => group.value === id ||
    ((!platformFilter.value || group.platform === platformFilter.value) && !current.value.routes.some(route => route.group_id === group.value)))
  if (!options.some(group => group.value === id)) {
    options.unshift({ value: id, label: `#${id}`, disabled: true })
  }
  return options
}
function chooseStrategy(event: Event) {
  const strategy = (event.target as HTMLSelectElement).value as ApiKeyGroupRouting['strategy']
  if (strategy !== 'lowest_rate' && strategy !== 'priority_weighted') return
  emit('update:modelValue', { ...cloneGroupRouting(current.value), strategy })
}
function update<K extends keyof ApiKeyGroupRoute>(index: number, field: K, value: ApiKeyGroupRoute[K]) {
  if (!props.modelValue) return
  const next = cloneGroupRouting(current.value)
  next.routes[index][field] = value
  emit('update:modelValue', next)
}
function remove(index: number) {
  const next = cloneGroupRouting(current.value)
  next.routes.splice(index, 1)
  emit('update:modelValue', next)
}
function add() {
  if (!unusedGroup.value || current.value.routes.length >= 20) return
  const next = cloneGroupRouting(current.value)
  next.routes.push({ group_id: unusedGroup.value.value, priority: 1, weight: 1, enabled: true, cooldown_seconds: 0 })
  emit('update:modelValue', next)
}
</script>

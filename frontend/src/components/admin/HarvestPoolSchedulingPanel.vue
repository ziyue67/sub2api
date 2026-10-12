<template>
  <div>
    <p v-if="error" role="alert" class="mt-3 text-sm text-red-600">{{ error }}</p>
    <button v-if="!draft && !loading" type="button" class="btn btn-secondary" @click="load">{{ t("common.refresh") }}</button>
    <fieldset v-if="draft" :disabled="saving || loading" class="mt-5 min-w-0 space-y-3 border-t border-gray-200 pt-5 dark:border-dark-600" data-testid="pool-scheduling-panel">
      <div class="flex items-start justify-between gap-4">
        <div>
          <h2 class="font-semibold">{{ t(`${p}.accountScheduling`) }}</h2>
          <p class="mt-2 text-sm text-gray-500">{{ t(`${p}.ticketPoolSchedulingHint`) }}</p>
        </div>
        <Toggle :model-value="draft.ticket_pool_scheduling ?? false" @update:model-value="draft.ticket_pool_scheduling = $event" data-testid="account-scheduling-toggle" :aria-label="t(`${p}.accountScheduling`)" />
      </div>
      <div class="mt-4 space-y-3">
        <label class="block text-sm">{{ t(`${p}.schedulingMode`) }}
          <select v-model="draft.scheduling_mode" class="input mt-1" data-testid="scheduling-mode">
            <option value="model">{{ t(`${p}.modeModel`) }}</option>
            <option value="account">{{ t(`${p}.modeAccount`) }}</option>
            <option value="groups">{{ t(`${p}.modeGroups`) }}</option>
          </select>
        </label>
        <div v-if="draft.scheduling_mode === 'groups'" data-testid="scheduling-groups">
          <p class="text-sm">{{ t(`${p}.schedulingGroups`) }}</p>
          <p v-if="groupsError" class="text-sm text-red-600">{{ groupsError }}</p>
          <div class="mt-2 flex flex-wrap gap-x-6 gap-y-2">
            <label v-for="group in groups" :key="group.id" class="flex max-w-full cursor-pointer items-start gap-2 py-1 text-sm">
              <input v-model="draft.scheduling_group_ids" class="mt-0.5 shrink-0" type="checkbox" :value="group.id" />
              <span class="min-w-0 break-words">{{ group.name }} (#{{ group.id }})</span>
            </label>
          </div>
        </div>
      </div>
      <p v-if="validation" role="alert" class="mt-3 text-sm text-amber-700">{{ validation }}</p>
      <p v-if="savedMessage" role="status" class="mt-3 text-sm text-emerald-600">{{ t(`${p}.saved`) }}</p>
      <div class="mt-4 flex gap-2">
        <button type="button" class="btn btn-primary btn-sm" data-testid="save-scheduling" :disabled="!dirty || saving || loading || !!validation" @click="save">{{ t(`${p}.save`) }}</button>
        <button type="button" class="btn btn-secondary btn-sm" :disabled="saving || loading" @click="load">{{ t(dirty ? `${p}.discard` : 'common.refresh') }}</button>
      </div>
    </fieldset>

  </div>
</template>
<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Toggle from '@/components/common/Toggle.vue'
import { getAllIncludingInactive } from '@/api/admin/groups'
import { getAstraGateway, saveAstraGateway, normalizeAstraGateway, type AstraGatewaySettings } from '@/api/admin/astraGateway'
const { t } = useI18n()
const p = 'admin.astraGateway'
const draft = ref<AstraGatewaySettings>()
const saved = ref<AstraGatewaySettings>()
const loading = ref(false)
const saving = ref(false)
const savedMessage = ref(false)
const error = ref('')
const groupsError = ref('')
const groups = ref<{ id: number; name: string }[]>([])
const dirty = computed(() => JSON.stringify(draft.value) !== JSON.stringify(saved.value))
const validation = computed(() => draft.value?.ticket_pool_scheduling && draft.value.scheduling_mode === 'groups' && !draft.value.scheduling_group_ids?.length ? t(`${p}.chooseSchedulingGroups`) : '')
async function load() {
  loading.value = true
  error.value = ''
  savedMessage.value = false
  const [settings, groupList] = await Promise.allSettled([getAstraGateway(), getAllIncludingInactive()])
  if (settings.status === 'fulfilled') {
    saved.value = normalizeAstraGateway(settings.value)
    draft.value = normalizeAstraGateway(settings.value)
  } else error.value = t(`${p}.loadError`)
  if (groupList.status === 'fulfilled') { groups.value = groupList.value; groupsError.value = '' }
  else groupsError.value = t(`${p}.loadError`)
  loading.value = false
}
async function save() {
  if (!draft.value || !dirty.value || saving.value || loading.value || validation.value) return
  saving.value = true
  error.value = ''
  savedMessage.value = false
  try {
    // Read routing settings again so this panel saves only its scheduling fields.
    const latest = await getAstraGateway()
    const value = await saveAstraGateway({ ...latest, account_scheduling: latest.account_scheduling, scheduling_mode: draft.value.scheduling_mode, scheduling_group_ids: [...(draft.value.scheduling_group_ids || [])], ticket_pool_scheduling: draft.value.ticket_pool_scheduling ?? false })
    saved.value = normalizeAstraGateway(value)
    draft.value = normalizeAstraGateway(value)
    savedMessage.value = true
  } catch { error.value = t(`${p}.saveError`) }
  finally { saving.value = false }
}
onMounted(load)
</script>

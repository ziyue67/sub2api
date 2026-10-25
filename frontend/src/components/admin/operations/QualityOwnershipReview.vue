<template>
  <section class="space-y-3 rounded border p-3" data-testid="quality-ownership-review">
    <h4>{{ t('qualityOps.ownershipTitle') }}</h4>
    <p>{{ t('qualityOps.ownershipNotUnlock') }}</p>
    <p v-if="scopeConflict" role="alert" data-testid="quality-scope-conflict">{{ t('qualityOps.scopeConflictHelp') }}</p>
    <p v-if="error" role="alert">{{ error }}</p>
    <button type="button" class="btn btn-secondary" :disabled="busy" @click="load">{{ t('qualityOps.previewOwnership') }}</button>
    <template v-if="preview">
      <p>{{ t('qualityOps.savedScope') }}: {{ preview.models.join(', ') || '—' }} · {{ preview.group_ids.join(', ') || '—' }}</p>
      <p v-if="preview.ownership_retained">{{ t('qualityOps.managedEntriesRemain') }}</p>
      <div class="overflow-auto">
        <table class="w-full text-left text-xs"><thead><tr><th>{{ t('qualityOps.ownershipTarget') }}</th><th>{{ t('qualityOps.ownershipExpected') }}</th><th>{{ t('qualityOps.ownershipCurrent') }}</th><th>{{ t('qualityOps.ownershipDecision') }}</th></tr></thead>
          <tbody><tr v-for="entry in preview.entries" :key="`${entry.kind}:${entry.model || entry.group_id}`">
            <td>{{ entry.model || `#${entry.group_id}` }}</td>
            <td>{{ t(`qualityOps.ownershipOwners.${entry.expected_owner}`) }} · {{ t(entry.expected_present ? 'qualityOps.present' : 'qualityOps.absent') }}</td>
            <td>{{ t(`qualityOps.ownershipOwners.${entry.current_owner}`) }}<span v-if="entry.current_owner_rule_id"> #{{ entry.current_owner_rule_id }}</span> · {{ t(entry.current_present ? 'qualityOps.present' : 'qualityOps.absent') }}</td>
            <td>{{ t(`qualityOps.ownershipStates.${entry.status}`) }} · {{ t(entry.can_relinquish ? 'qualityOps.canRelinquish' : 'qualityOps.retainOwnership') }}
              <details><summary>{{ t('qualityOps.restoreDiagnosticEvidence') }}</summary><p>{{ entry.changed_fields.join(', ') || '—' }}</p><code>{{ entry.expected_digest }} → {{ entry.current_digest }}</code></details>
            </td>
          </tr></tbody>
        </table>
      </div>
      <p v-if="reconciled" role="status">{{ t('qualityOps.ownershipReconciled') }}</p>
      <template v-if="preview.relinquishable_count && !reconciled">
        <label class="flex items-start gap-2"><input v-model="confirmed" type="checkbox" data-testid="quality-ownership-confirm" :disabled="busy" />{{ t('qualityOps.ownershipConfirm') }}</label>
        <button type="button" class="btn btn-secondary" data-testid="quality-ownership-reconcile" :disabled="busy || !confirmed || !preview.fence" @click="reconcile">{{ t('qualityOps.relinquishOldOwnership') }}</button>
      </template>
      <p>{{ t('qualityOps.keepDraftHelp') }}</p>
    </template>
  </section>
</template>
<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { previewQualityOwnership, reconcileQualityOwnership, type QualityOwnershipPreview } from '@/api/admin/accountQuality'
const props = defineProps<{ planId: number; draftModels?: string[]; draftGroupIds?: number[]; draftAction?: string }>()
const emit = defineEmits<{ reconciled: []; blocked: [value: boolean] }>()
const { t } = useI18n()
const preview = ref<QualityOwnershipPreview | null>(null), busy = ref(false), error = ref(''), confirmed = ref(false), reconciled = ref(false)
let generation = 0
onBeforeUnmount(() => { generation++ })
const same = (a: readonly (string | number)[], b: readonly (string | number)[]) => JSON.stringify([...new Set(a)].sort()) === JSON.stringify([...new Set(b)].sort())
const scopeConflict = computed(() => !!preview.value?.action && preview.value.action === 'isolate_models' && props.draftAction !== undefined &&
  (props.draftAction !== 'isolate_models' || !same(preview.value.models, props.draftModels || []) || !same(preview.value.group_ids, props.draftGroupIds || [])))
watch(scopeConflict, value => emit('blocked', value), { immediate: true })
function failure(cause: unknown) {
  const response = (cause as { response?: { status?: number; data?: { reason?: string } } })?.response
  return response?.status === 409 ? t('qualityOps.ownershipPreviewChanged') : t('qualityOps.ownershipLoadFailed')
}
async function load() {
  const current = ++generation, id = props.planId
  busy.value = true; error.value = ''; confirmed.value = false; reconciled.value = false; preview.value = null
  try { const data = await previewQualityOwnership(id); if (current === generation) preview.value = data }
  catch (cause) { if (current === generation) error.value = failure(cause) }
  finally { if (current === generation) busy.value = false }
}
async function reconcile() {
  if (busy.value || !confirmed.value || !preview.value?.fence) return
  const current = ++generation, id = props.planId, fence = preview.value.fence
  busy.value = true; error.value = ''; confirmed.value = false
  try {
    await reconcileQualityOwnership(id, fence)
    if (current !== generation) return
    const refreshed = await previewQualityOwnership(id)
    if (current !== generation) return
    preview.value = refreshed
    reconciled.value = true; emit('reconciled')
  } catch (cause) { if (current === generation) { error.value = failure(cause); preview.value = null } }
  finally { if (current === generation) busy.value = false }
}
watch(() => props.planId, () => { void load() }, { immediate: true })
</script>

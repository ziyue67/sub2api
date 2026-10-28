<template>
  <section class="space-y-3 rounded border p-3" data-testid="quality-ownership-review">
    <h4>{{ t(deleting ? 'qualityOps.deleteOwnershipTitle' : 'qualityOps.ownershipTitle') }}</h4>
    <p>{{ t(deleting ? 'qualityOps.deleteRestoreHelp' : 'qualityOps.ownershipNotUnlock') }}</p>
    <p v-if="scopeConflict" role="alert" data-testid="quality-scope-conflict">{{ t('qualityOps.scopeConflictHelp') }}</p>
    <p v-if="error" role="alert">{{ error }}</p>
    <button type="button" class="btn btn-secondary" :disabled="busy" @click="load">{{ t('qualityOps.previewOwnership') }}</button>
    <template v-if="preview">
      <p>{{ t('qualityOps.savedScope') }}: {{ preview.models.join(', ') || '—' }} · {{ preview.group_ids.join(', ') || '—' }}</p>
      <p v-if="preview.ownership_retained">{{ t(deleting ? 'qualityOps.deleteManagedEntries' : 'qualityOps.managedEntriesRemain') }}</p>
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
      <fieldset v-if="!deleting && preview.model_removals?.length" class="space-y-2 border p-2" :disabled="busy" data-testid="quality-model-removal">
        <legend>{{ t('qualityOps.modelRemovalTitle') }}</legend>
        <p>{{ t('qualityOps.modelRemovalHelp') }}</p>
        <label>{{ t('qualityOps.ownershipTarget') }} <select v-model="removalModel" data-testid="quality-removal-model"><option value="">—</option><option v-for="item in preview.model_removals" :key="item.model" :value="item.model">{{ item.model }}</option></select></label>
        <template v-if="selectedRemoval">
          <p data-testid="quality-removal-effect">{{ t(`qualityOps.modelRemovalEffects.${selectedRemoval.effect}`) }} · {{ selectedRemoval.isolation_model }}</p>
          <p v-if="!selectedRemoval.ready" role="alert" data-testid="quality-removal-blocked">{{ t(`qualityOps.modelRemovalBlocked.${selectedRemoval.blocked_reason}`) }}</p>
          <template v-else>
            <label>{{ t('qualityOps.modelRemovalReason') }} <select v-model="removalReason" data-testid="quality-removal-reason"><option value="">—</option><option value="orphan_marker_recovery">{{ t('qualityOps.modelRemovalReasons.orphan_marker_recovery') }}</option><option value="retire_model">{{ t('qualityOps.modelRemovalReasons.retire_model') }}</option></select></label>
            <label class="flex gap-2"><input v-model="removalConfirmed" type="checkbox" data-testid="quality-removal-confirm" />{{ t('qualityOps.modelRemovalConfirm', { model: removalModel }) }}</label>
            <button type="button" class="btn btn-secondary" data-testid="quality-remove-model" :disabled="busy || !removalConfirmed || !removalReason || !preview.fence" @click="removeModel">{{ t('qualityOps.modelRemovalSubmit') }}</button>
          </template>
        </template>
      </fieldset>
      <p v-if="removedModel" role="status" data-testid="quality-removal-readback">{{ t('qualityOps.modelRemovalReadback', { model: removedModel }) }}</p>
      <p v-if="reconciled" role="status">{{ t('qualityOps.ownershipReconciled') }}</p>
      <template v-if="preview.relinquishable_count && !reconciled">
        <label class="flex items-start gap-2"><input v-model="confirmed" type="checkbox" data-testid="quality-ownership-confirm" :disabled="busy" />{{ t('qualityOps.ownershipConfirm') }}</label>
        <button type="button" class="btn btn-secondary" data-testid="quality-ownership-reconcile" :disabled="busy || !confirmed || !preview.fence" @click="reconcile">{{ t('qualityOps.relinquishOldOwnership') }}</button>
      </template>
      <template v-if="deleting && preview.action === 'isolate_models'">
        <p v-if="!preview.restore_ready" role="alert" data-testid="quality-delete-restore-blocked">{{ t(`qualityOps.deleteRestoreBlocked.${preview.restore_blocked_reason || 'ownership_conflict'}`) }}</p>
        <template v-else>
          <label class="flex items-start gap-2"><input v-model="restoreConfirmed" type="checkbox" data-testid="quality-restore-confirm" :disabled="busy" />{{ t('qualityOps.deleteRestoreConfirm') }}</label>
          <button type="button" class="btn btn-secondary" data-testid="quality-restore-owned" :disabled="busy || !restoreConfirmed || !preview.fence" @click="restore">{{ t('qualityOps.restoreBeforeDelete') }}</button>
        </template>
      </template>
      <p>{{ t(deleting ? 'qualityOps.deleteAfterRestoreHelp' : 'qualityOps.keepDraftHelp') }}</p>
    </template>
  </section>
</template>
<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { previewQualityOwnership, reconcileQualityOwnership, restoreQualityOwnership, removeQualityOwnedModel, type QualityOwnershipPreview } from '@/api/admin/accountQuality'
const props = defineProps<{ planId: number; draftModels?: string[]; draftGroupIds?: number[]; draftAction?: string; deleting?: boolean; disabled?: boolean }>()
const emit = defineEmits<{ reconciled: []; blocked: [value: boolean]; deletable: [value: boolean]; deletionFence: [fence: string]; modelRemoved: [model: string]; working: [value: boolean] }>()
const { t } = useI18n()
const preview = ref<QualityOwnershipPreview | null>(null), working = ref(false), error = ref(''), confirmed = ref(false), reconciled = ref(false)
const restoreConfirmed = ref(false)
const removalModel = ref(''), removalReason = ref(''), removalConfirmed = ref(false), removedModel = ref('')
const selectedRemoval = computed(() => preview.value?.model_removals?.find(item => item.model === removalModel.value))
watch([removalModel, removalReason], () => { removalConfirmed.value = false })
watch(working, value => emit('working', value))
const busy = computed(() => working.value || !!props.disabled)
watch(() => !!preview.value?.fence && preview.value.action !== 'isolate_models' && !working.value && !error.value, value => { emit('deletable', value); emit('deletionFence', value ? preview.value?.fence || '' : '') }, { immediate: true })
let generation = 0
onBeforeUnmount(() => { generation++; emit('working', false) })
const same = (a: readonly (string | number)[], b: readonly (string | number)[]) => JSON.stringify([...new Set(a)].sort()) === JSON.stringify([...new Set(b)].sort())
const scopeConflict = computed(() => !!preview.value?.action && preview.value.action === 'isolate_models' && props.draftAction !== undefined &&
  (props.draftAction !== 'isolate_models' || !same(preview.value.models, props.draftModels || []) || !same(preview.value.group_ids, props.draftGroupIds || [])))
watch(scopeConflict, value => emit('blocked', value), { immediate: true })
function failure(cause: unknown) {
  const err = cause as { status?: number; response?: { status?: number } }
  return (err.status ?? err.response?.status) === 409 ? t('qualityOps.ownershipPreviewChanged') : t('qualityOps.ownershipLoadFailed')
}
async function load() {
  const current = ++generation, id = props.planId
  working.value = true; error.value = ''; confirmed.value = restoreConfirmed.value = removalConfirmed.value = false; reconciled.value = false; preview.value = null; removedModel.value = ''; removalModel.value = ''; removalReason.value = ''
  try { const data = await previewQualityOwnership(id); if (current === generation) preview.value = data }
  catch (cause) { if (current === generation) error.value = failure(cause) }
  finally { if (current === generation) working.value = false }
}
async function reconcile() {
  if (busy.value || !confirmed.value || !preview.value?.fence) return
  const current = ++generation, id = props.planId, fence = preview.value.fence
  working.value = true; error.value = ''; confirmed.value = false
  try {
    await reconcileQualityOwnership(id, fence)
    if (current !== generation) return
    const refreshed = await previewQualityOwnership(id)
    if (current !== generation) return
    preview.value = refreshed
    reconciled.value = true; emit('reconciled')
  } catch (cause) { if (current === generation) { error.value = failure(cause); preview.value = null } }
  finally { if (current === generation) working.value = false }
}
async function restore() {
  if (busy.value || !restoreConfirmed.value || !preview.value?.restore_ready || !preview.value.fence) return
  const current = ++generation, id = props.planId, fence = preview.value.fence
  working.value = true; error.value = ''; restoreConfirmed.value = false; confirmed.value = false
  try {
    await restoreQualityOwnership(id, fence)
    if (current !== generation) return
    const refreshed = await previewQualityOwnership(id)
    if (current !== generation) return
    preview.value = refreshed
    emit('reconciled')
  } catch (cause) { if (current === generation) { error.value = failure(cause); preview.value = null } }
  finally { if (current === generation) working.value = false }
}
async function removeModel() {
  if (busy.value || !selectedRemoval.value?.ready || !removalConfirmed.value || !removalReason.value || !preview.value?.fence) return
  const current = ++generation, id = props.planId, model = removalModel.value, fence = preview.value.fence
  working.value = true; error.value = ''; removalConfirmed.value = false; confirmed.value = false
  try {
    await removeQualityOwnedModel(id, fence, model, removalReason.value)
    if (current !== generation) return
    const refreshed = await previewQualityOwnership(id)
    if (current !== generation) return
    if (refreshed.models.includes(model) || refreshed.entries.some(entry => entry.kind === 'model' && entry.model === selectedRemoval.value?.isolation_model)) throw new Error('readback_failed')
    preview.value = refreshed; removedModel.value = model; removalModel.value = ''; removalReason.value = ''
    emit('modelRemoved', model); emit('reconciled')
  } catch (cause) { if (current === generation) { error.value = failure(cause) === t('qualityOps.ownershipPreviewChanged') ? t('qualityOps.ownershipPreviewChanged') : t('qualityOps.modelRemovalReadbackFailed'); preview.value = null } }
  finally { if (current === generation) working.value = false }
}
watch(() => props.planId, () => { void load() }, { immediate: true })
</script>

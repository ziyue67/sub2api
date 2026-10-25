<template>
  <section class="card space-y-3 p-4" data-test="moderation-user-summary">
    <h2 class="font-semibold">{{ t('admin.moderationUsers.listTitle') }}</h2>
    <div class="flex flex-wrap gap-2" role="tablist" :aria-label="t('admin.moderationUsers.listTitle')">
      <button v-for="tab in tabs" :key="tab" :data-test="`filter-${tab}`" type="button" role="tab" class="btn btn-secondary" :aria-selected="filter === tab" @click="filter = tab; page = 1; load()">{{ t(`admin.moderationUsers.${tab}`) }}</button>
    </div>
    <form class="flex flex-wrap items-end gap-2" @submit.prevent="page = 1; load()">
      <label class="text-sm">UID<input v-model="uid" type="number" min="1" step="1" class="input" /></label>
      <button type="submit" class="btn btn-secondary">{{ t('common.search') }}</button>
      <button type="button" class="btn btn-secondary" @click="load">{{ t('common.refresh') }}</button>
    </form>
    <p v-if="error" role="alert" class="text-red-600">{{ error }}</p>
    <p v-else-if="busy" role="status">{{ t('common.loading') }}</p>
    <p v-else-if="items.length === 0" data-test="empty">{{ t('admin.moderationUsers.empty') }}</p>
    <div v-else class="overflow-x-auto">
      <table class="w-full text-left text-sm">
        <thead><tr><th>UID</th><th>{{ t('admin.moderationUsers.exemptionTypes') }}</th><th>{{ t('admin.moderationUsers.state') }}</th><th>{{ t('admin.moderationUsers.reason') }}</th><th>{{ t('admin.moderationUsers.actor') }}</th><th>{{ t('admin.moderationUsers.time') }}</th><th></th></tr></thead>
        <tbody><tr v-for="item in items" :key="item.user_id">
          <td>{{ item.user_id }}</td><td>{{ exemptionLabels(item).join(' · ') || t('common.no') }}</td>
          <td>{{ item.status }} · {{ item.current_count }}</td><td class="max-w-xs break-words">{{ item.reason || '—' }}</td><td>{{ item.actor_id ?? '—' }}</td><td>{{ item.updated_at ? new Date(item.updated_at).toLocaleString() : '—' }}</td>
          <td><button type="button" class="btn btn-secondary" @click="selected = item.user_id">{{ t('common.edit') }}</button></td>
        </tr></tbody>
      </table>
    </div>
    <div class="flex flex-wrap items-center gap-2">
      <button type="button" class="btn btn-secondary" data-test="previous" :disabled="busy || page <= 1" @click="page--; load()">{{ t('pagination.previous') }}</button>
      <span>{{ page }} / {{ Math.max(1, Math.ceil(total / 20)) }} · {{ total }}</span>
      <button type="button" class="btn btn-secondary" data-test="next" :disabled="busy || page * 20 >= total" @click="page++; load()">{{ t('common.next') }}</button>
    </div>
    <BaseDialog :show="selected !== null" :title="t('admin.moderationUsers.title')" @close="selected = null">
      <ModerationUserPanel v-if="selected !== null" :key="selected" :user-id="selected" @changed="load" />
    </BaseDialog>
  </section>
</template>
<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ModerationUserPanel from './ModerationUserPanel.vue'
import { listModerationUsers, type ModerationUserState } from '@/api/admin/riskControl'
const { t } = useI18n()
const tabs = ['exempt', 'all', 'enforced', 'auto_ban_exempt', 'keyword_exempt', 'api_audit_exempt', 'email_exempt'] as const
const filter = ref<typeof tabs[number]>('exempt')
function exemptionLabels(item: ModerationUserState) {
  return [
    item.auto_ban_exempt && t('admin.moderationUsers.exemption'),
    item.keyword_exempt && t('admin.moderationUsers.keywordExemption'),
    item.api_audit_exempt && t('admin.moderationUsers.apiAuditExemption'),
    item.email_exempt && t('admin.moderationUsers.emailExemption'),
  ].filter(Boolean)
}
const items = ref<ModerationUserState[]>([])
const page = ref(1), total = ref(0), uid = ref(''), busy = ref(false), error = ref('')
const selected = ref<number | null>(null)
let version = 0
async function load() {
  const current = ++version
  busy.value = true; error.value = ''; items.value = []
  try {
    const result = await listModerationUsers({ page: page.value, page_size: 20, state: filter.value, ...(uid.value ? { user_id: Number(uid.value) } : {}) })
    if (current === version) { items.value = result.items; total.value = result.total }
  } catch (err) {
    const value = err as { response?: { data?: { message?: string } }; message?: string }
    if (current === version) { error.value = value.response?.data?.message || value.message || t('admin.moderationUsers.failed'); total.value = 0 }
  } finally { if (current === version) busy.value = false }
}
onMounted(load)
</script>

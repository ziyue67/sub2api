<template>
  <div class="space-y-2">
    <GroupSelector :model-value="modelValue" :groups="groups" :label="t('admin.users.observerGroups')"
      @update:model-value="$emit('update:modelValue', $event)" />
    <p class="input-hint">{{ t('admin.users.observerGroupsHint') }}</p>
    <p v-if="error" class="text-sm text-red-500">{{ error }}</p>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import type { Group } from '@/types'
import GroupSelector from '@/components/common/GroupSelector.vue'

defineProps<{ modelValue: number[] }>()
defineEmits<{ 'update:modelValue': [value: number[]] }>()
const { t } = useI18n()
const groups = ref<Group[]>([])
const error = ref('')
onMounted(async () => {
  try {
    groups.value = await adminAPI.groups.getAllIncludingInactive()
  } catch {
    error.value = t('common.error')
  }
})
</script>

<template>
  <AdminUsageView v-if="authStore.isObserver" observer-mode />
  <UserUsageView v-else />
</template>

<script setup lang="ts">
import { useAuthStore } from '@/stores/auth'
import { defineAsyncComponent } from 'vue'
const AdminUsageView = defineAsyncComponent(() => import('@/views/admin/UsageView.vue'))
const UserUsageView = defineAsyncComponent(() => import('@/views/user/UsageView.vue'))

const authStore = useAuthStore()
</script>

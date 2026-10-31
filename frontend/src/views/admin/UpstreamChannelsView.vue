<template><AppLayout><div class="space-y-4"><SmartOpsNav /><UpstreamChannelManager expanded /><a class="btn btn-secondary" href="/admin/accounts">{{ t('upstreamEntities.accountManagement') }}</a><a class="btn btn-secondary ml-2" href="/admin/profit-operations">{{ t('profitOps.title') }}</a></div></AppLayout></template>
<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import SmartOpsNav from '@/components/admin/operations/SmartOpsNav.vue'
import UpstreamChannelManager from '@/components/admin/operations/UpstreamChannelManager.vue'
const { t } = useI18n()
</script>

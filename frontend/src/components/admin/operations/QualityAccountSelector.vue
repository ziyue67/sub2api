<template>
  <div class="space-y-3">
    <label for="quality-account-search" class="block text-sm font-medium">{{ t('qualityOps.accounts') }}</label>
    <div class="grid gap-3 sm:grid-cols-2">
      <label class="space-y-1 text-sm"><span>{{ t('qualityOps.accountGroup') }}</span>
        <select v-model="accountGroup" class="input" data-testid="quality-account-group" @change="emit('search', 1)">
          <option value="">{{ t('qualityOps.allAccountGroups') }}</option>
          <option value="ungrouped">{{ t('qualityOps.ungroupedAccounts') }}</option>
          <option v-for="group in groups" :key="group.id" :value="String(group.id)">{{ group.name }} #{{ group.id }}</option>
        </select>
      </label>
      <label class="space-y-1 text-sm"><span>{{ t('qualityOps.accountType') }}</span>
        <select v-model="accountType" class="input" data-testid="quality-account-type" @change="emit('search', 1)">
          <option value="">{{ t('qualityOps.allAccountTypes') }}</option>
          <option value="oauth">{{ t('qualityOps.oauthAccounts') }}</option>
          <option value="setup-token">{{ t('qualityOps.setupTokenAccounts') }}</option>
          <option value="apikey">{{ t('qualityOps.apiKeyAccounts') }}</option>
        </select>
      </label>
    </div>
    <div class="flex gap-2"><input id="quality-account-search" v-model="search" class="input min-w-0" :placeholder="t('qualityOps.search')" @keydown.enter.prevent="emit('search', 1)" /><button type="button" class="btn btn-secondary shrink-0 whitespace-nowrap" @click="emit('search', 1)">{{ t('qualityOps.search') }}</button></div>
    <div class="flex flex-wrap items-center gap-3 text-sm">
      <button type="button" class="text-primary-600 disabled:opacity-50" data-testid="quality-select-page" :disabled="accountsLoading || selectingAccounts || !accounts.some(account => !disabledReason(account))" @click="emit('selectPage')">{{ t('qualityOps.selectAccountPage') }}</button>
      <button type="button" class="text-primary-600 disabled:opacity-50" data-testid="quality-select-all" :disabled="accountsLoading || selectingAccounts || !accounts.length" @click="emit('selectAll')">{{ t(selectingAccounts ? 'qualityOps.selectingAccounts' : 'qualityOps.selectMatchingAccounts') }}</button>
      <button type="button" class="text-gray-500 disabled:opacity-50" data-testid="quality-clear-selection" :disabled="!selectedAccounts.length && !selectingAccounts" @click="emit('clear')">{{ t('qualityOps.clearAccountSelection') }}</button>
    </div>
    <p class="text-xs text-gray-500">{{ t(bulk ? 'qualityOps.bulkAccountSelectionHint' : 'qualityOps.accountSelectionHint') }}</p>
    <p v-if="accountsError" role="alert" class="text-sm text-red-600">{{ accountsError }}</p>
    <p v-if="accountsLoading" role="status" class="text-sm text-gray-500">{{ t('qualityOps.loading') }}</p>
    <div class="grid max-h-52 gap-2 overflow-auto rounded border p-3 sm:grid-cols-2 dark:border-dark-600" :aria-busy="accountsLoading || selectingAccounts">
      <label v-for="account in accounts" :key="account.id" class="flex items-center gap-2 text-sm"><input v-model="selectedAccounts" type="checkbox" :value="account.id" :disabled="selectingAccounts || !!disabledReason(account)" /><span class="min-w-0 break-all">{{ account.name }} <span class="text-gray-500">#{{ account.id }}</span><span v-if="disabledReason(account)" class="ml-1 text-xs text-gray-500">{{ disabledReason(account) }}</span></span></label>
      <p v-if="!accountsLoading && !accounts.length" class="text-sm text-gray-500 sm:col-span-2">{{ t('qualityOps.noMatchingAccounts') }}</p>
    </div>
    <div class="flex flex-wrap items-center gap-3 text-sm"><button type="button" :aria-label="t('qualityOps.previousAccountPage')" :disabled="accountsLoading || selectingAccounts || accountPage <= 1" @click="emit('search', accountPage - 1)">←</button><span>{{ accountPage }} / {{ accountPages }}</span><button type="button" :aria-label="t('qualityOps.nextAccountPage')" :disabled="accountsLoading || selectingAccounts || accountPage >= accountPages" @click="emit('search', accountPage + 1)">→</button><span aria-live="polite">{{ t('qualityOps.selected', { count: selectedAccounts.length }) }}</span></div>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { AccountListItem, AdminGroup } from '@/types'

const { t } = useI18n()
const selectedAccounts = defineModel<number[]>({ required: true })
const search = defineModel<string>('search', { required: true })
const accountGroup = defineModel<string>('group', { required: true })
const accountType = defineModel<string>('type', { required: true })
defineProps<{
  accounts: AccountListItem[]
  groups: AdminGroup[]
  accountsLoading: boolean
  selectingAccounts: boolean
  accountsError: string
  accountPage: number
  accountPages: number
  bulk: boolean
  disabledReason: (account: AccountListItem) => string
}>()
const emit = defineEmits<{
  search: [page: number]
  selectPage: []
  selectAll: []
  clear: []
}>()
</script>

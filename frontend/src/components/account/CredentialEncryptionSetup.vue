<template>
  <section class="rounded-xl border border-gray-200 bg-white p-4 dark:border-dark-600 dark:bg-dark-800" data-testid="credential-encryption-setup" :aria-busy="busy">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <div>
        <h3 class="font-semibold text-gray-900 dark:text-gray-100">{{ t('tokenGuardV2.encryption.title') }}</h3>
        <p class="mt-1 text-sm text-gray-500 dark:text-gray-400" role="status">{{ t(status?.configured ? 'tokenGuardV2.encryption.ready' : busy ? 'tokenGuardV2.encryption.checking' : 'tokenGuardV2.encryption.required') }}</p>
      </div>
      <button v-if="error" type="button" class="btn btn-secondary" :disabled="busy" @click="check">{{ t('tokenGuardV2.encryption.retry') }}</button>
      <button v-else-if="status && !status.configured" type="button" class="btn btn-primary" :disabled="busy" data-testid="initialize-credential-encryption" @click="initialize">{{ t(busy ? 'tokenGuardV2.encryption.initializing' : 'tokenGuardV2.encryption.initialize') }}</button>
    </div>
    <p v-if="error" role="alert" class="mt-2 text-sm text-red-600 dark:text-red-400">{{ error }}</p>
    <p v-if="status && !status.configured" class="mt-2 text-sm text-gray-500 dark:text-gray-400">{{ t('tokenGuardV2.encryption.description') }}</p>
    <p v-if="status?.source === 'local_file'" class="mt-2 text-xs text-gray-500 dark:text-gray-400">{{ t('tokenGuardV2.encryption.backupHint') }}</p>
  </section>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { getCredentialEncryption, initializeCredentialEncryption, type CredentialEncryptionStatus } from '@/api/admin/credentialEncryption'

const emit = defineEmits<{ ready: [value: boolean] }>()
const { t } = useI18n()
const status = ref<CredentialEncryptionStatus | null>(null)
const busy = ref(false)
const error = ref('')

async function request(initialize: boolean) {
  if (busy.value) return
  busy.value = true
  error.value = ''
  emit('ready', false)
  try {
    status.value = await (initialize ? initializeCredentialEncryption() : getCredentialEncryption())
    emit('ready', status.value.configured)
  } catch {
    status.value = null
    error.value = t(initialize ? 'tokenGuardV2.encryption.initializeFailed' : 'tokenGuardV2.encryption.loadFailed')
  } finally {
    busy.value = false
  }
}
const check = () => request(false)
const initialize = () => request(true)
onMounted(check)
</script>

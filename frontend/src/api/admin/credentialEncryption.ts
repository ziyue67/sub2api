import { apiClient } from '../client'

export interface CredentialEncryptionStatus {
  configured: boolean
  source: 'unconfigured' | 'server_config' | 'local_file'
}

const path = '/admin/account-ops/token-guard-v2/encryption'

export async function getCredentialEncryption(): Promise<CredentialEncryptionStatus> {
  return (await apiClient.get(path)).data
}

export async function initializeCredentialEncryption(): Promise<CredentialEncryptionStatus> {
  return (await apiClient.post(`${path}/initialize`)).data
}

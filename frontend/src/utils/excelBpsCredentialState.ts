import { createSharedComposable, useTimestamp } from '@vueuse/core'
import type { ExcelBPSCredentialState } from '@/types'
export const useExcelCredentialClock = createSharedComposable(() => useTimestamp({ interval: 30_000 }))
export function excelCredentialStatus(state: ExcelBPSCredentialState, now: number): string {
  const expiry = state.expires_at ? Date.parse(state.expires_at) : NaN
  if (state.status !== 'revoked' && Number.isFinite(expiry) && expiry <= now) return 'expired'
  return ['unknown', 'pending', 'not_expired', 'expired', 'revoked', 'auth_failed'].includes(state.status) ? state.status : 'unknown'
}

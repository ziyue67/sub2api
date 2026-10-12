import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import ExcelBPSCredentialStatus from '../ExcelBPSCredentialStatus.vue'
import { excelCredentialStatus } from '@/utils/excelBpsCredentialState'
vi.mock('vue-i18n', async () => ({ ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'), useI18n: () => ({ t: (key: string) => key }) }))
describe('Excel BPS credential diagnosis', () => {
  it('distinguishes token expiry from verified upstream authentication', () => {
    expect(excelCredentialStatus({ status: 'not_expired', expires_at: '2026-10-11T00:00:00Z' }, Date.parse('2026-10-10T00:00:00Z'))).toBe('not_expired')
    expect(excelCredentialStatus({ status: 'not_expired', expires_at: '2026-10-11T00:00:00Z' }, Date.parse('2026-10-12T00:00:00Z'))).toBe('expired')
    expect(excelCredentialStatus({ status: 'revoked', expires_at: '2026-10-11T00:00:00Z' }, Date.parse('2026-10-12T00:00:00Z'))).toBe('revoked')
  })
  it('shows the safe cause and preserves the manual pause instruction', () => {
    const wrapper = mount(ExcelBPSCredentialStatus, { props: { state: { status: 'revoked', error_code: 'token_revoked', requires_manual_resume: true } } })
    expect(wrapper.text()).toContain('bpsCredentialState.revoked')
    expect(wrapper.text()).toContain('token_revoked')
    expect(wrapper.text()).toContain('bpsCredentialState.manualResume')
    wrapper.unmount()
  })
})

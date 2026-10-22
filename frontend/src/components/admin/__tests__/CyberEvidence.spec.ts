import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import CyberEvidencePanel from '../CyberEvidencePanel.vue'
import CyberEvidenceSettings from '../CyberEvidenceSettings.vue'

const api = vi.hoisted(() => ({ getEvidence: vi.fn(), exportEvidence: vi.fn(), deleteEvidence: vi.fn(), getEvidenceConfig: vi.fn(), updateEvidenceConfig: vi.fn() }))
vi.mock('@/api/admin/cyberEvidence', () => api)
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key, te: () => true }) }))
const fixture = () => ({ log_id: 7, user_id: 42, request_id: 'req_synthetic', endpoint: '/v1/chat/completions', model: 'synthetic', occurred_at: '2026-10-22T00:00:00Z', status: 'saved', payload: { input: '{"messages":[{"role":"user","content":"SYNTHETIC INPUT"}]}', upstream_reason: 'SYNTHETIC UPSTREAM ERROR', source: 'upstream_error_code:cyber_policy', redaction_version: 'cyber-evidence-v1', original_bytes: 120, truncated: true } })

describe('Cyber evidence on existing audit detail', () => {
  beforeEach(() => { vi.clearAllMocks() })
  it('reads only on demand and displays input and upstream reason separately as text', async () => {
    api.getEvidence.mockResolvedValue(fixture())
    const w = mount(CyberEvidencePanel, { props: { logId: 7 } })
    expect(api.getEvidence).not.toHaveBeenCalled()
    await w.get('button').trigger('click'); await flushPromises()
    expect(api.getEvidence).toHaveBeenCalledWith(7)
    const blocks = w.findAll('pre')
    expect(blocks[0].text()).toContain('SYNTHETIC INPUT')
    expect(blocks[0].text()).not.toContain('SYNTHETIC UPSTREAM ERROR')
    expect(blocks[1].text()).toBe('SYNTHETIC UPSTREAM ERROR')
    expect(w.text()).toContain('admin.riskControl.evidence.truncated')
    expect(w.text()).toContain('UID 42')
  })
  it('shows missing history without reconstructing input, and clears content on row changes', async () => {
    api.getEvidence.mockResolvedValue({ ...fixture(), status: 'historical_unavailable', payload: undefined })
    const w = mount(CyberEvidencePanel, { props: { logId: 7 } })
    await w.get('button').trigger('click'); await flushPromises()
    expect(w.text()).toContain('admin.riskControl.evidence.noHistory')
    expect(w.findAll('pre')).toHaveLength(0)
    await w.setProps({ logId: 8 })
    expect(w.text()).not.toContain('UID 42')
  })
  it('deletes retained content and does not expose backend errors', async () => {
    api.getEvidence.mockResolvedValue(fixture()); api.deleteEvidence.mockResolvedValue(undefined)
    const w = mount(CyberEvidencePanel, { props: { logId: 7 } })
    await w.get('button').trigger('click'); await flushPromises()
    await w.findAll('button').at(-1)!.trigger('click'); await flushPromises()
    expect(api.deleteEvidence).toHaveBeenCalledWith(7)
    expect(w.findAll('pre')).toHaveLength(0)
    api.getEvidence.mockRejectedValue(new Error('SENSITIVE ERROR'))
    await w.get('button').trigger('click'); await flushPromises()
    expect(w.text()).not.toContain('SENSITIVE ERROR')
    expect(w.text()).toContain('admin.riskControl.evidence.failed')
  })
  it('configures bounded retention separately and exposes key and failure state', async () => {
    api.getEvidenceConfig.mockResolvedValue({ config: { enabled: true, retention_days: 7 }, runtime: { encryption_ready: false, saved: 1, failed: 2, dropped: 3, queue_depth: 0, queue_capacity: 64 } })
    const w = mount(CyberEvidenceSettings)
    await w.get('button').trigger('click'); await flushPromises()
    expect(w.text()).toContain('admin.riskControl.evidence.keyMissing')
    await w.get('input[type="number"]').setValue(31)
    expect(w.findAll('button').at(-1)!.attributes('disabled')).toBeDefined()
    await w.get('input[type="number"]').setValue(3)
    api.updateEvidenceConfig.mockResolvedValue({ enabled: true, retention_days: 3 })
    await w.findAll('button').at(-1)!.trigger('click'); await flushPromises()
    expect(api.updateEvidenceConfig).toHaveBeenCalledWith({ enabled: true, retention_days: 3 })
  })
})

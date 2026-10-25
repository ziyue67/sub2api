import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import QualityOwnershipReview from '../QualityOwnershipReview.vue'
import { previewQualityOwnership, reconcileQualityOwnership, type QualityOwnershipPreview } from '@/api/admin/accountQuality'
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('@/api/admin/accountQuality', () => ({ previewQualityOwnership: vi.fn(), reconcileQualityOwnership: vi.fn() }))
const preview = (): QualityOwnershipPreview => ({ plan_id: 7, account_id: 42, rule_version: 'v1', account_version: 'v2', fence: 'a'.repeat(64), action: 'isolate_models', models: ['astra'], group_ids: [21], relinquishable_count: 1, ownership_retained: true, restrictions_changed: false,
  entries: [{ kind: 'model', model: 'astra', status: 'managed', expected_present: true, current_present: true, expected_owner: 'this_rule', current_owner: 'this_rule', expected_digest: 'a', current_digest: 'a', changed_fields: [], can_relinquish: false },
    { kind: 'model', model: 'sol', status: 'missing', expected_present: true, current_present: false, expected_owner: 'this_rule', current_owner: 'absent', expected_digest: 'a', current_digest: 'b', changed_fields: ['reason'], can_relinquish: true }] })
beforeEach(() => { vi.clearAllMocks(); vi.mocked(previewQualityOwnership).mockResolvedValue(preview()); vi.mocked(reconcileQualityOwnership).mockResolvedValue({ ...preview(), fence: '' }) })
describe('quality ownership explicit preview and confirmation', () => {
  it('shows conflicting draft scope without saving or changing restrictions', async () => {
    const wrapper = mount(QualityOwnershipReview, { props: { planId: 7, draftAction: 'isolate_models', draftModels: ['sol'], draftGroupIds: [21] } }); await flushPromises()
    expect(wrapper.find('[data-testid="quality-scope-conflict"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('qualityOps.ownershipNotUnlock'); expect(reconcileQualityOwnership).not.toHaveBeenCalled()
    expect(wrapper.get('[data-testid="quality-ownership-reconcile"]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-testid="quality-ownership-confirm"]').setValue(true)
    await wrapper.get('[data-testid="quality-ownership-reconcile"]').trigger('click'); await flushPromises()
    expect(reconcileQualityOwnership).toHaveBeenCalledTimes(1); expect(reconcileQualityOwnership).toHaveBeenCalledWith(7, 'a'.repeat(64)); expect(previewQualityOwnership).toHaveBeenCalledTimes(2)
    expect(wrapper.emitted('reconciled')).toHaveLength(1); expect(wrapper.text()).toContain('qualityOps.ownershipReconciled')
    wrapper.unmount()
  })
  it('requires a new preview after a fenced conflict and does not retry', async () => {
    vi.mocked(reconcileQualityOwnership).mockRejectedValueOnce({ response: { status: 409 } })
    const wrapper = mount(QualityOwnershipReview, { props: { planId: 7 } }); await flushPromises()
    await wrapper.get('[data-testid="quality-ownership-confirm"]').setValue(true)
    await wrapper.get('[data-testid="quality-ownership-reconcile"]').trigger('click'); await flushPromises()
    expect(wrapper.text()).toContain('qualityOps.ownershipPreviewChanged'); expect(wrapper.find('[data-testid="quality-ownership-reconcile"]').exists()).toBe(false)
    expect(reconcileQualityOwnership).toHaveBeenCalledTimes(1); expect(wrapper.emitted('reconciled')).toBeUndefined()
    wrapper.unmount()
  })
  it('keeps same-rule changed entries blocked and never offers automatic takeover', async () => {
    const p = preview(); p.relinquishable_count = 0; p.entries = [{ ...p.entries[0], status: 'changed_same_owner' }]
    vi.mocked(previewQualityOwnership).mockResolvedValue(p)
    const wrapper = mount(QualityOwnershipReview, { props: { planId: 7 } }); await flushPromises()
    expect(wrapper.text()).toContain('qualityOps.ownershipStates.changed_same_owner'); expect(wrapper.find('[data-testid="quality-ownership-confirm"]').exists()).toBe(false)
    expect(reconcileQualityOwnership).not.toHaveBeenCalled(); wrapper.unmount()
  })
})

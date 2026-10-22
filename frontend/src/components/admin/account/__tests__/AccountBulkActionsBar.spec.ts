import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'

import AccountBulkActionsBar from '../AccountBulkActionsBar.vue'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string) => key
  })
}))

describe('AccountBulkActionsBar', () => {
  it('only edits the checked selection while OAuth rows are selected', async () => {
    const wrapper = mount(AccountBulkActionsBar, {
      props: { selectedIds: [7, 11], totalResults: 105, selectingAll: false, allResultsSelected: false }
    })
    expect(wrapper.findAll('button').some(button => button.text() === 'admin.accounts.bulkActions.editFiltered')).toBe(false)
    const edit = wrapper.findAll('button').find(button => button.text() === 'admin.accounts.bulkActions.edit')
    await edit!.trigger('click')
    expect(wrapper.emitted('edit-selected')).toHaveLength(1)
    expect(wrapper.emitted('edit-filtered')).toBeUndefined()

    await wrapper.setProps({ selectedIds: [] })
    const filteredEdit = wrapper.findAll('button').find(button => button.text() === 'admin.accounts.bulkActions.editFiltered')
    expect(filteredEdit).toBeDefined()
    await filteredEdit!.trigger('click')
    expect(wrapper.emitted('edit-filtered')).toHaveLength(1)
  })

  it('only offers 2FA export with permission and a selection', async () => {
    const wrapper = mount(AccountBulkActionsBar, {
      props: { selectedIds: [1], totalResults: 45, selectingAll: false, allResultsSelected: false }
    })
    const exportButton = () => wrapper.findAll('button').find(item => item.text() === 'admin.accounts.bulkExport2fa.title')
    expect(exportButton()).toBeUndefined()
    await wrapper.setProps({ canExport2fa: true })
    await exportButton()!.trigger('click')
    expect(wrapper.emitted('export-2fa')).toHaveLength(1)
    await wrapper.setProps({ selectedIds: [] })
    expect(exportButton()).toBeUndefined()
  })

  it('allows selecting all results before any row is selected', async () => {
    const wrapper = mount(AccountBulkActionsBar, {
      props: {
        selectedIds: [],
        totalResults: 45,
        selectingAll: false,
        allResultsSelected: false
      }
    })

    const button = wrapper.findAll('button').find(item =>
      item.text().includes('admin.accounts.bulkActions.selectAllResults')
    )

    expect(button).toBeDefined()
    await button!.trigger('click')
    expect(wrapper.emitted('select-all-results')).toHaveLength(1)
  })

  it('preserves the upstream billing probe action from v0.1.166', async () => {
    const wrapper = mount(AccountBulkActionsBar, {
      props: {
        selectedIds: [1],
        totalResults: 45,
        selectingAll: false,
        allResultsSelected: false
      }
    })

    const button = wrapper.findAll('button').find(item =>
      item.text().includes('admin.accounts.bulkActions.probeUpstreamBilling')
    )

    expect(button).toBeDefined()
    await button!.trigger('click')
    expect(wrapper.emitted('probe-upstream-billing')).toHaveLength(1)
  })

  it('keeps batch model testing visible for selected accounts', async () => {
    const wrapper = mount(AccountBulkActionsBar, {
      props: {
        selectedIds: [1, 2],
        totalResults: 45,
        selectingAll: false,
        allResultsSelected: false
      }
    })

    const button = wrapper.get('[data-testid="batch-account-test"]')
    expect(button.text()).toContain('admin.accounts.bulkActions.batchTestModels')
    await button.trigger('click')
    expect(wrapper.emitted('test-connection')).toHaveLength(1)
  })
})

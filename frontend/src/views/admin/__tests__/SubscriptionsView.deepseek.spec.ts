import { describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

const { listSubscriptions, getAllGroups } = vi.hoisted(() => ({
  listSubscriptions: vi.fn().mockResolvedValue({
    items: [],
    total: 0,
    page: 1,
    page_size: 20,
    pages: 0
  }),
  getAllGroups: vi.fn().mockResolvedValue([])
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    subscriptions: {
      list: listSubscriptions
    },
    groups: {
      getAll: getAllGroups
    },
    usage: {
      searchUsers: vi.fn()
    }
  }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: vi.fn(),
    showSuccess: vi.fn()
  })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key
    })
  }
})

import SubscriptionsView from '../SubscriptionsView.vue'

describe('admin SubscriptionsView DeepSeek filter', () => {
  it('offers DeepSeek in the platform filter', async () => {
    const wrapper = mount(SubscriptionsView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          TablePageLayout: {
            template: '<div><slot name="filters" /><slot name="table" /><slot name="pagination" /></div>'
          },
          Select: {
            props: ['options', 'placeholder'],
            template: '<div class="select-stub" :data-placeholder="placeholder">{{ options.map(option => option.value).join(",") }}</div>'
          },
          DataTable: true,
          Pagination: true,
          BaseDialog: true,
          ConfirmDialog: true,
          EmptyState: true,
          GroupBadge: true,
          GroupOptionItem: true,
          Icon: true,
          RouterLink: true,
          Teleport: true
        }
      }
    })

    await flushPromises()

    const platformFilter = wrapper.get('[data-placeholder="admin.subscriptions.allPlatforms"]')
    expect(platformFilter.text().split(',')).toContain('deepseek')
    expect(listSubscriptions).toHaveBeenCalledTimes(1)
    expect(getAllGroups).toHaveBeenCalledTimes(1)

    wrapper.unmount()
  })
})

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'

import LeaderboardView from '../LeaderboardView.vue'

const {
  authState,
  featureState,
  getDashboardLeaderboard,
  getDashboardSnapshotV2,
} = vi.hoisted(() => ({
  authState: { isAdmin: false },
  featureState: { visible: true },
  getDashboardLeaderboard: vi.fn(),
  getDashboardSnapshotV2: vi.fn(),
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({
    get isAdmin() {
      return authState.isAdmin
    },
  }),
}))

vi.mock('@/utils/featureFlags', () => ({
  isLeaderboardActualCostVisible: () => featureState.visible,
}))

vi.mock('@/api/usage', () => ({
  usageAPI: {
    getDashboardLeaderboard,
    getDashboardSnapshotV2,
  },
}))

vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string) => key }),
}))

const leaderboardItem = {
  rank: 1,
  user: 'u***@example.com',
  requests: 3,
  total_tokens: 30,
  input_tokens: 10,
  output_tokens: 20,
  cache_tokens: 0,
  image_output_tokens: 0,
  cost: 15,
  actual_cost: 12.5,
  account_cost: 9,
  last_active_at: '09-22 20:00',
  is_me: true,
}

function responseWith(item: Record<string, unknown>) {
  return {
    days: 1,
    label: 'Today',
    timezone: 'Asia/Shanghai',
    start: '2026-09-22T00:00:00+08:00',
    end: '2026-09-22T20:00:00+08:00',
    limit: 20,
    sort_by: 'tokens',
    items: [item],
  }
}

function mountView() {
  return mount(LeaderboardView, {
    global: {
      stubs: {
        AppLayout: { template: '<div><slot /></div>' },
        LoadingSpinner: true,
        Select: true,
      },
    },
  })
}

describe('user leaderboard actual cost visibility', () => {
  let wrapper: VueWrapper | undefined

  beforeEach(() => {
    authState.isAdmin = false
    featureState.visible = true
    getDashboardLeaderboard.mockReset().mockResolvedValue(responseWith(leaderboardItem))
    getDashboardSnapshotV2.mockReset().mockResolvedValue({ models: [], groups: [] })
  })

  afterEach(() => {
    wrapper?.unmount()
    wrapper = undefined
  })

  it('hides the column from ordinary users when the administrator disables it', async () => {
    featureState.visible = false
    wrapper = mountView()
    await flushPromises()

    expect(wrapper.text()).not.toContain('leaderboard.actualCost')
    expect(wrapper.text()).not.toContain('$12.5000')
  })

  it('shows the column to ordinary users when enabled', async () => {
    wrapper = mountView()
    await flushPromises()

    expect(wrapper.text()).toContain('leaderboard.actualCost')
    expect(wrapper.text()).toContain('$12.5000')
  })

  it('keeps the column visible to administrators when the public switch is off', async () => {
    authState.isAdmin = true
    featureState.visible = false
    wrapper = mountView()
    await flushPromises()

    expect(wrapper.text()).toContain('leaderboard.actualCost')
    expect(wrapper.text()).toContain('$12.5000')
  })

  it('does not render a fake zero when the backend omits the protected field', async () => {
    const { actual_cost: _actualCost, ...itemWithoutActualCost } = leaderboardItem
    getDashboardLeaderboard.mockResolvedValue(responseWith(itemWithoutActualCost))
    wrapper = mountView()
    await flushPromises()

    expect(wrapper.text()).not.toContain('leaderboard.actualCost')
    expect(wrapper.text()).not.toContain('$0.0000')
  })
})

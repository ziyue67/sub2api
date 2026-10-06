import { describe, expect, it, vi, beforeEach } from 'vitest'
import { defineComponent, h } from 'vue'
import { mount } from '@vue/test-utils'

const isV1 = vi.fn(() => false)
const isV3 = vi.fn(() => false)

vi.mock('@/utils/featureFlags', () => ({
  isChannelMonitorV1Mode: () => isV1(),
  isChannelMonitorV3Mode: () => isV3(),
}))

vi.mock('../ChannelStatusV1View.vue', () => ({
  default: defineComponent({ name: 'ChannelStatusV1View', setup: () => () => h('div', { 'data-testid': 'v1' }) }),
}))
vi.mock('../ChannelStatusV2View.vue', () => ({
  default: defineComponent({ name: 'ChannelStatusV2View', setup: () => () => h('div', { 'data-testid': 'v2' }) }),
}))
vi.mock('../ChannelStatusV3View.vue', () => ({
  default: defineComponent({ name: 'ChannelStatusV3View', setup: () => () => h('div', { 'data-testid': 'v3' }) }),
}))

import ChannelStatusView from '../ChannelStatusView.vue'

const rendered = (wrapper: ReturnType<typeof mount>) =>
  ['v1', 'v2', 'v3'].filter((mode) => wrapper.find(`[data-testid="${mode}"]`).exists())

describe('ChannelStatusView mode switch', () => {
  beforeEach(() => {
    isV1.mockReset()
    isV3.mockReset()
    isV1.mockReturnValue(false)
    isV3.mockReturnValue(false)
  })

  it('renders V2 when not in v1 or v3 mode', () => {
    expect(rendered(mount(ChannelStatusView))).toEqual(['v2'])
  })

  it('renders V1 when in v1 mode', () => {
    isV1.mockReturnValue(true)
    expect(rendered(mount(ChannelStatusView))).toEqual(['v1'])
  })

  it('renders V3 when in v3 mode', () => {
    isV3.mockReturnValue(true)
    expect(rendered(mount(ChannelStatusView))).toEqual(['v3'])
  })
})

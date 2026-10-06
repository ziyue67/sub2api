import { flushPromises, mount, RouterLinkStub } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import PelicanShowcaseApiDialog from '../PelicanShowcaseApiDialog.vue'

const { copyToClipboard } = vi.hoisted(() => ({ copyToClipboard: vi.fn() }))
vi.mock('@/composables/useClipboard', () => ({ useClipboard: () => ({ copyToClipboard }) }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

const mountDialog = (props: { show?: boolean; enabled?: boolean; itemId?: number } = {}) => mount(PelicanShowcaseApiDialog, {
  props: { show: true, enabled: true, ...props },
  global: {
    stubs: {
      Icon: true,
      RouterLink: RouterLinkStub,
      BaseDialog: {
        props: ['show'], emits: ['close'],
        template: '<div v-if="show"><slot /><slot name="footer" /></div>',
      },
    },
  },
})

beforeEach(() => copyToClipboard.mockReset().mockResolvedValue(true))

describe('PelicanShowcaseApiDialog', () => {
  it('uses the panel origin and provides commands without collecting or loading a real API Key', async () => {
    const wrapper = mountDialog({ itemId: 42 })
    const manifestUrl = `${window.location.origin}/api/v1/public/pelican-showcase`
    expect(wrapper.get('[data-testid="showcase-api-manifest-url"]').text()).toBe(`GET ${manifestUrl}`)
    expect(wrapper.get('[data-testid="showcase-api-item-url"]').text()).toBe(`GET ${manifestUrl}/items/42`)
    expect(wrapper.find('input').exists()).toBe(false)
    expect(wrapper.text()).toContain('pelicanShowcase.api.readOnly')
    expect(wrapper.text()).toContain('pelicanShowcase.api.polling')
    expect(wrapper.findComponent(RouterLinkStub).props('to')).toBe('/keys')

    await wrapper.get('[data-testid="showcase-api-copy-manifest-url"]').trigger('click')
    expect(copyToClipboard).toHaveBeenLastCalledWith(manifestUrl)
    await wrapper.get('[data-testid="showcase-api-copy-example"]').trigger('click')
    const manifestCommand = copyToClipboard.mock.lastCall![0] as string
    expect(manifestCommand.split('\n')).toEqual([
      'curl --compressed -i \\',
      '  -H "Authorization: Bearer YOUR_API_KEY" \\',
      `  "${manifestUrl}"`,
    ])

    await wrapper.get('[data-testid="showcase-api-example-item"]').trigger('click')
    expect(wrapper.get('[data-testid="showcase-api-command"]').text()).toContain(`${manifestUrl}/items/42`)
    expect(wrapper.get('[data-testid="showcase-api-example-item"]').attributes('aria-selected')).toBe('true')
    expect(wrapper.text()).toContain('pelicanShowcase.api.itemHint')

    await wrapper.get('[data-testid="showcase-api-example-cache"]').trigger('click')
    await wrapper.get('[data-testid="showcase-api-copy-example"]').trigger('click')
    const cachedCommand = copyToClipboard.mock.lastCall![0] as string
    expect(cachedCommand).toContain('Authorization: Bearer YOUR_API_KEY')
    expect(cachedCommand).toContain("-H 'If-None-Match: YOUR_ETAG'")
    expect(cachedCommand).toContain(manifestUrl)
    expect(wrapper.text()).toContain('pelicanShowcase.api.cacheHint')
    wrapper.unmount()
  })

  it('shows unavailable access with a placeholder result ID and keeps the examples readable', async () => {
    const wrapper = mountDialog({ enabled: false })
    expect(wrapper.get('[data-testid="showcase-api-status"]').text()).toBe('pelicanShowcase.api.unavailable')
    expect(wrapper.get('[role="status"]').text()).toBe('pelicanShowcase.api.unavailableHint')
    expect(wrapper.get('[data-testid="showcase-api-item-url"]').text()).toContain('/items/RESULT_ID')
    await wrapper.get('[data-testid="showcase-api-example-item"]').trigger('click')
    expect(wrapper.get('[data-testid="showcase-api-command"]').text()).toContain('/items/RESULT_ID')
    await wrapper.get('[data-testid="showcase-api-copy-item-url"]').trigger('click')
    expect(copyToClipboard).toHaveBeenLastCalledWith(`${window.location.origin}/api/v1/public/pelican-showcase/items/RESULT_ID`)
    wrapper.unmount()
  })

  it('resets the request example when reopened and emits close when managing Keys', async () => {
    const wrapper = mountDialog()
    await wrapper.get('[data-testid="showcase-api-example-cache"]').trigger('click')
    await wrapper.setProps({ show: false })
    expect(wrapper.find('[data-testid="showcase-api-dialog"]').exists()).toBe(false)
    await wrapper.setProps({ show: true })
    await flushPromises()
    expect(wrapper.get('[data-testid="showcase-api-example-manifest"]').attributes('aria-selected')).toBe('true')
    await wrapper.get('[data-testid="showcase-api-keys"]').trigger('click')
    expect(wrapper.emitted('close')).toHaveLength(1)
    wrapper.unmount()
  })
})

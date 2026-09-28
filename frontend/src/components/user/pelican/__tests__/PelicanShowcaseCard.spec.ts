import { mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import PelicanShowcaseCard from '../PelicanShowcaseCard.vue'
import type { PelicanShowcaseItem } from '@/api/pelicanShowcase'

vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string) => key }),
}))

// Lets a test move the card in and out of view.
let report: (visible: boolean) => void = () => {}
let observerOptions: IntersectionObserverInit | undefined
class ManualObserver {
  constructor(callback: IntersectionObserverCallback, options?: IntersectionObserverInit) {
    observerOptions = options
    report = (visible) => callback([{ isIntersecting: visible } as IntersectionObserverEntry], this as unknown as IntersectionObserver)
  }
  observe() {}
  disconnect() {}
  unobserve() {}
}

const item: PelicanShowcaseItem = {
  id: 1, group_id: 1, model_id: 'gpt-6-astra', reasoning_effort: 'medium', latency_ms: 1000, generated_at: '2026-09-28T04:00:00Z',
}

beforeEach(() => {
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
  vi.stubGlobal('IntersectionObserver', ManualObserver)
})
afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

describe('PelicanShowcaseCard', () => {
  it('asks for its HTML only once it stays in view, so cards a drag sweeps past load nothing', () => {
    const wrapper = mount(PelicanShowcaseCard, { props: { item, groupName: 'GPT Plus' } })
    // The same lookahead covers cards below the fold and cards still hidden in their row.
    expect(observerOptions).toEqual({ rootMargin: '200px', scrollMargin: '200px' })

    report(true)
    vi.advanceTimersByTime(100)
    report(false)
    vi.advanceTimersByTime(1000)
    expect(wrapper.emitted('visible')).toBeUndefined()

    report(true)
    vi.advanceTimersByTime(150)
    expect(wrapper.emitted('visible')).toHaveLength(1)
    wrapper.unmount()
  })

  it('drops a pending report when it goes away', () => {
    const wrapper = mount(PelicanShowcaseCard, { props: { item, groupName: 'GPT Plus' } })
    report(true)
    expect(vi.getTimerCount()).toBe(1)
    wrapper.unmount()
    expect(vi.getTimerCount()).toBe(0)
  })
})

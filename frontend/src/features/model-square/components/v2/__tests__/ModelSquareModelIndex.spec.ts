import { mount } from '@vue/test-utils'
import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { nextTick } from 'vue'
import ModelSquareModelIndex from '../ModelSquareModelIndex.vue'
import type { ModelSquareModel } from '../../../types'

const models: ModelSquareModel[] = [
  { key: 'openai:gpt-4o', name: 'gpt-4o', platform: 'openai', entries: [], channels: [] },
  { key: 'openai:gpt-4o-mini', name: 'gpt-4o-mini', platform: 'openai', entries: [], channels: [] },
  { key: 'anthropic:claude-3-5-sonnet', name: 'claude-3-5-sonnet', platform: 'anthropic', entries: [], channels: [] },
  { key: 'anthropic:claude-3-opus', name: 'claude-3-opus', platform: 'anthropic', entries: [], channels: [] },
  { key: 'google:gemini-1.5-pro', name: 'gemini-1.5-pro', platform: 'google', entries: [], channels: [] },
  { key: 'google:gemini-1.5-flash', name: 'gemini-1.5-flash', platform: 'google', entries: [], channels: [] },
  { key: 'meta:llama-3-70b', name: 'llama-3-70b', platform: 'meta', entries: [], channels: [] },
  { key: 'mistral:mistral-large', name: 'mistral-large', platform: 'mistral', entries: [], channels: [] },
  { key: 'cohere:command-r-plus', name: 'command-r-plus', platform: 'cohere', entries: [], channels: [] },
  { key: 'alibaba:qwen-2-72b', name: 'qwen-2-72b', platform: 'alibaba', entries: [], channels: [] },
]

function modelArticles() {
  return models.map((m) => `<article id='model-${m.key}' data-model-key='${m.key}'></article>`).join('')
}

describe('ModelSquareModelIndex', () => {
  beforeEach(() => {
    document.body.innerHTML = modelArticles()
    Element.prototype.scrollIntoView = vi.fn()
    globalThis.IntersectionObserver = vi.fn().mockImplementation(() => ({
      observe: vi.fn(),
      disconnect: vi.fn(),
      unobserve: vi.fn(),
    }))
  })

  afterEach(() => {
    document.body.innerHTML = ''
    vi.restoreAllMocks()
  })

  it('renders one button per model', async () => {
    const wrapper = mount(ModelSquareModelIndex, {
      props: { models, modelValue: null },
      attachTo: document.body,
    })
    await nextTick()
    expect(wrapper.findAll('button').length).toBe(models.length)
  })

  it('emits update:modelValue and scrolls to the selected model on click', async () => {
    const wrapper = mount(ModelSquareModelIndex, {
      props: { models, modelValue: null },
      attachTo: document.body,
    })
    await nextTick()

    const target = document.getElementById('model-openai:gpt-4o')!
    const scrollSpy = vi.spyOn(target, 'scrollIntoView').mockImplementation(() => {})

    const buttons = wrapper.findAll('button')
    expect(buttons.length).toBe(models.length)

    await buttons[0].trigger('click')

    expect(wrapper.emitted('update:modelValue')).toBeTruthy()
    expect(wrapper.emitted('update:modelValue')![0]).toEqual(['openai:gpt-4o'])
    expect(scrollSpy).toHaveBeenCalledWith({ behavior: 'smooth', block: 'start' })
  })

  it('scrolls to other models as well', async () => {
    const wrapper = mount(ModelSquareModelIndex, {
      props: { models, modelValue: null },
      attachTo: document.body,
    })
    await nextTick()

    const buttons = wrapper.findAll('button')
    const claudeTarget = document.getElementById('model-anthropic:claude-3-5-sonnet')!
    const geminiTarget = document.getElementById('model-google:gemini-1.5-pro')!
    const claudeSpy = vi.spyOn(claudeTarget, 'scrollIntoView').mockImplementation(() => {})
    const geminiSpy = vi.spyOn(geminiTarget, 'scrollIntoView').mockImplementation(() => {})

    await buttons[2].trigger('click')
    expect(claudeSpy).toHaveBeenCalledWith({ behavior: 'smooth', block: 'start' })

    await buttons[4].trigger('click')
    expect(geminiSpy).toHaveBeenCalledWith({ behavior: 'smooth', block: 'start' })
  })

  it('highlights matching text when search prop is provided', async () => {
    const wrapper = mount(ModelSquareModelIndex, {
      props: { models, modelValue: null, search: 'claude' },
      attachTo: document.body,
    })
    await nextTick()

    const claudeButton = wrapper.findAll('button')[2]
    expect(claudeButton.html()).toContain('<mark')
    expect(claudeButton.html()).toContain('claude')
  })

  it('navigates with keyboard arrows and selects with Enter', async () => {
    const wrapper = mount(ModelSquareModelIndex, {
      props: { models, modelValue: null },
      attachTo: document.body,
    })
    await nextTick()

    const aside = wrapper.find('aside')
    await aside.trigger('keydown', { key: 'ArrowDown' })
    await aside.trigger('keydown', { key: 'ArrowDown' })
    await aside.trigger('keydown', { key: 'Enter' })

    expect(wrapper.emitted('update:modelValue')).toBeTruthy()
    expect(wrapper.emitted('update:modelValue')!.slice(-1)[0]).toEqual(['anthropic:claude-3-5-sonnet'])
  })

  it('wraps around with arrow keys', async () => {
    const wrapper = mount(ModelSquareModelIndex, {
      props: { models, modelValue: null },
      attachTo: document.body,
    })
    await nextTick()

    const aside = wrapper.find('aside')
    await aside.trigger('keydown', { key: 'ArrowUp' })
    await aside.trigger('keydown', { key: 'Enter' })

    expect(wrapper.emitted('update:modelValue')).toBeTruthy()
    expect(wrapper.emitted('update:modelValue')!.slice(-1)[0]).toEqual([models[models.length - 1].key])
  })
})

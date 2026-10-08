import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import { createI18n } from 'vue-i18n'
import OAuthAuthorizationFlow from '../OAuthAuthorizationFlow.vue'

vi.mock('@/composables/useClipboard', () => ({ useClipboard: () => ({ copyToClipboard: vi.fn(), copied: false }) }))

const make = (platform: 'openai' | 'anthropic' = 'openai') => mount(OAuthAuthorizationFlow, {
  props: { addMethod: 'oauth', platform, excelOauth: platform === 'openai', showCookieOption: false },
  global: { plugins: [createI18n({ legacy: false, locale: 'en', missingWarn: false, fallbackWarn: false, messages: { en: {} } })] }
})

describe('OpenAI Excel OAuth', () => {
  it('uses Excel instructions without a legacy login selector and accepts the official callback', async () => {
    const wrapper = make()
    expect(wrapper.text()).toContain('excelLoginHint')
    expect(wrapper.find('select').exists()).toBe(false)
    const generate = wrapper.findAll('button').find(b => b.text().includes('generateAuthUrl'))!
    await generate.trigger('click')
    expect(wrapper.emitted('generate-url')?.at(-1)).toEqual([])
    await wrapper.findAll('textarea').at(-1)!.setValue('https://bps.openai.com/auth/callback?code=test-code&state=bps.test.PC')
    expect((wrapper.vm as unknown as { oauthState: string }).oauthState).toBe('bps.test.PC')
    expect((wrapper.vm as unknown as { authCode: string }).authCode).toBe('test-code')
  })
  it('does not change other providers', () => {
    expect(make('anthropic').text()).not.toContain('excelLoginHint')
  })
})

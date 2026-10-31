import { describe, expect, it } from 'vitest'
import { createI18n } from 'vue-i18n'

describe('translation HTML sanitization', () => {
  it('neutralizes iframe srcdoc instead of only escaping its value', () => {
    const i18n = createI18n({
      legacy: false,
      locale: 'en',
      escapeParameter: true,
      warnHtmlMessage: false,
      messages: { en: { fixture: '<iframe srcdoc="{content}"></iframe>' } },
    })
    const container = document.createElement('div')
    container.innerHTML = i18n.global.t('fixture', { content: '<b>synthetic fixture</b>' })
    expect(container.querySelector('iframe')).not.toBeNull()
    expect(container.querySelector('iframe')?.hasAttribute('srcdoc')).toBe(false)
    i18n.dispose()
  })
})

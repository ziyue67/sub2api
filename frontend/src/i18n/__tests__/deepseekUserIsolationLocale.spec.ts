import { describe, expect, it } from 'vitest'

import en from '../locales/en/admin/accounts'
import zh from '../locales/zh/admin/accounts'

describe('DeepSeek user isolation locale descriptions', () => {
  it('documents the two supported modes without exposing an editable identity', () => {
    expect(en.accounts.deepseek.userIsolationAuthenticatedUser).toContain('authenticated_user')
    expect(en.accounts.deepseek.userIsolationOff).toContain('off')
    expect(en.accounts.deepseek.userIsolationModeDesc).toContain('cannot be entered or viewed')

    expect(zh.accounts.deepseek.userIsolationAuthenticatedUser).toContain('authenticated_user')
    expect(zh.accounts.deepseek.userIsolationOff).toContain('off')
    expect(zh.accounts.deepseek.userIsolationModeDesc).toContain('不能输入或查看')
  })
})

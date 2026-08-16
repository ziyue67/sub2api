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

  it('documents only the client WS-to-HTTP bridge modes', () => {
    expect(en.accounts.deepseek.wsModeOff).toContain('off')
    expect(en.accounts.deepseek.wsModeHttpBridge).toContain('http_bridge')
    expect(en.accounts.deepseek.wsModeDesc).toContain('HTTP /responses')
    expect(en.accounts.deepseek.wsModeDesc).toContain('does not use native WebSocket')

    expect(zh.accounts.deepseek.wsModeOff).toContain('off')
    expect(zh.accounts.deepseek.wsModeHttpBridge).toContain('http_bridge')
    expect(zh.accounts.deepseek.wsModeDesc).toContain('HTTP /responses')
    expect(zh.accounts.deepseek.wsModeDesc).toContain('不使用原生 WebSocket')
  })
})

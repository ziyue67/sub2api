import { describe, expect, it } from 'vitest'
import {
  formatTokenGuardReloginText,
  parseTokenGuardReloginText
} from '../accountTokenGuard'

describe('token guard re-login credential format', () => {
  it('parses the account----password----2FA format', () => {
    expect(parseTokenGuardReloginText(
      'user@example.com----p,a,s,s----JBSWY3DPEHPK3PXP\nsecond@example.com----secret----'
    )).toEqual([
      { email: 'user@example.com', password: 'p,a,s,s', mfa_secret: 'JBSWY3DPEHPK3PXP' },
      { email: 'second@example.com', password: 'secret', mfa_secret: '' }
    ])
  })

  it('keeps reading legacy comma-separated entries and formats them with the new separator', () => {
    const accounts = parseTokenGuardReloginText('user@example.com,password,JBSWY3DPEHPK3PXP')
    expect(formatTokenGuardReloginText(accounts)).toBe('user@example.com----password----JBSWY3DPEHPK3PXP')
  })
})

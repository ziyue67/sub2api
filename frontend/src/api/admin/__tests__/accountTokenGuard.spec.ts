import { describe, expect, it } from 'vitest'
import {
  formatTokenGuardReloginText,
  parseTwoFALoginText,
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


describe('initial 2FA login input', () => {
  it('supports both separators and passwords containing commas', () => {
    expect(parseTwoFALoginText('USER@example.com----p,a!ss----SECRET\nsecond@example.com,password,SECRET')).toEqual([
      { email: 'user@example.com', password: 'p,a!ss', mfa_secret: 'SECRET' },
      { email: 'second@example.com', password: 'password', mfa_secret: 'SECRET' }
    ])
  })
  it.each(['', 'bad', 'u@example.com----p----', 'u@example.com----p----s----extra', 'u@example.com----p----s\nU@example.com----p----s'])('rejects invalid or duplicate entries', input => {
    expect(() => parseTwoFALoginText(input)).toThrow('invalid_batch')
  })
})

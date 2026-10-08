import { describe, expect, it } from 'vitest'
import { splitSupportTicketText, supportTicketErrorMessage } from '../supportTickets'
import { supportTicketLength } from '@/api/supportTickets'

const t = (key: string, params?: Record<string, unknown>) => (params ? `${key}:${JSON.stringify(params)}` : key)

describe('support ticket text', () => {
  it('turns http(s) addresses into links and keeps the rest as text', () => {
    expect(splitSupportTicketText('截图在 https://img.example.com/a.png，谢谢')).toEqual([
      { kind: 'text', text: '截图在 ' },
      { kind: 'link', text: 'https://img.example.com/a.png', href: 'https://img.example.com/a.png' },
      { kind: 'text', text: '，谢谢' },
    ])
    expect(splitSupportTicketText('see http://x.test/path?q=1.')).toEqual([
      { kind: 'text', text: 'see ' },
      { kind: 'link', text: 'http://x.test/path?q=1', href: 'http://x.test/path?q=1' },
      { kind: 'text', text: '.' },
    ])
    expect(splitSupportTicketText('(https://a.test) and https://b.test!').filter((part) => part.kind === 'link').map((part) => part.text))
      .toEqual(['https://a.test', 'https://b.test'])
  })

  it('never links other schemes', () => {
    for (const text of ['javascript:alert(1)', 'data:text/html,<b>x</b>', 'ftp://files.test/a', 'mailto:a@b.test']) {
      expect(splitSupportTicketText(text)).toEqual([{ kind: 'text', text }])
    }
    expect(splitSupportTicketText('')).toEqual([])
  })

  it('counts characters the way the server does', () => {
    expect(supportTicketLength('报错')).toBe(2)
    expect(supportTicketLength('ok 👍')).toBe(4)
  })

  it('explains failures by reason code with the limit filled in', () => {
    expect(supportTicketErrorMessage({ reason: 'SUPPORT_TICKET_TOO_MANY_OPEN', metadata: { max: '5' }, message: 'too many unclosed tickets' }, t))
      .toBe('supportTickets.errors.SUPPORT_TICKET_TOO_MANY_OPEN:{"max":"5"}')
    expect(supportTicketErrorMessage({ message: 'boom' }, t)).toBe('boom')
    expect(supportTicketErrorMessage(null, t, 'supportTickets.loadFailed')).toBe('supportTickets.loadFailed')
  })
})

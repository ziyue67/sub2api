import type { SupportTicketStatus } from '@/api/supportTickets'
import { extractI18nErrorMessage } from '@/utils/apiError'

export const SUPPORT_TICKET_STATUS_CLASSES: Record<SupportTicketStatus, string> = {
  pending: 'bg-amber-50 text-amber-700 ring-amber-600/20 dark:bg-amber-900/30 dark:text-amber-300 dark:ring-amber-400/20',
  processing: 'bg-blue-50 text-blue-700 ring-blue-600/20 dark:bg-blue-900/30 dark:text-blue-300 dark:ring-blue-400/20',
  replied: 'bg-emerald-50 text-emerald-700 ring-emerald-600/20 dark:bg-emerald-900/30 dark:text-emerald-300 dark:ring-emerald-400/20',
  closed: 'bg-gray-100 text-gray-600 ring-gray-500/20 dark:bg-dark-700 dark:text-gray-300 dark:ring-white/10',
}

export type SupportTicketTextPart = { kind: 'text'; text: string } | { kind: 'link'; text: string; href: string }

// Stops at whitespace, quotes, angle brackets and full-width punctuation so a
// link followed by Chinese text does not swallow it.
const URL_PATTERN = /https?:\/\/[^\s<>"'（）【】「」『』《》，。；：！？、]+/gi
const TRAILING_PUNCTUATION = /[.,;:!?)\]}'"]+$/

function isHttpUrl(value: string): boolean {
  try {
    const url = new URL(value)
    return url.protocol === 'http:' || url.protocol === 'https:'
  } catch {
    return false
  }
}

/**
 * Splits plain ticket text into text and http(s) link parts. Messages are never
 * rendered as HTML; links become <a> nodes built by the template.
 */
export function splitSupportTicketText(text: string): SupportTicketTextPart[] {
  const parts: SupportTicketTextPart[] = []
  let last = 0
  for (const match of text.matchAll(URL_PATTERN)) {
    const start = match.index ?? 0
    const trailing = match[0].match(TRAILING_PUNCTUATION)?.[0] ?? ''
    const url = match[0].slice(0, match[0].length - trailing.length)
    if (start > last) parts.push({ kind: 'text', text: text.slice(last, start) })
    parts.push(isHttpUrl(url) ? { kind: 'link', text: url, href: url } : { kind: 'text', text: url })
    last = start + url.length
  }
  if (last < text.length) parts.push({ kind: 'text', text: text.slice(last) })
  return parts
}

type Translate = (key: string, params?: Record<string, unknown>) => string

/** Localized message for a failed ticket request, using the reason code when known. */
export function supportTicketErrorMessage(err: unknown, t: Translate, fallbackKey = 'supportTickets.actionFailed'): string {
  return extractI18nErrorMessage(err, t, 'supportTickets.errors', t(fallbackKey))
}

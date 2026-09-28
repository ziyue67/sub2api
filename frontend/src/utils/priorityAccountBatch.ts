import type { AccountListItem } from '@/types'

export interface PriorityAccountRow {
  key: string
  kind: 'teams' | 'pro' | 'plus' | 'api' | 'oauth'
  rate?: number
  priority: number
  selected: boolean
  accounts: Pick<AccountListItem, 'id' | 'name' | 'priority' | 'concurrency' | 'load_factor'>[]
}
export function groupPriorityAccounts(accounts: AccountListItem[]): PriorityAccountRow[] {
  const groups = new Map<string, PriorityAccountRow>(), seen = new Set<number>()
  for (const account of accounts) {
    if (account.parent_account_id || seen.has(account.id) || account.platform !== 'openai' || !['apikey', 'oauth'].includes(account.type)) continue
    seen.add(account.id)
    let kind: PriorityAccountRow['kind'] = 'oauth', rate: number | undefined
    if (account.type === 'apikey') {
      kind = 'api'
      rate = typeof account.rate_multiplier === 'number' && Number.isFinite(account.rate_multiplier) && account.rate_multiplier >= 0 ? account.rate_multiplier : 1
    } else {
      const plan = String(account.credentials?.plan_type || account.parent_plan_type || '').toLowerCase().replace(/[_\s-]/g, '')
      if (['team', 'teams', 'business', 'chatgptteam', 'chatgptbusiness'].includes(plan)) kind = 'teams'
      else if (['pro', 'chatgptpro'].includes(plan)) kind = 'pro'
      else if (['plus', 'chatgptplus'].includes(plan)) kind = 'plus'
    }
    const key = kind === 'api' ? `api:${rate}` : kind
    if (!groups.has(key)) groups.set(key, { key, kind, rate, priority: 0, selected: true, accounts: [] })
    groups.get(key)!.accounts.push({ id: account.id, name: account.name, priority: account.priority, concurrency: account.concurrency, load_factor: account.load_factor })
  }
  const order = { teams: 0, pro: 1, plus: 2, api: 3, oauth: 4 }
  return [...groups.values()].sort((a, b) => order[a.kind] - order[b.kind] || (a.rate ?? 0) - (b.rate ?? 0)).map((row, index) => ({ ...row, priority: index + 1 }))
}
export function priorityBatchPayloads(rows: PriorityAccountRow[], concurrency?: number, loadFactor?: number, teamsWindowStart?: string) {
  if (concurrency !== undefined && (!Number.isInteger(concurrency) || concurrency < 1 || concurrency > 10000)) throw new Error('invalid concurrency')
  if (loadFactor !== undefined && (!Number.isInteger(loadFactor) || loadFactor < 1 || loadFactor > 10000)) throw new Error('invalid load factor')
  if (teamsWindowStart !== undefined && !Number.isFinite(Date.parse(teamsWindowStart))) throw new Error('invalid Teams window start')
  const batches: { extra?: { priority_teams_window_start: string }; account_ids: number[]; priority: number; concurrency?: number; load_factor?: number }[] = []
  const seen = new Set<number>()
  for (const row of rows.filter(row => row.selected)) {
    if (!Number.isInteger(row.priority) || row.priority < 0 || row.priority > 1000000) throw new Error('invalid priority')
    const ids = row.accounts.map(a => a.id).filter(id => { if (seen.has(id)) return false; seen.add(id); return true })
    for (let i = 0; i < ids.length; i += 100) batches.push({ account_ids: ids.slice(i, i + 100), priority: row.priority, ...(concurrency === undefined ? {} : { concurrency }), ...(loadFactor === undefined ? {} : { load_factor: loadFactor }), ...(row.kind === 'teams' && teamsWindowStart ? { extra: { priority_teams_window_start: teamsWindowStart } } : {}) })
  }
  return batches
}

export type PriorityAccountKind = 'teams' | 'pro' | 'plus' | 'api'
export const defaultPriorityTypeOrder: PriorityAccountKind[] = ['teams', 'pro', 'plus', 'api']
export function orderPriorityAccountRows(rows: PriorityAccountRow[], order: PriorityAccountKind[]): PriorityAccountRow[] {
  if (order.length !== 4 || new Set(order).size !== 4 || order.some(kind => !defaultPriorityTypeOrder.includes(kind))) throw new Error('invalid account type order')
  const rank = (kind: PriorityAccountRow['kind']) => kind === 'oauth' ? order.length : order.indexOf(kind)
  return [...rows].sort((a, b) => rank(a.kind) - rank(b.kind) || (a.rate ?? 0) - (b.rate ?? 0)).map((row, index) => ({ ...row, priority: index + 1 }))
}
export function inferPriorityTypeOrder(rows: PriorityAccountRow[]): PriorityAccountKind[] {
  const priority = (kind: PriorityAccountKind) => Math.min(...rows.filter(r => r.kind === kind).flatMap(r => r.accounts.map(a => a.priority)))
  return [...defaultPriorityTypeOrder].sort((a, b) => {
    const left = priority(a), right = priority(b)
    return left === right ? 0 : left < right ? -1 : 1
  })
}

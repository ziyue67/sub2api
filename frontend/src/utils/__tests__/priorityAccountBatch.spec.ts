import { describe, expect, it } from 'vitest'
import { groupPriorityAccounts, priorityBatchPayloads, orderPriorityAccountRows, inferPriorityTypeOrder } from '../priorityAccountBatch'
import type { AccountListItem } from '@/types'
const account = (id: number, type: string, plan = '', rate = 1) => ({ id, name: `A${id}`, platform: 'openai', type, credentials: { plan_type: plan }, rate_multiplier: rate, priority: 9, concurrency: 5, load_factor: null }) as AccountListItem

describe('priority account batch', () => {
  it('separates API account rates and OAuth plans without leaking credentials into preview', () => {
    const rows = groupPriorityAccounts([account(1, 'oauth', 'team'), account(2, 'oauth', 'business'), account(3, 'oauth', 'chatgpt_pro'), account(4, 'apikey', '', .5), account(5, 'apikey', '', .1), account(6, 'oauth', 'plus'), account(7, 'oauth', 'free'), account(1, 'oauth', 'team')])
    expect(rows.map(r => r.key)).toEqual(['teams', 'pro', 'plus', 'api:0.1', 'api:0.5', 'oauth'])
    expect(rows[0].accounts.map(a => a.id)).toEqual([1, 2]); expect(rows[0].accounts[0]).not.toHaveProperty('credentials')
  })
  it('writes only selected fields to pinned account IDs, including 100/10000/1', () => {
    const rows = groupPriorityAccounts([account(1, 'oauth', 'team'), account(2, 'apikey')]); rows[1].selected = false
    expect(priorityBatchPayloads(rows)).toEqual([{ account_ids: [1], priority: 1 }])
    expect(priorityBatchPayloads(rows, 100, 10000)).toEqual([{ account_ids: [1], priority: 1, concurrency: 100, load_factor: 10000 }])
  })
  it('bounds batches and validates values before any write', () => {
    const rows = groupPriorityAccounts(Array.from({ length: 205 }, (_, i) => account(i + 1, 'oauth', 'team')))
    expect(priorityBatchPayloads(rows).map(b => b.account_ids.length)).toEqual([100, 100, 5])
    expect(() => priorityBatchPayloads(rows, 0)).toThrow(); expect(() => priorityBatchPayloads(rows, 100, 10001)).toThrow()
    rows[0].priority = 1.5; expect(() => priorityBatchPayloads(rows)).toThrow()
  })
})

it('only writes a paid-window start to selected physical Teams accounts', () => {
  const rows = groupPriorityAccounts([account(1, 'oauth', 'team'), account(2, 'oauth', 'pro'), { ...account(3, 'oauth', 'team'), parent_account_id: 1 }])
  const start = '2026-09-28T10:00:00.000Z'
  const batches = priorityBatchPayloads(rows, 100, 10000, start)
  expect(batches).toHaveLength(2)
  expect(batches[0].extra).toEqual({ priority_teams_window_start: start })
  expect(batches[1]).not.toHaveProperty('extra')
})

it('supports arbitrary type order and reconstructs it from applied priorities', () => {
  const rows = groupPriorityAccounts([account(1, 'oauth', 'team'), account(2, 'oauth', 'pro'), account(3, 'oauth', 'plus'), account(4, 'apikey')])
  const sorted = orderPriorityAccountRows(rows, ['plus', 'api', 'teams', 'pro'])
  expect(sorted.map(r => r.kind)).toEqual(['plus', 'api', 'teams', 'pro'])
  expect(priorityBatchPayloads(sorted).map(p => [p.account_ids[0], p.priority])).toEqual([[3, 1], [4, 2], [1, 3], [2, 4]])
  sorted.forEach(row => row.accounts.forEach(a => { a.priority = row.priority }))
  expect(inferPriorityTypeOrder(sorted)).toEqual(['plus', 'api', 'teams', 'pro'])
})

import { describe, expect, it } from 'vitest'
import { CANDY_HISTORY_LIMIT, candyDisplayState, candyHistorySlots, hasMonitorSamples, monitorCardTimeline, monitorRefreshSeconds } from '../monitorCards'
import type { MonitorCandyHistory, MonitorCoverage, MonitorMatrixRow, MonitorMetric } from '@/api/channelMonitorV2'

describe('monitor card data semantics', () => {
  it.each([0, 1, 60, 99, 100, 120])('keeps 100 history slots for %i records without inventing results', (count) => {
    const results: MonitorCandyHistory['results'] = Array.from({ length: count }, (_, index) => ({ checked_at: new Date(Date.UTC(2026, 8, 29, 0, index)).toISOString(), verdict: 'correct', latency_ms: index }))
    const history: MonitorCandyHistory = { model: 'demo', reasoning_effort: 'medium', interval_minutes: 1, results }
    const slots = candyHistorySlots(history)
    const kept = Math.min(100, count)
    expect(CANDY_HISTORY_LIMIT).toBe(100)
    expect(slots).toHaveLength(100)
    expect(slots.slice(0, 100 - kept)).toEqual(Array(100 - kept).fill(null))
    expect(slots.slice(100 - kept)).toEqual(results.slice(-100))
    if (count) expect(slots[99]).toBe(results.at(-1))
    expect(history.results).toHaveLength(count)
  })
  it('refreshes at the fastest visible probe interval without slowing configured polling or bootstrap', () => {
    const row = (minutes: number) => ({ candy: { interval_minutes: minutes } }) as MonitorMatrixRow
    expect(monitorRefreshSeconds(300, [], false)).toBe(300)
    expect(monitorRefreshSeconds(60, [], false)).toBe(60)
    expect(monitorRefreshSeconds(undefined, [], false)).toBe(300)
    expect(monitorRefreshSeconds(300, [row(1)], false)).toBe(60)
    expect(monitorRefreshSeconds(300, [row(2)], false)).toBe(120)
    expect(monitorRefreshSeconds(300, [row(5), row(10)], false)).toBe(300)
    expect(monitorRefreshSeconds(300, [row(5), row(1)], false)).toBe(60)
    expect(monitorRefreshSeconds(60, [row(2)], false)).toBe(60)
    expect(monitorRefreshSeconds(300, [row(0), row(Number.NaN)], false)).toBe(300)
    expect(monitorRefreshSeconds(30, [row(0.5)], false)).toBe(60)
    expect(monitorRefreshSeconds(300, [row(1)], true)).toBe(10)
  })
  it('does not infer successful traffic from redacted zero counters', () => {
    expect(hasMonitorSamples({ request_count: 0, has_samples: false } as MonitorMetric)).toBe(false)
    expect(hasMonitorSamples({ request_count: 0, has_samples: true } as MonitorMetric)).toBe(true)
    expect(hasMonitorSamples({ request_count: 3 } as MonitorMetric)).toBe(true)
  })
  it('keeps empty time windows unknown and uses the worst observed health in each window', () => {
    const metric = { request_count: 0, has_samples: true } as MonitorMetric
    const row = { buckets: [
      { bucket_start: '2026-09-28T00:06:00Z', metrics: metric, health: { overall: 'healthy' } },
      { bucket_start: '2026-09-28T00:07:00Z', metrics: metric, health: { overall: 'warning' } },
      { bucket_start: '2026-09-28T00:12:00Z', metrics: { ...metric, has_samples: false }, health: { overall: 'healthy' } },
    ] } as MonitorMatrixRow
    const bars = monitorCardTimeline(row, { requested_start: '2026-09-28T00:00:00Z', requested_end: '2026-09-28T01:30:00Z' } as MonitorCoverage)
    expect(bars).toHaveLength(18)
    expect(bars[0]).toMatchObject({ state: 'unknown', observed: false })
    expect(bars[1]).toMatchObject({ state: 'warning', observed: true })
    expect(bars[2]).toMatchObject({ state: 'unknown', observed: false })
    expect(monitorCardTimeline(row, undefined)).toEqual([])
  })
  it('preserves sorted original bucket metrics without averaging redacted counts or medians', () => {
    const metric = { request_count: 0, has_samples: true, error_rate: 0.1, cache_rate: 0.8, ttft: { p50_ms: 8000 } } as MonitorMetric
    const late = { bucket_start: '2026-09-28T00:07:00Z', metrics: metric, health: { overall: 'warning' } }
    const early = { bucket_start: '2026-09-28T00:06:00Z', metrics: { ...metric, error_rate: 0, ttft: { p50_ms: null } }, health: { overall: 'healthy' } }
    const row = { buckets: [late, early, { ...late, bucket_start: 'invalid' }, { ...late, bucket_start: '2026-09-28T01:30:00Z' }] } as MonitorMatrixRow
    const bars = monitorCardTimeline(row, { requested_start: '2026-09-28T00:00:00Z', requested_end: '2026-09-28T01:30:00Z' } as MonitorCoverage)
    expect(bars[0].buckets).toEqual([])
    expect(bars[1].buckets).toEqual([early, late])
    expect(bars[1].buckets[1].metrics).toBe(metric)
    expect(bars[1].state).toBe('warning')
    expect(row.buckets[0]).toBe(late)
    expect(bars.flatMap(bar => bar.buckets)).toHaveLength(2)
  })
  it('distinguishes a wrong answer, failed probe, missing history and stale result', () => {
    const history: MonitorCandyHistory = { model: 'test', reasoning_effort: 'medium', interval_minutes: 1, results: [] }
    const now = Date.parse('2026-09-28T01:00:00Z')
    expect(candyDisplayState(history, now)).toBe('unknown')
    for (const verdict of ['correct', 'incorrect', 'error'] as const) {
      history.results = [{ checked_at: '2026-09-28T00:59:00Z', verdict, latency_ms: 3 }]
      expect(candyDisplayState(history, now)).toBe(verdict)
    }
    history.results[0].checked_at = '2026-09-28T00:55:00Z'
    expect(candyDisplayState(history, now)).toBe('stale')
  })
})

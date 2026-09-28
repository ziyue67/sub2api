import { describe, expect, it } from 'vitest'
import { candyDisplayState, hasMonitorSamples, monitorCardTimeline, monitorRefreshSeconds } from '../monitorCards'
import type { MonitorCandyHistory, MonitorCoverage, MonitorMatrixRow, MonitorMetric } from '@/api/channelMonitorV2'

describe('monitor card data semantics', () => {
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

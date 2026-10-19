import type { HealthState, MonitorCandyHistory, MonitorCandyResult, MonitorCoverage, MonitorMatrixBucket, MonitorMatrixRow, MonitorMetric } from '@/api/channelMonitorV2'

export const CANDY_HISTORY_LIMIT = 100
export const CANDY_HISTORY_LIMIT_MIN = 15
export const CANDY_HISTORY_LIMIT_MAX = 100
export const MONITOR_CARD_SLOT_COUNT = 18
/** Matches ChannelMonitorV2RecentSampleBucket. Sample bars are 5-minute slots, not the selected range bucket. */
export const MONITOR_CARD_SAMPLE_BUCKET_MS = 5 * 60 * 1000
export type MonitorCardTimelineMode = 'windows' | 'samples'

export function monitorCardTimelineMode(value: string | null | undefined): MonitorCardTimelineMode {
  return value === 'samples' ? 'samples' : 'windows'
}

export function candyHistoryLimit(history: { history_limit?: number } | null | undefined): number {
  const limit = history?.history_limit
  if (typeof limit === 'number' && Number.isInteger(limit) && limit >= CANDY_HISTORY_LIMIT_MIN && limit <= CANDY_HISTORY_LIMIT_MAX) return limit
  return CANDY_HISTORY_LIMIT
}

export function monitorCandyHistories(row: Pick<MonitorMatrixRow, 'candy' | 'candy_checks'>): MonitorCandyHistory[] {
  if (row.candy_checks && row.candy_checks.length > 0) return row.candy_checks
  return row.candy ? [row.candy] : []
}

export function candyHistorySlots(history: MonitorCandyHistory): Array<MonitorCandyResult | null> {
  const limit = candyHistoryLimit(history)
  const results = history.results.slice(-limit)
  return [...Array<null>(limit - results.length).fill(null), ...results]
}

export function hasMonitorSamples(metric: MonitorMetric): boolean {
  return metric.has_samples === true || metric.request_count > 0
}

// A recent-sample bar is a bucket the health score can use. Traffic below
// minimum_sample is not a sample. Redacted payloads keep has_samples and a
// scored band; overall "unknown" is the server's insufficient-sample result.
export function meetsMonitorSampleMinimum(bucket: Pick<MonitorMatrixBucket, 'metrics' | 'health'>): boolean {
  if (!bucket.metrics || !hasMonitorSamples(bucket.metrics)) return false
  const minimum = bucket.health?.minimum_sample
  const count = bucket.metrics.request_count
  if (typeof minimum === 'number' && minimum > 0 && count > 0) return count >= minimum
  if (count <= 0 && bucket.metrics.has_samples === true && bucket.health?.overall === 'unknown') return false
  return true
}

export function monitorRefreshSeconds(configSeconds: number | undefined, rows: readonly MonitorMatrixRow[], bootstrap: boolean): number {
  if (bootstrap) return 10
  let seconds = configSeconds && Number.isFinite(configSeconds) && configSeconds > 0 ? configSeconds : 300
  for (const row of rows) {
    for (const history of monitorCandyHistories(row)) {
      const interval = history.interval_minutes
      if (interval && Number.isFinite(interval) && interval > 0) {
        seconds = Math.min(seconds, interval * 60)
      }
    }
  }
  // Refresh before a healthy minute probe can appear stale in a five-minute snapshot.
  return Math.max(60, seconds)
}

export interface MonitorCardBar {
  start: number
  end: number
  state: HealthState
  observed: boolean
  buckets: MonitorMatrixBucket[]
}

function coverageBounds(coverage: MonitorCoverage | undefined) {
  const start = Date.parse(coverage?.requested_start || '')
  const end = Date.parse(coverage?.requested_end || coverage?.data_through || '')
  if (!Number.isFinite(start) || !Number.isFinite(end) || end <= start) return null
  return { start, end }
}

// Latest buckets that meet the minimum sample. sample_buckets already ignores
// the selected range. Older payloads only have range buckets, so those stay clipped.
export function monitorCardSampleTimeline(row: MonitorMatrixRow, coverage: MonitorCoverage | undefined): MonitorCardBar[] {
  const dedicated = Array.isArray(row.sample_buckets)
  const source = Array.isArray(row.sample_buckets) ? row.sample_buckets : row.buckets
  const bounds = dedicated ? null : coverageBounds(coverage)
  const widthMs = dedicated ? MONITOR_CARD_SAMPLE_BUCKET_MS : (coverage?.bucket_seconds && coverage.bucket_seconds > 0 ? coverage.bucket_seconds * 1000 : 0)
  return source
    .map(bucket => ({ bucket, at: Date.parse(bucket.bucket_start) }))
    .filter(item => Number.isFinite(item.at) && meetsMonitorSampleMinimum(item.bucket) && (!bounds || (item.at >= bounds.start && item.at < bounds.end)))
    .sort((a, b) => a.at - b.at)
    .slice(-MONITOR_CARD_SLOT_COUNT)
    .map(item => ({
      start: item.at,
      end: widthMs > 0 ? item.at + widthMs : item.at,
      state: item.bucket.health.overall,
      observed: true,
      buckets: [item.bucket],
    }))
}

// Keep real time gaps. Each bar summarizes observed health in one of 18 equal
// time windows, never interpolating a successful request into missing history.
export function monitorCardWindowTimeline(row: MonitorMatrixRow, coverage: MonitorCoverage | undefined): MonitorCardBar[] {
  const bounds = coverageBounds(coverage)
  if (!bounds) return []
  const { start, end } = bounds
  const width = (end - start) / MONITOR_CARD_SLOT_COUNT
  const bars: MonitorCardBar[] = Array.from({ length: MONITOR_CARD_SLOT_COUNT }, (_, index) => ({ start: start + index * width, end: start + (index + 1) * width, state: 'unknown', observed: false, buckets: [] }))
  const severity: Record<HealthState, number> = { unknown: 0, healthy: 1, warning: 2, critical: 3 }
  for (const bucket of row.buckets) {
    const at = Date.parse(bucket.bucket_start)
    if (at < start || at >= end || !hasMonitorSamples(bucket.metrics)) continue
    const bar = bars[Math.floor((at - start) / width)]
    if (!bar) continue
    bar.observed = true
    // Keep the original bucket metrics: redacted counts cannot weight rates,
    // and bucket medians cannot be combined into a time-window P50.
    bar.buckets.push(bucket)
    if (severity[bucket.health.overall] > severity[bar.state]) bar.state = bucket.health.overall
  }
  for (const bar of bars) bar.buckets.sort((a, b) => Date.parse(a.bucket_start) - Date.parse(b.bucket_start))
  return bars
}

export function monitorCardTimeline(row: MonitorMatrixRow, coverage: MonitorCoverage | undefined, mode: MonitorCardTimelineMode = 'windows') {
  return mode === 'samples' ? monitorCardSampleTimeline(row, coverage) : monitorCardWindowTimeline(row, coverage)
}

export function candyDisplayState(history: MonitorCandyHistory, now: number): 'correct' | 'incorrect' | 'error' | 'unknown' | 'stale' {
  const latest = history.results.at(-1)
  if (!latest) return 'unknown'
  const age = now - Date.parse(latest.checked_at)
  if (!Number.isFinite(age) || age > Math.max(180, history.interval_minutes * 60 + 120) * 1000) return 'stale'
  return latest.verdict
}

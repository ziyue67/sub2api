import type { UsageLog } from '@/types'
import { BILLING_MODE_VIDEO } from './billingMode'

type UsageTpsRow = Partial<
  Pick<UsageLog, 'output_tokens' | 'duration_ms' | 'first_token_ms' | 'image_count' | 'image_output_tokens' | 'billing_mode'>
>

/**
 * Average output throughput (tokens/s) over the recorded request duration.
 *
 * Use the same window for streaming and non-streaming records. Output tokens
 * can include reasoning generated before the first visible token, and buffered
 * tool output can arrive only at completion. Subtracting first_token_ms would
 * divide the full output count by an unrelated, potentially tiny window.
 *
 * This includes waiting time and is not a measurement of model generation speed.
 * Image/video requests and records without valid output or duration are excluded.
 */
export const usageOutputTps = (row: UsageTpsRow | null | undefined): number | null => {
  const outputTokens = row?.output_tokens ?? 0
  const durationMs = row?.duration_ms ?? 0
  if (!Number.isFinite(outputTokens) || !Number.isFinite(durationMs) || outputTokens <= 0 || durationMs <= 0) {
    return null
  }
  if ((row?.image_count ?? 0) > 0 || (row?.image_output_tokens ?? 0) > 0 || row?.billing_mode === BILLING_MODE_VIDEO) {
    return null
  }
  const tps = outputTokens / (durationMs / 1000)
  return Number.isFinite(tps) ? tps : null
}

/** "30.8 t/s"；100 t/s 及以上取整。不可计算时返回 null，由调用方显示占位符。 */
export const formatUsageOutputTps = (row: UsageTpsRow | null | undefined): string | null => {
  const tps = usageOutputTps(row)
  if (tps == null) return null
  return `${tps >= 100 ? Math.round(tps) : tps.toFixed(1)} t/s`
}

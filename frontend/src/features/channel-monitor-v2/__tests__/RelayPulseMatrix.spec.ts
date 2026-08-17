const i18nT = (key: string, params?: Record<string, unknown>) => {
  const map: Record<string, string> = {
    'channelMonitorV2.matrix.title': '色块矩阵',
    'channelMonitorV2.matrix.description': '趋势色块视图',
    'channelMonitorV2.bucket.minutes': '{count}分钟',
    'channelMonitorV2.matrix.wheelZoomX': '滚轮缩放横向',
    'channelMonitorV2.matrix.resetZoom': '重置缩放',
    'channelMonitorV2.matrix.dimension': '维度',
    'channelMonitorV2.metrics.successRate': '成功率',
    'channelMonitorV2.metrics.ttft': '首 Token',
    'channelMonitorV2.metrics.tps': '每秒 Token',
    'channelMonitorV2.metrics.cacheRate': '缓存率',
    'channelMonitorV2.matrix.scoreLine': '评分 {score}',
    'channelMonitorV2.metrics.successRateValue': '成功率 {value}',
    'channelMonitorV2.metrics.errorRateValue': '错误率 {value}',
    'channelMonitorV2.metrics.rpmValue': 'RPM {value}',
    'channelMonitorV2.metrics.tpmValue': 'TPM {value}',
    'channelMonitorV2.metrics.tpsValue': '每秒 Token {value}',
    'channelMonitorV2.metrics.ttftValue': '首 Token {value}',
    'channelMonitorV2.metrics.durationValue': '时长 {value}',
    'channelMonitorV2.metrics.cacheRateValue': '缓存率 {value}',
    'channelMonitorV2.matrix.noTrafficAt': '{time} 无流量',
    'channelMonitorV2.matrix.noTraffic': '无流量',
    'channelMonitorV2.matrix.legendAria': '图例',
    'channelMonitorV2.matrix.bad': '差',
    'channelMonitorV2.matrix.good': '好',
    'channelMonitorV2.matrix.healthyLegend': '健康',
    'channelMonitorV2.matrix.warningLegend': '警告',
    'channelMonitorV2.matrix.criticalLegend': '异常',
    'channelMonitorV2.matrix.unknownLegend': '未知',
  }
  const template = map[key] || key
  return template.replace(/\{(\w+)\}/g, (_, name) => String(params?.[name] ?? ''))
}

vi.mock('vue-i18n', async (importOriginal) => {
  const actual = await importOriginal<typeof import('vue-i18n')>()
  return {
    ...actual,
    useI18n: () => ({
      t: i18nT,
      te: (key: string) => key.startsWith('channelMonitorV2.'),
      locale: { value: 'zh' },
    }),
  }
})

import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import { afterEach, describe, expect, it, vi } from 'vitest'
import RelayPulseMatrix from '../RelayPulseMatrix.vue'
import type { MonitorHealth, MonitorMetric } from '@/api/channelMonitorV2'

const health: MonitorHealth = {
  overall: 'warning',
  error_rate: 'warning',
  ttft: 'healthy',
  cache: 'warning',
  score: 52,
  error_rate_score: 40,
  ttft_score: 100,
  cache_score: 50,
  minimum_sample: 20,
}

function metrics(requestCount: number): MonitorMetric {
  return {
    success_requests: requestCount ? requestCount - 1 : 0,
    error_requests: requestCount ? 1 : 0,
    request_count: requestCount,
    token_count: 100,
    rpm: 1.5,
    tpm: 10.2,
    error_rate: requestCount ? 1 / requestCount : 0,
    cache_rate: 0.5,
    cache_rate_numerator: 50,
    cache_rate_denominator: 100,
    ttft: { sample_count: requestCount, p50_ms: 100, p95_ms: 300, avg_ms: 150 },
    duration: { sample_count: requestCount, p50_ms: 500, p95_ms: 900, avg_ms: 600 },
    upstream_affected_requests: 2,
    upstream_attempt_count: 3,
  }
}

function installAnimationFrameMock() {
  let nextFrame = 1
  const callbacks = new Map<number, FrameRequestCallback>()
  const request = vi.fn((callback: FrameRequestCallback) => {
    const frame = nextFrame++
    callbacks.set(frame, callback)
    return frame
  })
  const cancel = vi.fn((frame: number) => {
    callbacks.delete(frame)
  })
  vi.stubGlobal('requestAnimationFrame', request)
  vi.stubGlobal('cancelAnimationFrame', cancel)

  return {
    callbacks,
    request,
    cancel,
    flushNext() {
      const next = callbacks.entries().next().value as [number, FrameRequestCallback] | undefined
      if (!next) throw new Error('No animation frame is pending')
      callbacks.delete(next[0])
      next[1](0)
    },
  }
}

function dispatchWheel(element: Element, init: WheelEventInit) {
  element.dispatchEvent(new WheelEvent('wheel', {
    bubbles: true,
    cancelable: true,
    ...init,
  }))
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('RelayPulseMatrix', () => {
  it('shows privacy-safe hover tooltips and multi-band colors without click modal', async () => {
    const wrapper = mount(RelayPulseMatrix, {
      props: {
        rows: [{
          platform: 'openai',
          group_id: 7,
          group_name: '默认组',
          model: 'gpt-5',
          metrics: metrics(10),
          health,
          buckets: [
            { bucket_start: '2026-08-01T00:00:00Z', metrics: metrics(10), health },
            { bucket_start: '2026-08-01T00:01:00Z', metrics: metrics(0), health },
          ],
        }],
        coverage: {
          requested_start: '2026-08-01T00:00:00Z',
          requested_end: '2026-08-01T00:03:00Z',
          coverage_start: '2026-08-01T00:00:00Z',
          data_through: '2026-08-01T00:03:00Z',
          computed_at: '2026-08-01T00:03:00Z',
          aggregation_lag_seconds: 0,
          coverage_complete: true,
          bucket_seconds: 60,
        },
        healthMode: 'overall',
      },
    })

    const cells = wrapper.findAll('.pulse-cell')
    expect(cells).toHaveLength(3)
    // Tooltip content exists once in the teleported overlay, not once per cell.
    expect(wrapper.findAll('.pulse-tooltip')).toHaveLength(0)
    expect(document.body.querySelectorAll('.matrix-floating-tooltip')).toHaveLength(1)
    await cells[0].trigger('mouseenter', { clientX: 100, clientY: 100 })
    const tip = document.body.querySelector('.matrix-floating-tooltip')?.textContent || ''
    expect(cells[0].attributes('aria-label')).toContain('成功率')
    expect(tip).toContain('成功率')
    expect(tip).toContain('90.0%')
    expect(tip).toContain('首 Token')
    expect(tip).toContain('每秒 Token')
    expect(tip).toContain('缓存率')
    expect(tip).toContain('RPM')
    expect(tip).not.toContain('请求数')
    expect(tip).not.toContain('用户错误')
    expect(tip).not.toContain('上游受影响')
    // Summary columns: success · ttft · tokens/s · cache
    const header = wrapper.find('.matrix-header').text()
    expect(header).toContain('成功率')
    expect(header).toContain('首 Token')
    expect(header).toContain('每秒 Token')
    expect(header).toContain('缓存率')
    // Multi-band class from score 52 → score5
    expect(cells[0].classes().some((c) => c.startsWith('health-score'))).toBe(true)
    // Redacted user payloads may have request_count=0 but still include score.
    expect(cells[1].classes().some((c) => c.startsWith('health-score'))).toBe(true)
    expect(cells[2].classes()).toContain('health-unknown')

    // No click-to-open modal
    await cells[0].trigger('click')
    expect(wrapper.find('[role="dialog"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('coalesces tooltip moves and cancels pending positioning work', async () => {
    const frames = installAnimationFrameMock()
    const wrapper = mount(RelayPulseMatrix, {
      props: {
        rows: [{
          platform: 'openai',
          group_id: 7,
          group_name: '默认组',
          model: 'gpt-5',
          metrics: metrics(10),
          health,
          buckets: [{ bucket_start: '2026-08-01T00:00:00Z', metrics: metrics(10), health }],
        }],
        coverage: {
          requested_start: '2026-08-01T00:00:00Z',
          requested_end: '2026-08-01T00:03:00Z',
          coverage_start: '2026-08-01T00:00:00Z',
          data_through: '2026-08-01T00:03:00Z',
          computed_at: '2026-08-01T00:03:00Z',
          aggregation_lag_seconds: 0,
          coverage_complete: true,
          bucket_seconds: 60,
        },
        healthMode: 'overall',
      },
    })

    const cell = wrapper.findAll('.pulse-cell')[0]
    await cell.trigger('mouseenter', { clientX: 100, clientY: 120 })
    await cell.trigger('mousemove', { clientX: 140, clientY: 170 })
    await cell.trigger('mousemove', { clientX: 180, clientY: 220 })

    expect(frames.request).toHaveBeenCalledTimes(1)
    expect(frames.callbacks.size).toBe(1)
    frames.flushNext()
    const tooltip = document.body.querySelector('.matrix-floating-tooltip') as HTMLElement
    expect(tooltip.style.left).toBe('180px')
    expect(tooltip.style.top).toBe('208px')

    await cell.trigger('mousemove', { clientX: 200, clientY: 240 })
    expect(frames.callbacks.size).toBe(1)
    await cell.trigger('mouseleave')
    expect(frames.callbacks.size).toBe(0)
    expect(frames.cancel).toHaveBeenCalledTimes(1)

    await cell.trigger('mouseenter', { clientX: 220, clientY: 260 })
    expect(frames.callbacks.size).toBe(1)
    wrapper.unmount()
    expect(frames.callbacks.size).toBe(0)
    expect(frames.cancel).toHaveBeenCalledTimes(2)
  })

  it('coalesces wheel updates and discards stale work on reset or range changes', async () => {
    const frames = installAnimationFrameMock()
    const coverage = {
      requested_start: '2026-08-01T00:00:00Z',
      requested_end: '2026-08-01T00:05:00Z',
      coverage_start: '2026-08-01T00:00:00Z',
      data_through: '2026-08-01T00:05:00Z',
      computed_at: '2026-08-01T00:05:00Z',
      aggregation_lag_seconds: 0,
      coverage_complete: true,
      bucket_seconds: 60,
    }
    const wrapper = mount(RelayPulseMatrix, {
      props: {
        rows: [{
          platform: 'openai',
          group_id: 7,
          group_name: '默认组',
          model: 'gpt-5',
          metrics: metrics(10),
          health,
          buckets: [{ bucket_start: '2026-08-01T00:00:00Z', metrics: metrics(10), health }],
        }],
        coverage,
        healthMode: 'overall',
      },
    })

    const matrix = wrapper.get('.matrix-scroll')
    dispatchWheel(matrix.element, { deltaX: 0, deltaY: -20, clientX: 100 })
    dispatchWheel(matrix.element, { deltaX: 0, deltaY: -30, clientX: 140 })
    dispatchWheel(matrix.element, { deltaX: 0, deltaY: -40, clientX: 180 })
    expect(frames.request).toHaveBeenCalledTimes(1)
    expect(frames.callbacks.size).toBe(1)

    frames.flushNext()
    await nextTick()
    const reset = wrapper.get('button')
    expect(reset.attributes('disabled')).toBeUndefined()

    dispatchWheel(matrix.element, { deltaX: 0, deltaY: -20, clientX: 200 })
    expect(frames.callbacks.size).toBe(1)
    await reset.trigger('click')
    expect(frames.callbacks.size).toBe(0)
    expect(reset.attributes('disabled')).toBeDefined()

    dispatchWheel(matrix.element, { deltaX: 0, deltaY: -20, clientX: 220 })
    expect(frames.callbacks.size).toBe(1)
    await wrapper.setProps({
      coverage: { ...coverage, requested_end: '2026-08-01T00:06:00Z' },
    })
    expect(frames.callbacks.size).toBe(0)
    expect(frames.cancel).toHaveBeenCalledTimes(2)
    wrapper.unmount()
  })
})

describe('RelayPulseMatrix axis range', () => {
  it('uses selected requested range for the X axis even with partial coverage', () => {
    const wrapper = mount(RelayPulseMatrix, {
      props: {
        rows: [{
          platform: 'openai',
          group_id: 7,
          group_name: '默认组',
          model: 'gpt-5',
          metrics: metrics(10),
          health,
          buckets: [
            { bucket_start: '2026-08-01T00:02:00Z', metrics: metrics(10), health },
          ],
        }],
        coverage: {
          requested_start: '2026-08-01T00:00:00Z',
          requested_end: '2026-08-01T00:05:00Z',
          coverage_start: '2026-08-01T00:02:00Z',
          data_through: '2026-08-01T00:03:00Z',
          computed_at: '2026-08-01T00:03:00Z',
          aggregation_lag_seconds: 0,
          coverage_complete: false,
          bucket_seconds: 60,
        },
        healthMode: 'overall',
      },
    })
    // 5 minutes @ 60s buckets → 5 cells spanning the selected range
    expect(wrapper.findAll('.pulse-cell')).toHaveLength(5)
    wrapper.unmount()
  })
})

import { describe, expect, it } from 'vitest'
import {
  formatMonitorV3Availability,
  formatMonitorV3Bucket,
  formatMonitorV3Multiplier,
  formatMonitorV3Offset,
  formatMonitorV3Rate,
  formatMonitorV3Slot,
  monitorV3CategoryLayout,
  monitorV3WindowRange,
} from '../monitorV3'
import type { MonitorV3CategoryStatus } from '@/api/channelMonitorV3'

describe('monitor V3 formatting', () => {
  it('formats availability like the status page', () => {
    expect(formatMonitorV3Availability(97.4612)).toBe('97.46%')
    expect(formatMonitorV3Availability(88.5966)).toBe('88.6%')
    expect(formatMonitorV3Availability(59.7)).toBe('59.7%')
    expect(formatMonitorV3Availability(100)).toBe('100%')
    expect(formatMonitorV3Availability(0)).toBe('0%')
    expect(formatMonitorV3Availability(null)).toBe('—')
    expect(formatMonitorV3Availability(undefined)).toBe('—')
  })

  it('never rounds a failed check up to 100%', () => {
    expect(formatMonitorV3Availability(99.996)).toBe('99.99%')
    expect(formatMonitorV3Availability(120)).toBe('100%')
  })

  it('formats multipliers and hides missing ones', () => {
    expect(formatMonitorV3Multiplier(0.2)).toBe('×0.2')
    expect(formatMonitorV3Multiplier(0.15)).toBe('×0.15')
    expect(formatMonitorV3Multiplier(1.2)).toBe('×1.2')
    expect(formatMonitorV3Multiplier(null)).toBe('')
    expect(formatMonitorV3Multiplier(undefined)).toBe('')
  })

  it('labels time zones by offset', () => {
    expect(formatMonitorV3Offset(0)).toBe('UTC')
    expect(formatMonitorV3Offset(-7)).toBe('UTC-7')
    expect(formatMonitorV3Offset(5.5)).toBe('UTC+5:30')
  })

  it('ends the window label at generation time, not at the open slot end', () => {
    const range = monitorV3WindowRange('2026-10-04T03:53:00Z', '2026-10-04T09:35:00Z', '2026-10-04T09:31:20Z')
    const end = new Date('2026-10-04T09:31:20Z')
    expect(range.to).toBe(`${end.getMonth() + 1}/${end.getDate()} ${String(end.getHours()).padStart(2, '0')}:${String(end.getMinutes()).padStart(2, '0')}`)
  })

  it('lets a category alone on the last row span both columns', () => {
    const category = (id: number) => ({ id, name: String(id), availability: null, components: [] }) as MonitorV3CategoryStatus
    expect(monitorV3CategoryLayout([1, 2, 3, 4, 5].map(category)).map((item) => item.fullWidth)).toEqual([false, false, false, false, true])
    expect(monitorV3CategoryLayout([1, 2].map(category)).map((item) => item.fullWidth)).toEqual([false, false])
    expect(monitorV3CategoryLayout([category(1)])[0].fullWidth).toBe(true)
  })

  it('formats passive slot details', () => {
    expect(formatMonitorV3Rate(0.98234)).toBe('98.23%')
    expect(formatMonitorV3Rate(1)).toBe('100%')
    expect(formatMonitorV3Rate(null)).toBe('—')
    expect(formatMonitorV3Bucket(2000)).toBe('≤ 2s')
    expect(formatMonitorV3Bucket(500)).toBe('≤ 0.5s')
    expect(formatMonitorV3Bucket(2147483647)).toBe('> 600s')
    expect(formatMonitorV3Bucket(undefined)).toBe('—')
    const start = new Date(2026, 9, 4, 15, 35)
    expect(formatMonitorV3Slot(start.toISOString(), 5)).toBe('10/4 15:35 - 15:40')
    const late = new Date(2026, 9, 4, 23, 30)
    expect(formatMonitorV3Slot(late.toISOString(), 60)).toBe('10/4 23:30 - 10/5 00:30')
  })
})

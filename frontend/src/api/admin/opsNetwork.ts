import { apiClient } from '../client'
export interface NetworkPoint {
  at: string; instance: string; egress_mbps: number | null; tcp_retrans_percent: number | null; qdisc_drops: number | null
  egress_status: string; tcp_status: string; qdisc_status: string; window_seconds: number
}
export interface NetworkSeries {
  points: NetworkPoint[]
  body_points: { at: string; bytes: number; samples: number }[]
  scope: string
}
export async function getNetworkSeries(signal?: AbortSignal) {
  return (await apiClient.get<NetworkSeries>('/admin/ops/dashboard/network-series', { params: { time_range: '1h' }, signal })).data
}

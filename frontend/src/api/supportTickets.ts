/**
 * Support tickets ("网站工单") — user and admin endpoints.
 * Every call answers SUPPORT_TICKET_DISABLED (403) while the feature switch is off.
 */

import { apiClient } from './client'
import type { PaginatedResponse } from '@/types'

export type SupportTicketStatus = 'pending' | 'processing' | 'replied' | 'closed'
/** List filter: '' = every status, 'open' = every status except closed. */
export type SupportTicketStatusFilter = '' | 'open' | SupportTicketStatus
export type SupportTicketRole = 'user' | 'admin'
/** Statuses an admin can choose when replying. */
export type SupportTicketReplyStatus = 'replied' | 'processing' | 'closed'

export const SUPPORT_TICKET_TITLE_MAX = 100
export const SUPPORT_TICKET_BODY_MAX = 5000
export const SUPPORT_TICKET_CATEGORY_MAX = 32
export const SUPPORT_TICKET_MAX_CATEGORIES = 20
export const SUPPORT_TICKET_NOTICE_MAX = 500
export const SUPPORT_TICKET_MAX_OPEN_LIMIT = 50

export interface SupportTicketUser {
  id: number
  email: string
  username: string
  status: string
  balance: number
  deleted: boolean
  created_at: string
}

export interface SupportTicket {
  id: number
  user_id: number
  category: string
  title: string
  status: SupportTicketStatus
  message_count: number
  last_message_at: string
  last_message_role: SupportTicketRole
  user_unread: boolean
  admin_unread: boolean
  closed_at?: string
  closed_by_role?: SupportTicketRole
  created_at: string
  updated_at: string
  /** Admin views only. */
  user?: SupportTicketUser
}

export interface SupportTicketMessage {
  id: number
  author_role: SupportTicketRole
  /** Which admin replied; admin views only. */
  author_name?: string
  body: string
  created_at: string
}

export interface SupportTicketDetail {
  ticket: SupportTicket
  messages: SupportTicketMessage[]
}

export interface SupportTicketUserSummary {
  unread_count: number
  open_count: number
  max_open: number
  categories: string[]
  notice: string
}

export interface SupportTicketAdminSummary {
  pending_count: number
  categories: string[]
}

/** Admin settings card (系统设置 → 功能 → 网站工单). */
export interface SupportTicketConfig {
  categories: string[]
  max_open_per_user: number
  notice: string
}

export interface CreateSupportTicketRequest {
  category: string
  title: string
  body: string
}

/** Length as the server counts it: Unicode code points, not UTF-16 units. */
export function supportTicketLength(text: string): number {
  return [...text].length
}

// ---- users ----

export async function getMySummary(): Promise<SupportTicketUserSummary> {
  const { data } = await apiClient.get<SupportTicketUserSummary>('/support-tickets/summary')
  return data
}

export async function listMyTickets(params: { status?: SupportTicketStatusFilter; page: number; page_size: number }): Promise<PaginatedResponse<SupportTicket>> {
  const { data } = await apiClient.get<PaginatedResponse<SupportTicket>>('/support-tickets', { params })
  return data
}

export async function createTicket(request: CreateSupportTicketRequest): Promise<SupportTicketDetail> {
  const { data } = await apiClient.post<SupportTicketDetail>('/support-tickets', request)
  return data
}

export async function getMyTicket(id: number): Promise<SupportTicketDetail> {
  const { data } = await apiClient.get<SupportTicketDetail>(`/support-tickets/${id}`)
  return data
}

export async function replyMyTicket(id: number, body: string): Promise<SupportTicketDetail> {
  const { data } = await apiClient.post<SupportTicketDetail>(`/support-tickets/${id}/messages`, { body })
  return data
}

export async function closeMyTicket(id: number): Promise<SupportTicketDetail> {
  const { data } = await apiClient.post<SupportTicketDetail>(`/support-tickets/${id}/close`)
  return data
}

export async function reopenMyTicket(id: number): Promise<SupportTicketDetail> {
  const { data } = await apiClient.post<SupportTicketDetail>(`/support-tickets/${id}/reopen`)
  return data
}

// ---- admins ----

export async function getAdminSummary(): Promise<SupportTicketAdminSummary> {
  const { data } = await apiClient.get<SupportTicketAdminSummary>('/admin/support-tickets/summary')
  return data
}

export async function listTickets(params: {
  status?: SupportTicketStatusFilter
  category?: string
  keyword?: string
  page: number
  page_size: number
}): Promise<PaginatedResponse<SupportTicket>> {
  const { data } = await apiClient.get<PaginatedResponse<SupportTicket>>('/admin/support-tickets', { params })
  return data
}

export async function getTicket(id: number): Promise<SupportTicketDetail> {
  const { data } = await apiClient.get<SupportTicketDetail>(`/admin/support-tickets/${id}`)
  return data
}

export async function replyTicket(id: number, body: string, status: SupportTicketReplyStatus): Promise<SupportTicketDetail> {
  const { data } = await apiClient.post<SupportTicketDetail>(`/admin/support-tickets/${id}/messages`, { body, status })
  return data
}

/** processing, closed, or pending to reopen a closed ticket. */
export async function setTicketStatus(id: number, status: 'processing' | 'closed' | 'pending'): Promise<SupportTicketDetail> {
  const { data } = await apiClient.post<SupportTicketDetail>(`/admin/support-tickets/${id}/status`, { status })
  return data
}

export async function deleteTicket(id: number): Promise<void> {
  await apiClient.delete(`/admin/support-tickets/${id}`)
}

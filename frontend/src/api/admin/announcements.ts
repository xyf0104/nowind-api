/**
 * Admin Announcements API endpoints
 */

import { apiClient } from '../client'
import type {
  Announcement,
  AnnouncementEmailDeliverySummary,
  AnnouncementEmailDispatchResult,
  AnnouncementEmailDispatchScope,
  AnnouncementUserReadStatus,
  BasePaginationResponse,
  CreateAnnouncementRequest,
  UpdateAnnouncementRequest
} from '@/types'

export async function list(
  page: number = 1,
  pageSize: number = 20,
  filters?: {
    status?: string
    search?: string
    sort_by?: string
    sort_order?: 'asc' | 'desc'
  },
  options?: {
    signal?: AbortSignal
  }
): Promise<BasePaginationResponse<Announcement>> {
  const { data } = await apiClient.get<BasePaginationResponse<Announcement>>('/admin/announcements', {
    params: { page, page_size: pageSize, ...filters },
    signal: options?.signal
  })
  return data
}

export async function getById(id: number): Promise<Announcement> {
  const { data } = await apiClient.get<Announcement>(`/admin/announcements/${id}`)
  return data
}

export async function create(request: CreateAnnouncementRequest): Promise<Announcement> {
  const { data } = await apiClient.post<Announcement>('/admin/announcements', request)
  return data
}

export async function update(id: number, request: UpdateAnnouncementRequest): Promise<Announcement> {
  const { data } = await apiClient.put<Announcement>(`/admin/announcements/${id}`, request)
  return data
}

export async function deleteAnnouncement(id: number): Promise<{ message: string }> {
  const { data } = await apiClient.delete<{ message: string }>(`/admin/announcements/${id}`)
  return data
}

export async function getReadStatus(
  id: number,
  page: number = 1,
  pageSize: number = 20,
  filters?: {
    search?: string
    sort_by?: string
    sort_order?: 'asc' | 'desc'
  },
  options?: {
    signal?: AbortSignal
  }
): Promise<BasePaginationResponse<AnnouncementUserReadStatus>> {
  const { data } = await apiClient.get<BasePaginationResponse<AnnouncementUserReadStatus>>(
    `/admin/announcements/${id}/read-status`,
    {
      params: { page, page_size: pageSize, ...filters },
      signal: options?.signal
    }
  )
  return data
}

export async function dispatchEmailNotifications(
  id: number,
  request: {
    scope: AnnouncementEmailDispatchScope
    user_ids?: number[]
  }
): Promise<AnnouncementEmailDispatchResult> {
  const { data } = await apiClient.post<AnnouncementEmailDispatchResult>(
    `/admin/announcements/${id}/email-notifications`,
    request
  )
  return data
}

export async function getEmailDeliverySummary(id: number): Promise<AnnouncementEmailDeliverySummary> {
  const { data } = await apiClient.get<AnnouncementEmailDeliverySummary>(
    `/admin/announcements/${id}/email-delivery-summary`
  )
  return data
}

const announcementsAPI = {
  list,
  getById,
  create,
  update,
  delete: deleteAnnouncement,
  getReadStatus,
  dispatchEmailNotifications,
  getEmailDeliverySummary
}

export default announcementsAPI

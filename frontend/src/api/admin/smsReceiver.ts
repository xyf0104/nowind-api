import { apiClient } from '../client'

export interface SMSReceiverQueueStatus {
  queued_count: number
  active_count: number
}

export interface SMSReceiverSession {
  session_id?: string
  status: string
  number?: string
  country?: string
  code?: string
  queued_count: number
}

export interface SMSReceiverAddResult {
  added_count: number
  queued_count: number
}

export interface SMSReceiverClearResult {
  deleted_count: number
  queued_count: number
}

export async function getStatus(): Promise<SMSReceiverQueueStatus> {
  const { data } = await apiClient.get<SMSReceiverQueueStatus>('/admin/settings/sms-receiver')
  return data
}

export async function addCardKeys(cardKeys: string): Promise<SMSReceiverAddResult> {
  const { data } = await apiClient.post<SMSReceiverAddResult>('/admin/settings/sms-receiver/card-keys', {
    card_keys: cardKeys
  })
  return data
}

export async function clearCardKeys(): Promise<SMSReceiverClearResult> {
  const { data } = await apiClient.delete<SMSReceiverClearResult>('/admin/settings/sms-receiver/card-keys')
  return data
}

export async function redeem(): Promise<SMSReceiverSession> {
  const { data } = await apiClient.post<SMSReceiverSession>('/admin/settings/sms-receiver/redeem')
  return data
}

export async function resume(sessionID: string): Promise<SMSReceiverSession> {
  const { data } = await apiClient.post<SMSReceiverSession>(`/admin/settings/sms-receiver/sessions/${encodeURIComponent(sessionID)}/resume`)
  return data
}

export async function check(sessionID: string): Promise<SMSReceiverSession> {
  const { data } = await apiClient.post<SMSReceiverSession>(`/admin/settings/sms-receiver/sessions/${encodeURIComponent(sessionID)}/check`)
  return data
}

export async function change(sessionID: string): Promise<SMSReceiverSession> {
  const { data } = await apiClient.post<SMSReceiverSession>(`/admin/settings/sms-receiver/sessions/${encodeURIComponent(sessionID)}/change`)
  return data
}

export async function cancel(sessionID: string): Promise<SMSReceiverSession> {
  const { data } = await apiClient.post<SMSReceiverSession>(`/admin/settings/sms-receiver/sessions/${encodeURIComponent(sessionID)}/cancel`)
  return data
}

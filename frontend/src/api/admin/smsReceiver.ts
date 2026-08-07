import { apiClient } from '../client'

export interface SMSReceiverQueueStatus {
  queued_count: number
  active_count: number
  available?: boolean
  fee_amount?: number
  balance?: number
}

export interface SMSReceiverSession {
  session_id?: string
  status: string
  number?: string
  country?: string
  code?: string
  queued_count: number
  fee_amount?: number
  charge_state?: 'held' | 'captured' | 'released' | ''
  balance?: number
  action_available_at?: string
}

export interface SMSReceiverAddResult {
  added_count: number
  queued_count: number
}

export interface SMSReceiverClearResult {
  deleted_count: number
  queued_count: number
}

export interface SMSReceiverMemberFeeResult {
  fee_amount: number
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

export async function updateMemberFee(memberFee: number): Promise<SMSReceiverMemberFeeResult> {
  const { data } = await apiClient.put<SMSReceiverMemberFeeResult>('/admin/settings/sms-receiver/member-fee', {
    member_fee: memberFee
  })
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

// Member endpoints never receive card keys or global queue totals. They use
// the same browser-safe session shape as the admin tool plus fee/balance data.
const memberBasePath = '/sms-receiver'

export const memberSMSReceiverAPI = {
  async getStatus(): Promise<SMSReceiverQueueStatus> {
    const { data } = await apiClient.get<SMSReceiverQueueStatus>(memberBasePath)
    return data
  },
  async redeem(): Promise<SMSReceiverSession> {
    const { data } = await apiClient.post<SMSReceiverSession>(`${memberBasePath}/redeem`)
    return data
  },
  async resume(sessionID: string): Promise<SMSReceiverSession> {
    const { data } = await apiClient.post<SMSReceiverSession>(`${memberBasePath}/sessions/${encodeURIComponent(sessionID)}/resume`)
    return data
  },
  async check(sessionID: string): Promise<SMSReceiverSession> {
    const { data } = await apiClient.post<SMSReceiverSession>(`${memberBasePath}/sessions/${encodeURIComponent(sessionID)}/check`)
    return data
  },
  async change(sessionID: string): Promise<SMSReceiverSession> {
    const { data } = await apiClient.post<SMSReceiverSession>(`${memberBasePath}/sessions/${encodeURIComponent(sessionID)}/change`)
    return data
  },
  async cancel(sessionID: string): Promise<SMSReceiverSession> {
    const { data } = await apiClient.post<SMSReceiverSession>(`${memberBasePath}/sessions/${encodeURIComponent(sessionID)}/cancel`)
    return data
  }
}

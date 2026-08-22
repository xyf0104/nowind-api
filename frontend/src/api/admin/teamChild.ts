import { apiClient } from '../client'

export interface TeamChildMailboxStatus {
  configured: boolean
  browser_configured: boolean
}

export interface TeamChildBrowserSession {
  embed_url: string
  expires_at: string
}

export interface TeamChildMailbox {
  session_id: string
  email: string
  expires_at: string
}

export interface TeamChildMailboxCode {
  status: 'waiting' | 'received'
  code?: string
  expires_at: string
}

export interface TeamChildMailboxConfigImportResult {
  configured: boolean
  auth_mode: string
  domain: string
  restart_required: boolean
}

export interface TeamChildAuthURL {
  auth_url: string
  session_id: string
}

export interface TeamChildCreateAccountRequest {
  session_id: string
  code: string
  state: string
  redirect_uri?: string
  proxy_id?: number
  name: string
  concurrency: number
  priority: number
  group_ids: number[]
}

export interface TeamChildMember {
  /** Stable identity returned by the automation service: normalized email. */
  id: string
  email: string
  name?: string
  role: string
  seat_type?: string
  added_at?: string
  status?: string
}

export interface TeamChildMembersResult {
  ready: boolean
  url?: string
  members: TeamChildMember[]
  pending_invites?: number
  workspace_name?: string
  message?: string
  seat_email?: string
  operation?: {
    type: 'inspect' | 'invite' | 'update' | 'remove'
    email?: string
    role?: string
    confirmed: boolean
  }
}

export async function getMailboxStatus(): Promise<TeamChildMailboxStatus> {
  const { data } = await apiClient.get<TeamChildMailboxStatus>('/admin/openai/team-child/mailbox-status')
  return data
}

export async function createMailbox(): Promise<TeamChildMailbox> {
  const { data } = await apiClient.post<TeamChildMailbox>('/admin/openai/team-child/mailboxes')
  return data
}

export async function importMailboxConfig(file: File): Promise<TeamChildMailboxConfigImportResult> {
  const formData = new FormData()
  formData.append('file', file, file.name)
  const { data } = await apiClient.post<TeamChildMailboxConfigImportResult>(
    '/admin/openai/team-child/mailbox-config',
    formData,
    { headers: { 'Content-Type': 'multipart/form-data' } }
  )
  return data
}

export async function createBrowserSession(): Promise<TeamChildBrowserSession> {
  const { data } = await apiClient.post<TeamChildBrowserSession>(
    '/admin/openai/team-child/browser-sessions'
  )
  return data
}

export async function listTeamChildMembers(): Promise<TeamChildMembersResult> {
  const { data } = await apiClient.get<TeamChildMembersResult>('/admin/openai/team-child/members')
  return data
}

export async function refreshTeamChildMembers(): Promise<TeamChildMembersResult> {
  const { data } = await apiClient.post<TeamChildMembersResult>('/admin/openai/team-child/members/refresh')
  return data
}

export async function inspectTeamChildSeat(): Promise<TeamChildMembersResult> {
  const { data } = await apiClient.post<TeamChildMembersResult>('/admin/openai/team-child/members/inspect')
  return data
}

export async function inviteTeamChildMember(email: string): Promise<TeamChildMembersResult> {
  const { data } = await apiClient.post<TeamChildMembersResult>('/admin/openai/team-child/members/invite', { email })
  return data
}

export async function updateTeamChildMember(email: string, role: string): Promise<TeamChildMembersResult> {
  const { data } = await apiClient.patch<TeamChildMembersResult>('/admin/openai/team-child/members', { email, role })
  return data
}

export async function removeTeamChildMember(email: string): Promise<TeamChildMembersResult> {
  const { data } = await apiClient.delete<TeamChildMembersResult>('/admin/openai/team-child/members', { data: { email } })
  return data
}

export async function pollMailboxCode(sessionId: string): Promise<TeamChildMailboxCode> {
  const { data } = await apiClient.get<TeamChildMailboxCode>(
    `/admin/openai/team-child/mailboxes/${encodeURIComponent(sessionId)}/code`
  )
  return data
}

export async function deleteMailboxSession(sessionId: string): Promise<void> {
  await apiClient.delete(`/admin/openai/team-child/mailboxes/${encodeURIComponent(sessionId)}`)
}

export async function generateOpenAIAuthUrl(config: { proxy_id?: number; redirect_uri?: string } = {}): Promise<TeamChildAuthURL> {
  const { data } = await apiClient.post<TeamChildAuthURL>('/admin/openai/generate-auth-url', config)
  return data
}

export async function createOpenAIAccountFromOAuth(payload: TeamChildCreateAccountRequest) {
  const { data } = await apiClient.post('/admin/openai/create-from-oauth', payload)
  return data as { id?: number; name?: string; email?: string; status?: string; [key: string]: unknown }
}

export const teamChildAPI = {
  getMailboxStatus,
  createBrowserSession,
  listTeamChildMembers,
  refreshTeamChildMembers,
  inspectTeamChildSeat,
  inviteTeamChildMember,
  updateTeamChildMember,
  removeTeamChildMember,
  createMailbox,
  importMailboxConfig,
  pollMailboxCode,
  deleteMailboxSession,
  generateOpenAIAuthUrl,
  createOpenAIAccountFromOAuth
}

export default teamChildAPI

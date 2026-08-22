import { apiClient } from '../client'

export interface TeamChildMailboxStatus {
  configured: boolean
  browser_configured: boolean
}

export interface TeamChildBrowserSession {
  embed_url: string
  expires_at: string
  ticket_expires_at?: string
  controller_id: string
  control_expires_at: string
}

export interface TeamChildBrowserSessionRequest {
  controller_id?: string
  /** Explicitly replace the current graphical-browser controller. */
  take_over?: boolean
}

export interface TeamChildBrowserControl {
  controller_id: string
  control_expires_at?: string
  released?: boolean
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

export type TeamChildWorkflowStatus = 'running' | 'manual_required' | 'callback_ready' | 'failed' | 'cancelled'

export interface TeamChildWorkflowStep {
  key: 'members' | 'remove' | 'invite' | 'oauth' | 'verify'
  number: number
  label: string
  status: 'pending' | 'running' | 'waiting' | 'completed' | 'failed' | 'cancelled'
  message?: string
}

export interface TeamChildWorkflow {
  id: string
  status: TeamChildWorkflowStatus
  expires_at: string
  manual_required: boolean
  callback_url?: string
  error?: string
  steps: TeamChildWorkflowStep[]
}

export interface StartTeamChildWorkflowRequest {
  seat_email: string
  invite_email: string
  auth_url: string
  /** Must be set only after the XIASS in-page destructive-action dialog. */
  confirmed: true
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

export async function createBrowserSession(payload: TeamChildBrowserSessionRequest = {}): Promise<TeamChildBrowserSession> {
  const { data } = await apiClient.post<TeamChildBrowserSession>(
    '/admin/openai/team-child/browser-sessions',
    payload
  )
  return data
}

export async function heartbeatTeamChildBrowserControl(controllerID: string): Promise<TeamChildBrowserControl> {
  const { data } = await apiClient.post<TeamChildBrowserControl>('/admin/openai/team-child/browser-control/heartbeat', {
    controller_id: controllerID
  })
  return data
}

export async function releaseTeamChildBrowserControl(controllerID: string): Promise<TeamChildBrowserControl> {
  const { data } = await apiClient.delete<TeamChildBrowserControl>('/admin/openai/team-child/browser-control', {
    data: { controller_id: controllerID }
  })
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

export async function startTeamChildWorkflow(payload: StartTeamChildWorkflowRequest): Promise<TeamChildWorkflow> {
  const { data } = await apiClient.post<TeamChildWorkflow>('/admin/openai/team-child/workflows', payload)
  return data
}

export async function getTeamChildWorkflow(workflowID: string): Promise<TeamChildWorkflow> {
  const { data } = await apiClient.get<TeamChildWorkflow>(`/admin/openai/team-child/workflows/${encodeURIComponent(workflowID)}`)
  return data
}

export async function cancelTeamChildWorkflow(workflowID: string): Promise<TeamChildWorkflow> {
  const { data } = await apiClient.delete<TeamChildWorkflow>(`/admin/openai/team-child/workflows/${encodeURIComponent(workflowID)}`)
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
  heartbeatTeamChildBrowserControl,
  releaseTeamChildBrowserControl,
  listTeamChildMembers,
  refreshTeamChildMembers,
  inspectTeamChildSeat,
  inviteTeamChildMember,
  updateTeamChildMember,
  removeTeamChildMember,
  startTeamChildWorkflow,
  getTeamChildWorkflow,
  cancelTeamChildWorkflow,
  createMailbox,
  importMailboxConfig,
  pollMailboxCode,
  deleteMailboxSession,
  generateOpenAIAuthUrl,
  createOpenAIAccountFromOAuth
}

export default teamChildAPI

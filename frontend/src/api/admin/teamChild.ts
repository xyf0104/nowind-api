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

export interface TeamChildBrowserNavigationResult {
  ok?: boolean
  url?: string
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
  /** Marks the imported account for the Team-child 401 reminder. */
  team_child?: boolean
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
  /** Owner/admin rows and instance-configured protected identities are read-only. */
  protected?: boolean
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

export type TeamChildWorkflowStatus = 'running' | 'manual_required' | 'callback_ready' | 'completed' | 'failed' | 'cancelled'

export interface TeamChildWorkflowStep {
  key: 'members' | 'remove' | 'invite' | 'oauth' | 'verify'
  number: number
  label: string
  status: 'pending' | 'running' | 'waiting' | 'completed' | 'failed' | 'cancelled'
  message?: string
}

export type TeamChildWorkflowNodeKey =
  | 'members' | 'remove' | 'invite' | 'invite_confirm' | 'oauth' | 'signup' | 'email' | 'password'
  | 'mail' | 'mailbox' | 'email_code' | 'phone' | 'sms_confirm' | 'phone_submit'
  | 'sms_poll' | 'sms_code' | 'profile_wait' | 'profile' | 'workspace_wait'
  | 'workspace' | 'callback' | 'import'

export interface TeamChildWorkflowNode {
  key: TeamChildWorkflowNodeKey
  number: number
  label: string
  status: TeamChildWorkflowStep['status']
  message?: string
}

export interface TeamChildWorkflow {
  id: string
  status: TeamChildWorkflowStatus
  expires_at: string
  manual_required: boolean
  oauth_session_id?: string
  oauth_state?: string
  current_node?: TeamChildWorkflowNodeKey | ''
  callback_url?: string
  error?: string
  steps: TeamChildWorkflowStep[]
  nodes?: TeamChildWorkflowNode[]
}

export interface ActiveTeamChildWorkflowResult {
  active: boolean
  workflow?: TeamChildWorkflow
}

export interface StartTeamChildWorkflowRequest {
  /** Empty only when the ordinary member seat was already removed manually. */
  seat_email?: string
  invite_email: string
  auth_url: string
  oauth_session_id: string
  seat_already_removed: boolean
  start_step?: TeamChildWorkflowStepKey
  run_only_step?: boolean
  /** Must be set only after the XIASS in-page destructive-action dialog. */
  confirmed: true
}

export type TeamChildWorkflowStepKey = TeamChildWorkflowStep['key']

export async function getMailboxStatus(): Promise<TeamChildMailboxStatus> {
  const { data } = await apiClient.get<TeamChildMailboxStatus>('/admin/openai/team-child/mailbox-status')
  return data
}

export async function createMailbox(): Promise<TeamChildMailbox> {
  const { data } = await apiClient.post<TeamChildMailbox>('/admin/openai/team-child/mailboxes')
  return data
}

export async function getActiveMailbox(): Promise<TeamChildMailbox | null> {
  const { data } = await apiClient.get<{ active: boolean; mailbox?: TeamChildMailbox }>('/admin/openai/team-child/mailboxes/active')
  return data.active && data.mailbox ? data.mailbox : null
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

/** Navigate the already-mounted server Chromium tab without opening a local tab. */
export async function navigateTeamChildBrowser(url: string): Promise<TeamChildBrowserNavigationResult> {
  const { data } = await apiClient.post<TeamChildBrowserNavigationResult>(
    '/admin/openai/team-child/browser/navigate',
    { url }
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

export async function getActiveTeamChildWorkflow(): Promise<TeamChildWorkflow | null> {
  const { data } = await apiClient.get<ActiveTeamChildWorkflowResult>('/admin/openai/team-child/workflows/active')
  return data.active && data.workflow ? data.workflow : null
}

export async function continueTeamChildWorkflow(workflowID: string): Promise<TeamChildWorkflow> {
  const { data } = await apiClient.post<TeamChildWorkflow>(`/admin/openai/team-child/workflows/${encodeURIComponent(workflowID)}/continue`)
  return data
}

export async function runTeamChildWorkflowStep(workflowID: string, step: TeamChildWorkflowStepKey): Promise<TeamChildWorkflow> {
  const { data } = await apiClient.post<TeamChildWorkflow>(
    `/admin/openai/team-child/workflows/${encodeURIComponent(workflowID)}/run-step`,
    { step }
  )
  return data
}

export async function submitTeamChildWorkflowCallback(workflowID: string, callbackURL: string): Promise<TeamChildWorkflow> {
  const { data } = await apiClient.post<TeamChildWorkflow>(
    `/admin/openai/team-child/workflows/${encodeURIComponent(workflowID)}/callback`,
    { callback_url: callbackURL }
  )
  return data
}

export async function restartTeamChildWorkflowOAuth(workflowID: string, authURL: string, oauthSessionID: string): Promise<TeamChildWorkflow> {
  const { data } = await apiClient.post<TeamChildWorkflow>(
    `/admin/openai/team-child/workflows/${encodeURIComponent(workflowID)}/restart-oauth`,
    { auth_url: authURL, oauth_session_id: oauthSessionID }
  )
  return data
}

export async function submitTeamChildWorkflowEmailCode(workflowID: string, code: string): Promise<TeamChildWorkflow> {
  const { data } = await apiClient.post<TeamChildWorkflow>(
    `/admin/openai/team-child/workflows/${encodeURIComponent(workflowID)}/email-code`,
    { code }
  )
  return data
}

export async function submitTeamChildWorkflowPhone(workflowID: string, phone: string): Promise<TeamChildWorkflow> {
  const { data } = await apiClient.post<TeamChildWorkflow>(
    `/admin/openai/team-child/workflows/${encodeURIComponent(workflowID)}/phone`,
    { phone }
  )
  return data
}

export async function submitTeamChildWorkflowSMSCode(workflowID: string, code: string): Promise<TeamChildWorkflow> {
  const { data } = await apiClient.post<TeamChildWorkflow>(
    `/admin/openai/team-child/workflows/${encodeURIComponent(workflowID)}/sms-code`,
    { code }
  )
  return data
}

export async function completeTeamChildWorkflow(workflowID: string): Promise<TeamChildWorkflow> {
  const { data } = await apiClient.post<TeamChildWorkflow>(
    `/admin/openai/team-child/workflows/${encodeURIComponent(workflowID)}/complete`
  )
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

export async function createOpenAIAccountFromOAuth(payload: TeamChildCreateAccountRequest) {
  const { data } = await apiClient.post('/admin/openai/create-from-oauth', payload)
  return data as { id?: number; name?: string; email?: string; status?: string; [key: string]: unknown }
}

export const teamChildAPI = {
  getMailboxStatus,
  createBrowserSession,
  navigateTeamChildBrowser,
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
  getActiveTeamChildWorkflow,
  continueTeamChildWorkflow,
  runTeamChildWorkflowStep,
  submitTeamChildWorkflowCallback,
  restartTeamChildWorkflowOAuth,
  submitTeamChildWorkflowEmailCode,
  submitTeamChildWorkflowPhone,
  submitTeamChildWorkflowSMSCode,
  completeTeamChildWorkflow,
  cancelTeamChildWorkflow,
  createMailbox,
  getActiveMailbox,
  importMailboxConfig,
  pollMailboxCode,
  deleteMailboxSession,
  createOpenAIAccountFromOAuth
}

export default teamChildAPI

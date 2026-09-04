import { apiClient } from '../client'

export interface ExecutionNodeAccountStats {
  total: number
  active: number
  schedulable: number
}

export interface ExecutionNodeAdminIssue {
  code: string
  severity: 'error' | 'warning' | string
  message: string
}

export interface ExecutionNodeRuntimeStatus {
  enabled: boolean
  node_id: string
  default_proxy_id: number
  emergency_local_egress: boolean
  control_plane: boolean
  legacy_unassigned_node_id: string
  legacy_unassigned_proxy_id: number
}

export interface ExecutionNodeAdminNode {
  node_id: string
  weight: number
  proxy_id: number
  proxy_name?: string
  proxy_status?: string
  proxy_valid: boolean
  online: boolean
  is_local: boolean
  account_stats: ExecutionNodeAccountStats
}

export interface ExecutionNodeAdminStatus {
  balancing_enabled: boolean
  can_enable: boolean
  database_reachable: boolean
  heartbeat_store_reachable: boolean
  runtime: ExecutionNodeRuntimeStatus
  nodes: ExecutionNodeAdminNode[]
  issues: ExecutionNodeAdminIssue[]
}

export interface ExecutionNodePairingPeer {
  node_id: string
  version?: string
  protocol_version: number
  database_fingerprint: string
  redis_fingerprint: string
  auth_fingerprint: string
  state_fingerprint: string
  paired_at: string
  peer_url?: string
  ready: boolean
}

export interface ExecutionNodePairingStatus {
  protocol_version: number
  local_node_id: string
  paired: boolean
  production_ready: boolean
  protocol_compatible: boolean
  database_shared: boolean
  redis_shared: boolean
  auth_compatible: boolean
  invite_active: boolean
  invite_expires_at?: string
  state_fingerprint?: string
  state_error?: string
  peer?: ExecutionNodePairingPeer
}

export interface ExecutionNodePairingInvite {
  token: string
  expires_at: string
}

export interface ExecutionNodeRuntimeConfig {
  node_id: string
  default_proxy_id: number
  legacy_unassigned_node_id: string
  legacy_unassigned_proxy_id: number
}

export async function getStatus(): Promise<ExecutionNodeAdminStatus> {
  const { data } = await apiClient.get<ExecutionNodeAdminStatus>('/admin/settings/execution-nodes/status')
  return data
}

export async function getPairingStatus(): Promise<ExecutionNodePairingStatus> {
  const { data } = await apiClient.get<ExecutionNodePairingStatus>('/admin/settings/execution-nodes/pairing')
  return data
}

export async function initializeRuntime(nodeID: string): Promise<ExecutionNodeRuntimeConfig> {
  const { data } = await apiClient.post<ExecutionNodeRuntimeConfig>('/admin/settings/execution-nodes/runtime/initialize', { node_id: nodeID })
  return data
}

export async function generatePairingInvite(): Promise<ExecutionNodePairingInvite> {
  const { data } = await apiClient.post<ExecutionNodePairingInvite>('/admin/settings/execution-nodes/pairing/invite')
  return data
}

export async function pairExecutionNode(peerURL: string, token: string, targetNodeID: string, targetURL: string): Promise<ExecutionNodePairingStatus> {
  const { data } = await apiClient.post<ExecutionNodePairingStatus>('/admin/settings/execution-nodes/pairing/join', {
    peer_url: peerURL,
    token,
    target_node_id: targetNodeID,
    target_url: targetURL
  })
  return data
}

export async function unpairExecutionNode(): Promise<void> {
  await apiClient.post('/admin/settings/execution-nodes/pairing/unpair')
}

export const executionNodesAPI = { getStatus, getPairingStatus, initializeRuntime, generatePairingInvite, pairExecutionNode, unpairExecutionNode }

export default executionNodesAPI

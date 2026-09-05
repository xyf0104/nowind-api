import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

import ExecutionNodesView from '../ExecutionNodesView.vue'
import type { ExecutionNodeAdminStatus } from '@/api/admin/executionNodes'

const { getStatus, getPairingStatus, initializeRuntime, updateOfflineTakeover, generatePairingInvite, pairExecutionNode, unpairExecutionNode, getAllWithCount, updateSettings, showError, showSuccess } = vi.hoisted(() => ({
  getStatus: vi.fn(),
  getPairingStatus: vi.fn(),
  initializeRuntime: vi.fn(),
  updateOfflineTakeover: vi.fn(),
  generatePairingInvite: vi.fn(),
  pairExecutionNode: vi.fn(),
  unpairExecutionNode: vi.fn(),
  getAllWithCount: vi.fn(),
  updateSettings: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    executionNodes: { getStatus, getPairingStatus, initializeRuntime, updateOfflineTakeover, generatePairingInvite, pairExecutionNode, unpairExecutionNode },
    proxies: { getAllWithCount },
    settings: { updateSettings }
  }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError, showSuccess })
}))

vi.mock('@/utils/apiError', () => ({
  extractApiErrorMessage: (_error: unknown, fallback: string) => fallback,
  extractI18nErrorMessage: (_error: unknown, _t: unknown, _prefix: string, fallback: string) => fallback
}))

vi.mock('vue-i18n', async (importOriginal) => {
  const actual = await importOriginal<typeof import('vue-i18n')>()
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key })
  }
})

const ToggleStub = {
  inheritAttrs: false,
  props: {
    modelValue: { type: Boolean, required: true },
    disabled: { type: Boolean, default: false }
  },
  emits: ['update:modelValue'],
  template: '<button type="button" v-bind="$attrs" :disabled="disabled" @click="$emit(\'update:modelValue\', !modelValue)" />'
}

const ConfirmDialogStub = {
  props: { show: { type: Boolean, default: false } },
  emits: ['confirm', 'cancel'],
  template: '<button v-if="show" type="button" data-testid="execution-node-confirm" @click="$emit(\'confirm\')" />'
}

function statusFixture(overrides: Partial<ExecutionNodeAdminStatus> = {}): ExecutionNodeAdminStatus {
  return {
    balancing_enabled: false,
    can_enable: true,
    database_reachable: true,
    heartbeat_store_reachable: true,
    runtime: {
      enabled: true,
      node_id: 'api',
      default_proxy_id: 84,
      emergency_local_egress: true,
      control_plane: true,
      legacy_unassigned_node_id: 'api',
      legacy_unassigned_proxy_id: 84
    },
    nodes: [
      {
        node_id: 'api',
        weight: 3,
        proxy_id: 84,
        proxy_name: 'US egress',
        proxy_status: 'active',
        proxy_valid: true,
        online: true,
        is_local: true,
        account_stats: { total: 5, active: 4, schedulable: 3 }
      },
      {
        node_id: 'api2',
        weight: 1,
        proxy_id: 83,
        proxy_name: 'JP egress',
        proxy_status: 'active',
        proxy_valid: true,
        online: true,
        is_local: false,
        account_stats: { total: 2, active: 2, schedulable: 1 }
      }
    ],
    issues: [],
    ...overrides
  }
}

function mountView() {
  return mount(ExecutionNodesView, {
    global: {
      stubs: {
        AppLayout: { template: '<main><slot /></main>' },
        Toggle: ToggleStub,
        ConfirmDialog: ConfirmDialogStub,
        Icon: true
      }
    }
  })
}

describe('ExecutionNodesView', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    getStatus.mockResolvedValue(statusFixture())
    getPairingStatus.mockResolvedValue({
      protocol_version: 1,
      local_node_id: 'api',
      paired: false,
      production_ready: false,
      protocol_compatible: false,
      database_shared: false,
      redis_shared: false,
      auth_compatible: false,
      invite_active: false
    })
    generatePairingInvite.mockResolvedValue({ token: 'a'.repeat(64), expires_at: '2026-09-04T12:10:00Z' })
    initializeRuntime.mockResolvedValue({ node_id: 'api', tunnel_token: 'b'.repeat(64), default_proxy_id: 84, legacy_unassigned_node_id: 'api', legacy_unassigned_proxy_id: 84 })
    updateOfflineTakeover.mockResolvedValue(undefined)
    pairExecutionNode.mockResolvedValue({
      protocol_version: 1,
      local_node_id: 'api',
      paired: true,
      production_ready: true,
      protocol_compatible: true,
      database_shared: true,
      redis_shared: true,
      auth_compatible: true,
      invite_active: false,
      peer: { node_id: 'api2', protocol_version: 1, database_fingerprint: 'db', redis_fingerprint: 'redis', auth_fingerprint: 'auth', state_fingerprint: 'state', paired_at: '2026-09-04T12:00:00Z' }
    })
    unpairExecutionNode.mockResolvedValue(undefined)
    getAllWithCount.mockResolvedValue([
      { id: 84, name: 'US egress' },
      { id: 83, name: 'JP egress' }
    ])
    updateSettings.mockResolvedValue({})
  })

  it('loads node health, account counts, and current weights', async () => {
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.get('[data-testid="execution-node-label-0"]').text()).toContain('admin.executionNodes.localNode')
    expect(wrapper.get('[data-testid="execution-node-label-1"]').text()).toContain('admin.executionNodes.otherNode')
    expect(wrapper.get('[data-testid="execution-node-weight-0"]').element).toHaveProperty('value', '3')
    expect(wrapper.text()).toContain('5')
    expect(wrapper.find('[data-testid="execution-node-proxy-1"]').exists()).toBe(false)
  })

  it('renders disabled multi-node runtime as a neutral single-node state', async () => {
    getStatus.mockResolvedValue(statusFixture({
      can_enable: false,
      runtime: {
        ...statusFixture().runtime,
        enabled: false,
        node_id: '',
        default_proxy_id: 0,
        legacy_unassigned_proxy_id: 0
      },
      issues: [{ code: 'MULTI_NODE_DISABLED', severity: 'info', message: 'single node is healthy' }]
    }))
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.text()).toContain('single node is healthy')
    expect(wrapper.text()).not.toContain('MULTI_NODE_DISABLED')
    expect(wrapper.text()).not.toContain('LOCAL_NODE_ID_INVALID')
    expect(wrapper.text()).not.toContain('NODE_PROXY_UNAVAILABLE')
  })

  it('renders older server responses with null node collections', async () => {
    getStatus.mockResolvedValue({
      ...statusFixture({
        can_enable: false,
        runtime: { ...statusFixture().runtime, enabled: false, node_id: '', default_proxy_id: 0, legacy_unassigned_proxy_id: 0 }
      }),
      nodes: null,
      issues: null
    } as unknown as ExecutionNodeAdminStatus)
    const wrapper = mountView()
    await flushPromises()

    expect(showError).not.toHaveBeenCalled()
    expect(wrapper.get('[data-testid="execution-node-label-0"]').text()).toContain('admin.executionNodes.localNode')
    expect(wrapper.get('[data-testid="execution-node-generate-invite"]').attributes('disabled')).toBeDefined()
  })

  it('requires confirmation before enabling and saves the full policy', async () => {
    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('[data-testid="execution-node-balancing-toggle"]').trigger('click')
    expect(wrapper.find('[data-testid="execution-node-confirm"]').exists()).toBe(true)
    expect(updateSettings).not.toHaveBeenCalled()

    await wrapper.get('[data-testid="execution-node-confirm"]').trigger('click')
    await wrapper.get('[data-testid="execution-node-save"]').trigger('click')
    await flushPromises()

    expect(updateSettings).toHaveBeenCalledWith({
      execution_node_balancing_enabled: true,
      execution_node_weights: { api: 3, api2: 1 },
      execution_node_proxy_ids: { api: 84, api2: 83 }
    })
  })

  it('offers local runtime initialization before balancing is enabled', async () => {
    getStatus.mockResolvedValue(statusFixture({
      can_enable: false,
      runtime: { ...statusFixture().runtime, enabled: false, node_id: '', default_proxy_id: 0, legacy_unassigned_proxy_id: 0 },
      nodes: [],
      issues: [{ code: 'MULTI_NODE_DISABLED', severity: 'info', message: 'single node is healthy' }]
    }))
    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('[data-testid="execution-node-initialize"]').trigger('click')
    await flushPromises()

    expect(initializeRuntime).toHaveBeenCalledWith('node1')
    expect(showSuccess).toHaveBeenCalled()
    expect(wrapper.get('[data-testid="execution-node-generate-invite"]').attributes('disabled')).toBeDefined()
  })

  it('blocks enabling when the server preflight has errors', async () => {
    getStatus.mockResolvedValue(statusFixture({
      can_enable: false,
      issues: [{ code: 'LOCAL_RUNTIME_DISABLED', severity: 'error', message: 'runtime disabled' }]
    }))
    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('[data-testid="execution-node-balancing-toggle"]').trigger('click')

    expect(wrapper.find('[data-testid="execution-node-confirm"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="execution-node-balancing-toggle"]').attributes('disabled')).toBeDefined()
    expect(showError).not.toHaveBeenCalled()
  })

  it('generates a one-time invite and pairs through the peer URL', async () => {
    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('[data-testid="execution-node-generate-invite"]').trigger('click')
    await flushPromises()
    expect(generatePairingInvite).toHaveBeenCalledTimes(1)
    expect(wrapper.get('[data-testid="execution-node-invite-token"]').element).toHaveProperty('value', 'a'.repeat(64))

    await wrapper.get('[data-testid="execution-node-peer-url"]').setValue('https://api2.example.com')
    await wrapper.get('[data-testid="execution-node-peer-token"]').setValue('b'.repeat(64))
    await wrapper.get('[data-testid="execution-node-pair"]').trigger('click')
    await flushPromises()

    expect(pairExecutionNode).toHaveBeenCalledWith('https://api2.example.com', 'b'.repeat(64), 'api', window.location.origin)
    expect(wrapper.text()).toContain('admin.executionNodes.pairingReady')
  })

  it('auto-detects local pairing details and changes offline takeover without a restart', async () => {
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.find('[data-testid="execution-node-target-id"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="execution-node-target-url"]').exists()).toBe(false)

    await wrapper.get('[data-testid="execution-node-takeover-toggle"]').trigger('click')
    await flushPromises()

    expect(updateOfflineTakeover).toHaveBeenCalledWith(false)
    expect(showSuccess).toHaveBeenCalled()
  })

  it('hides internal identity and egress while still permitting weight changes', async () => {
    getStatus.mockResolvedValue(statusFixture({ balancing_enabled: true }))
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.find('[data-testid="execution-node-id-0"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="execution-node-proxy-0"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="execution-node-label-0"]').text()).toContain('admin.executionNodes.localNode')
    expect(wrapper.get('[data-testid="execution-node-weight-0"]').attributes('disabled')).toBeUndefined()

    await wrapper.get('[data-testid="execution-node-weight-0"]').setValue('4')
    await wrapper.get('[data-testid="execution-node-save"]').trigger('click')
    await flushPromises()

    expect(updateSettings).toHaveBeenCalledWith(expect.objectContaining({
      execution_node_balancing_enabled: true,
      execution_node_weights: { api: 4, api2: 1 }
    }))
  })
})

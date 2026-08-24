import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

import AccountsView from '../AccountsView.vue'
import type { Account, AdminGroup } from '@/types'

const {
  routerPush,
  listAccounts,
  listWithEtag,
  getBatchTodayStats,
  getUpstreamBillingProbeSettings,
  getAllProxies,
  getAllGroups,
  getSettings,
  showError
} = vi.hoisted(() => ({
  routerPush: vi.fn(),
  listAccounts: vi.fn(),
  listWithEtag: vi.fn(),
  getBatchTodayStats: vi.fn(),
  getUpstreamBillingProbeSettings: vi.fn(),
  getAllProxies: vi.fn(),
  getAllGroups: vi.fn(),
  getSettings: vi.fn(),
  showError: vi.fn()
}))

vi.mock('vue-router', () => ({
  useRoute: () => ({ query: {} }),
  useRouter: () => ({ push: routerPush })
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      list: listAccounts,
      listWithEtag,
      getBatchTodayStats,
      getUpstreamBillingProbeSettings,
      delete: vi.fn(),
      batchClearError: vi.fn(),
      batchRefresh: vi.fn(),
      toggleSchedulable: vi.fn()
    },
    proxies: { getAll: getAllProxies },
    groups: { getAll: getAllGroups },
    settings: { getSettings }
  }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError,
    showSuccess: vi.fn(),
    showInfo: vi.fn()
  })
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ token: 'test-token' })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key })
  }
})

const BaseDialogStub = {
  props: ['show'],
  emits: ['close'],
  template: '<section v-if="show" data-test="base-dialog"><slot /><slot name="footer" /></section>'
}

const mountView = () => mount(AccountsView, {
  global: {
    stubs: {
      AppLayout: { template: '<div><slot /></div>' },
      TablePageLayout: {
        template: '<div><slot name="filters" /><slot name="table" /><slot name="pagination" /></div>'
      },
      DataTable: { props: ['data'], template: '<div data-test="data-table"></div>' },
      BaseDialog: BaseDialogStub,
      Pagination: true,
      ConfirmDialog: true,
      AccountTableActions: { template: '<div><slot name="after" /></div>' },
      AccountTableFilters: { template: '<div></div>' },
      AccountBulkActionsBar: true,
      AccountActionMenu: true,
      ImportDataModal: true,
      ReAuthAccountModal: true,
      AccountTestModal: true,
      AccountStatsModal: true,
      OAuthBillingBreakdownDialog: true,
      ScheduledTestsPanel: true,
      SyncFromCrsModal: true,
      TempUnschedStatusModal: true,
      ErrorPassthroughRulesModal: true,
      TLSFingerprintProfilesModal: true,
      TotpStepUpDialog: true,
      CreateAccountModal: true,
      EditAccountModal: true,
      BulkEditAccountModal: true,
      PlatformTypeBadge: true,
      AccountCapacityCell: true,
      AccountStatusIndicator: true,
      AccountTodayStatsCell: true,
      AccountGroupsCell: true,
      AccountUsageCell: true,
      UpstreamBillingRateCell: true,
      Icon: true
    }
  }
})

const group = (id: number, name: string): AdminGroup => ({
  id,
  name,
  platform: 'openai',
  rate_multiplier: 1,
  rpm_limit: 0,
  is_exclusive: false,
  status: 'active',
  subscription_type: 'standard',
  supported_model_scopes: [],
  sort_order: id,
  created_at: '2026-08-20T00:00:00Z',
  updated_at: '2026-08-20T00:00:00Z'
} as AdminGroup)

const account = (groupIDs: number[]): Account => ({
  id: 101,
  name: 'OAuth Account A',
  platform: 'openai',
  type: 'oauth',
  group_ids: groupIDs,
  proxy_id: null,
  concurrency: 3,
  priority: 1,
  status: 'active',
  error_message: null,
  last_used_at: null,
  expires_at: null,
  auto_pause_on_expired: false,
  schedulable: true,
  rate_limited_at: null,
  rate_limit_reset_at: null,
  overload_until: null,
  temp_unschedulable_until: null,
  temp_unschedulable_reason: null,
  session_window_start: null,
  session_window_end: null,
  session_window_status: null,
  created_at: '2026-08-20T00:00:00Z',
  updated_at: '2026-08-20T00:00:00Z'
})

describe('admin AccountsView user account allowlist entry', () => {
  let wrapper: ReturnType<typeof mountView> | null = null

  beforeEach(() => {
    localStorage.clear()
    vi.clearAllMocks()
    listAccounts.mockResolvedValue({ items: [], total: 0, page: 1, page_size: 20, pages: 0 })
    listWithEtag.mockResolvedValue({ notModified: true, etag: null, data: null })
    getBatchTodayStats.mockResolvedValue({ stats: {} })
    getUpstreamBillingProbeSettings.mockResolvedValue({ enabled: true, interval_minutes: 30 })
    getAllProxies.mockResolvedValue([])
    getSettings.mockResolvedValue({})
  })

  afterEach(() => {
    wrapper?.unmount()
    wrapper = null
    localStorage.clear()
  })

  it('opens the shared group page directly for an account with one group', async () => {
    getAllGroups.mockResolvedValue([group(9, 'Only Group')])
    wrapper = mountView()
    await flushPromises()

    ;(wrapper.vm as unknown as { openAccountUserAllowlist: (value: Account) => void })
      .openAccountUserAllowlist(account([9]))
    await flushPromises()

    expect(routerPush).toHaveBeenCalledWith({
      name: 'AdminGroups',
      query: {
        user_account_allowlist_group: '9',
        user_account_allowlist_account: '101'
      }
    })
  })

  it('asks for the group and then opens the same page for a multi-group account', async () => {
    getAllGroups.mockResolvedValue([group(9, 'Group A'), group(10, 'Group B')])
    wrapper = mountView()
    await flushPromises()

    ;(wrapper.vm as unknown as { openAccountUserAllowlist: (value: Account) => void })
      .openAccountUserAllowlist(account([9, 10]))
    await wrapper.vm.$nextTick()

    expect(wrapper.get('[data-test="base-dialog"]').text()).toContain('Group A')
    expect(wrapper.get('[data-test="base-dialog"]').text()).toContain('Group B')

    const groupB = wrapper.findAll('[data-test="base-dialog"] button')
      .find((button) => button.text().includes('Group B'))
    expect(groupB).toBeDefined()
    await groupB!.trigger('click')
    await flushPromises()

    expect(routerPush).toHaveBeenCalledWith({
      name: 'AdminGroups',
      query: {
        user_account_allowlist_group: '10',
        user_account_allowlist_account: '101'
      }
    })
  })

  it('does not navigate when the account is not attached to a group', async () => {
    getAllGroups.mockResolvedValue([])
    wrapper = mountView()
    await flushPromises()

    ;(wrapper.vm as unknown as { openAccountUserAllowlist: (value: Account) => void })
      .openAccountUserAllowlist(account([]))

    expect(routerPush).not.toHaveBeenCalled()
    expect(showError).toHaveBeenCalledWith('admin.groups.userAccountAllowlist.noAccountGroups')
  })
})

import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

const { receiverMocks, receiverState, authStoreMock } = vi.hoisted(() => ({
  receiverMocks: {
    refresh: vi.fn(),
    refreshQueueStatus: vi.fn(),
    start: vi.fn(),
    stop: vi.fn(),
    appendCardKeys: vi.fn(),
    clearQueuedCardKeys: vi.fn(),
    changeNumber: vi.fn(),
    cancel: vi.fn()
  },
  receiverState: { hasActiveSession: true },
  authStoreMock: {
    isAuthenticated: true,
    isAdmin: true,
    user: { balance: 100 },
    logout: vi.fn()
  }
}))

vi.mock('@/composables/usePixlabSMSReceiver', async () => {
  const { ref } = await import('vue')
  return {
    usePixlabSMSReceiver: () => ({
      phase: ref('waiting'),
      phoneForCopy: ref('+27749433060'),
      countryCallingCode: ref('27'),
      localPhoneNumber: ref('749433060'),
      region: ref('南非'),
      countryFlag: ref('🇿🇦'),
      code: ref('--'),
      queuedKeyCount: ref(11),
      activeSessionCount: ref(1),
      feeAmount: ref(2),
      balance: ref(100),
      hasActiveSession: ref(receiverState.hasActiveSession),
      statusText: ref('实时监听'),
      sessionExpiresAt: ref('2026-08-14T00:15:00.000Z'),
      sessionExpiryText: ref('08:42'),
      canRefresh: ref(true),
      canChangeNumber: ref(true),
      canCancel: ref(true),
      memberMutationRemainingSeconds: ref(0),
      isRefreshing: ref(false),
      isChangingNumber: ref(false),
      isCancelling: ref(false),
      ...receiverMocks
    })
  }
})

vi.mock('@/stores', () => ({
  useAppStore: () => ({ showError: vi.fn(), showInfo: vi.fn(), showSuccess: vi.fn() }),
  useAuthStore: () => authStoreMock
}))

vi.mock('vue-router', () => ({
  useRouter: () => ({ push: vi.fn() }),
  useRoute: () => ({ query: {} })
}))

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({ copyToClipboard: vi.fn().mockResolvedValue(true) })
}))

vi.mock('@/api/admin/smsReceiver', () => ({ updateMemberFee: vi.fn() }))

import SMSReceiverConsoleView from '../SMSReceiverConsoleView.vue'

describe('SMSReceiverConsoleView refresh status', () => {
  beforeEach(() => {
    receiverState.hasActiveSession = true
    Object.values(receiverMocks).forEach((mock) => mock.mockReset())
    receiverMocks.refresh.mockResolvedValue('waiting')
    receiverMocks.refreshQueueStatus.mockResolvedValue({ queued_count: 11, active_count: 1 })
  })

  it('refreshes the active receiver session instead of reading queue status only', async () => {
    const wrapper = mount(SMSReceiverConsoleView, {
      global: {
        stubs: {
          DarkVideoBackground: true,
          SMSRechargeDialog: true,
          Icon: true
        }
      }
    })
    await flushPromises()
    receiverMocks.refresh.mockClear()
    receiverMocks.refreshQueueStatus.mockClear()

    await wrapper.get('[data-testid="refresh-sms-status"]').trigger('click')
    await flushPromises()

    expect(receiverMocks.refresh).toHaveBeenCalledTimes(1)
    expect(receiverMocks.refreshQueueStatus).toHaveBeenCalledTimes(1)
    expect(wrapper.get('[data-testid="sms-session-countdown"]').text()).toContain('08:42')
  })

  it('reads queue status only when there is no current session', async () => {
    receiverState.hasActiveSession = false
    const wrapper = mount(SMSReceiverConsoleView, {
      global: {
        stubs: {
          DarkVideoBackground: true,
          SMSRechargeDialog: true,
          Icon: true
        }
      }
    })
    await flushPromises()
    receiverMocks.refresh.mockClear()
    receiverMocks.refreshQueueStatus.mockClear()

    await wrapper.get('[data-testid="refresh-sms-status"]').trigger('click')
    await flushPromises()

    expect(receiverMocks.refresh).not.toHaveBeenCalled()
    expect(receiverMocks.refreshQueueStatus).toHaveBeenCalledTimes(1)
  })
})

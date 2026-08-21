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
      phase: ref(receiverState.hasActiveSession ? 'waiting' : 'idle'),
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

const SMSReceiverActionDialogStub = {
  props: ['show'],
  emits: ['cancel', 'confirm'],
  template: `
    <div v-if="show" data-testid="sms-confirm-dialog">
      <button type="button" data-testid="confirm-sms-receiver-action" @click="$emit('confirm')">confirm</button>
      <button type="button" data-testid="close-sms-receiver-action" @click="$emit('cancel')">close</button>
    </div>
  `,
}

function mountConsole() {
  return mount(SMSReceiverConsoleView, {
    global: {
      stubs: {
        DarkVideoBackground: true,
        SMSRechargeDialog: true,
        SMSReceiverActionDialog: SMSReceiverActionDialogStub,
        Icon: true,
      },
    },
  })
}

describe('SMSReceiverConsoleView refresh status', () => {
  beforeEach(() => {
    receiverState.hasActiveSession = true
    Object.values(receiverMocks).forEach((mock) => mock.mockReset())
    receiverMocks.refresh.mockResolvedValue('waiting')
    receiverMocks.refreshQueueStatus.mockResolvedValue({ queued_count: 11, active_count: 1 })
  })

  it('refreshes the active receiver session instead of reading queue status only', async () => {
    const wrapper = mountConsole()
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
    const wrapper = mountConsole()
    await flushPromises()
    receiverMocks.refresh.mockClear()
    receiverMocks.refreshQueueStatus.mockClear()

    await wrapper.get('[data-testid="refresh-sms-status"]').trigger('click')
    await flushPromises()

    expect(receiverMocks.refresh).not.toHaveBeenCalled()
    expect(receiverMocks.refreshQueueStatus).toHaveBeenCalledTimes(1)
  })

  it('requires an in-app confirmation before claiming a number', async () => {
    receiverState.hasActiveSession = false
    receiverMocks.start.mockResolvedValue('waiting')
    const wrapper = mountConsole()
    await flushPromises()

    await wrapper.get('[data-testid="claim-sms-number"]').trigger('click')
    await flushPromises()
    expect(receiverMocks.start).not.toHaveBeenCalled()
    expect(wrapper.get('[data-testid="sms-confirm-dialog"]').exists()).toBe(true)

    await wrapper.get('[data-testid="confirm-sms-receiver-action"]').trigger('click')
    await flushPromises()
    expect(receiverMocks.start).toHaveBeenCalledTimes(1)
  })

  it('requires an in-app confirmation before cancelling the current number', async () => {
    receiverMocks.cancel.mockResolvedValue('cancelled')
    const wrapper = mountConsole()
    await flushPromises()

    await wrapper.get('[data-testid="cancel-sms-number"]').trigger('click')
    await flushPromises()
    expect(receiverMocks.cancel).not.toHaveBeenCalled()
    expect(wrapper.get('[data-testid="sms-confirm-dialog"]').exists()).toBe(true)

    await wrapper.get('[data-testid="confirm-sms-receiver-action"]').trigger('click')
    await flushPromises()
    expect(receiverMocks.cancel).toHaveBeenCalledTimes(1)
  })

  it('requires an in-app confirmation before changing the current number', async () => {
    receiverMocks.changeNumber.mockResolvedValue('waiting')
    const wrapper = mountConsole()
    await flushPromises()

    const changeButton = wrapper.findAll('button').find((button) => button.text().includes('换号'))
    expect(changeButton).toBeDefined()

    await changeButton!.trigger('click')
    await flushPromises()

    expect(receiverMocks.changeNumber).not.toHaveBeenCalled()
    expect(wrapper.get('[data-testid="sms-confirm-dialog"]').exists()).toBe(true)

    await wrapper.get('[data-testid="confirm-sms-receiver-action"]').trigger('click')
    await flushPromises()

    expect(receiverMocks.changeNumber).toHaveBeenCalledTimes(1)
  })
})

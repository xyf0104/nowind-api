import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import PixlabSMSReceiver from '../PixlabSMSReceiver.vue'

const SMSReceiverActionDialogStub = {
  props: ['show'],
  emits: ['cancel', 'confirm'],
  template: `
    <div v-if="show" data-testid="sms-confirm-dialog">
      <button type="button" data-testid="confirm-sms-receiver-action" @click="$emit('confirm')">confirm</button>
      <button type="button" data-testid="cancel-sms-receiver-action" @click="$emit('cancel')">cancel</button>
    </div>
  `,
}

const { receiverMocks, receiverState, copyToClipboardMock } = vi.hoisted(() => ({
  receiverMocks: {
    start: vi.fn(),
    stop: vi.fn(),
    refresh: vi.fn(),
    changeNumber: vi.fn(),
    cancel: vi.fn(),
    refreshQueueStatus: vi.fn(),
    appendCardKeys: vi.fn(),
    clearQueuedCardKeys: vi.fn()
  },
  receiverState: {
    canChangeNumber: false,
    hasActiveSession: false,
  },
  copyToClipboardMock: vi.fn().mockResolvedValue(true)
}))

vi.mock('@/composables/usePixlabSMSReceiver', async () => {
  const { ref } = await import('vue')
  return {
    usePixlabSMSReceiver: () => ({
      phase: ref('idle'),
      phoneDisplay: ref('--'),
      phoneForCopy: ref('+27749433060'),
      countryCallingCode: ref('27'),
      localPhoneNumber: ref('749433060'),
      region: ref('南非'),
      countryFlag: ref('🇿🇦'),
      code: ref('--'),
      queuedKeyCount: ref(3),
      statusText: ref('等待取号'),
      statusClass: ref('text-cyan-600'),
      sessionExpiresAt: ref('2026-08-14T00:15:00.000Z'),
      sessionExpiryText: ref('12:34'),
      hasActiveSession: ref(receiverState.hasActiveSession),
      canRefresh: ref(true),
      canChangeNumber: ref(receiverState.canChangeNumber),
      canCancel: ref(true),
      isRefreshing: ref(false),
      isChangingNumber: ref(false),
      isCancelling: ref(false),
      ...receiverMocks
    })
  }
})

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: vi.fn(),
    showInfo: vi.fn(),
    showSuccess: vi.fn()
  })
}))

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({ copyToClipboard: copyToClipboardMock })
}))

describe('PixlabSMSReceiver', () => {
  beforeEach(() => {
    Object.values(receiverMocks).forEach((mock) => mock.mockReset())
    receiverState.canChangeNumber = false
    receiverState.hasActiveSession = false
    copyToClipboardMock.mockClear()
    receiverMocks.start.mockResolvedValue('waiting')
  })

  function mountReceiver(active = true) {
    return mount(PixlabSMSReceiver, {
      props: { active },
      global: {
        stubs: {
          BaseDialog: true,
          Icon: true,
          SMSReceiverActionDialog: SMSReceiverActionDialogStub,
        },
      },
    })
  }

  async function claimNumber(wrapper: ReturnType<typeof mountReceiver>) {
    await wrapper.get('[data-testid="request-sms-phone"]').trigger('click')
    await wrapper.get('[data-testid="confirm-sms-receiver-action"]').trigger('click')
    await flushPromises()
  }

  it('requires an explicit request before claiming a number for every OAuth flow', async () => {
    const wrapper = mountReceiver()

    await flushPromises()
    expect(receiverMocks.start).not.toHaveBeenCalled()
    expect(wrapper.get('[data-testid="request-sms-phone"]').text()).toContain('获取手机号')

    await wrapper.get('[data-testid="request-sms-phone"]').trigger('click')
    await flushPromises()
    expect(receiverMocks.start).not.toHaveBeenCalled()
    expect(wrapper.get('[data-testid="sms-confirm-dialog"]').exists()).toBe(true)

    await wrapper.get('[data-testid="confirm-sms-receiver-action"]').trigger('click')
    await flushPromises()
    expect(receiverMocks.start).toHaveBeenCalledTimes(1)
    expect(wrapper.emitted('phone-ready')).toEqual([['+27749433060']])
    expect(wrapper.find('[data-testid="request-sms-phone"]').exists()).toBe(false)
  })

  it('keeps the request button visible but disabled until an authorization link exists', async () => {
    const wrapper = mountReceiver(false)

    const requestButton = wrapper.get('[data-testid="request-sms-phone"]')
    expect(requestButton.attributes('disabled')).toBeDefined()
    expect(receiverMocks.start).not.toHaveBeenCalled()

    await wrapper.setProps({ active: true })
    expect(wrapper.get('[data-testid="request-sms-phone"]').attributes('disabled')).toBeUndefined()
    expect(receiverMocks.start).not.toHaveBeenCalled()
  })

  it('copies the complete international number while keeping country code and local number separated on screen', async () => {
    const wrapper = mountReceiver()
    await claimNumber(wrapper)

    expect(wrapper.text()).toContain('+27')
    expect(wrapper.text()).toContain('749433060')
	await wrapper.get('[title="点击手机号框复制完整国际号码"]').trigger('click')
    expect(copyToClipboardMock).toHaveBeenCalledWith('+27749433060', '手机号已复制')
  })

  it('renders the countdown supplied by the current server session', async () => {
    const wrapper = mountReceiver()
    await claimNumber(wrapper)

    const countdown = wrapper.get('[data-testid="sms-session-countdown"]')
    expect(countdown.text()).toContain('号码有效期')
    expect(countdown.text()).toContain('12:34')
    expect(countdown.get('time').attributes('datetime')).toBe('2026-08-14T00:15:00.000Z')
  })

  it('keeps a verification code visible when it arrives during cancellation', async () => {
    receiverMocks.cancel.mockResolvedValue('received')
    const wrapper = mountReceiver()
    await claimNumber(wrapper)
    const cancelButton = wrapper.findAll('button').find((button) => button.text().includes('取消'))
    expect(cancelButton).toBeDefined()
    await cancelButton!.trigger('click')
    await flushPromises()

    expect(receiverMocks.cancel).not.toHaveBeenCalled()
    await wrapper.get('[data-testid="confirm-sms-receiver-action"]').trigger('click')
    await flushPromises()

    expect(receiverMocks.cancel).toHaveBeenCalledTimes(1)
    expect(wrapper.emitted('session-cancelled')).toBeUndefined()
    expect(wrapper.find('[data-testid="request-sms-phone"]').exists()).toBe(false)
  })

  it('requires an in-app confirmation before changing the current number', async () => {
    receiverState.canChangeNumber = true
    receiverMocks.changeNumber.mockResolvedValue('waiting')
    const wrapper = mountReceiver()
    await claimNumber(wrapper)

    const changeButton = wrapper.findAll('button').find((button) => button.text().includes('换号'))
    expect(changeButton).toBeDefined()

    await changeButton!.trigger('click')
    await flushPromises()

    expect(receiverMocks.changeNumber).not.toHaveBeenCalled()
    expect(wrapper.get('[data-testid="sms-confirm-dialog"]').exists()).toBe(true)

    await wrapper.get('[data-testid="confirm-sms-receiver-action"]').trigger('click')
    await flushPromises()

    expect(receiverMocks.changeNumber).toHaveBeenCalledTimes(1)
    expect(wrapper.emitted('phone-ready')).toEqual([['+27749433060'], ['+27749433060']])
  })

  it('opens the in-app replacement confirmation when the OAuth page rejects the active number', async () => {
    receiverState.canChangeNumber = true
    receiverState.hasActiveSession = true
    receiverMocks.changeNumber.mockResolvedValue('waiting')
    const wrapper = mount(PixlabSMSReceiver, {
      props: { active: true, replacementRequired: true },
      global: {
        stubs: {
          BaseDialog: true,
          Icon: true,
          SMSReceiverActionDialog: SMSReceiverActionDialogStub,
        },
      },
    })

    await flushPromises()
    expect(wrapper.get('[data-testid="sms-confirm-dialog"]').exists()).toBe(true)
    await wrapper.get('[data-testid="confirm-sms-receiver-action"]').trigger('click')
    await flushPromises()

    expect(receiverMocks.changeNumber).toHaveBeenCalledTimes(1)
    // The mounted confirmed session is replayed once for recovery, then the
    // confirmed replacement is emitted once more to overwrite that number.
    expect(wrapper.emitted('phone-ready')).toEqual([['+27749433060'], ['+27749433060']])
  })

  it('replays an existing confirmed number when manual recovery advances to the submit node', async () => {
    receiverState.hasActiveSession = true
    const wrapper = mount(PixlabSMSReceiver, {
      props: { active: true, submissionKey: 'sms_confirm' },
      global: {
        stubs: {
          BaseDialog: true,
          Icon: true,
          SMSReceiverActionDialog: SMSReceiverActionDialogStub,
        },
      },
    })

    await flushPromises()
    expect(wrapper.emitted('phone-ready')).toEqual([['+27749433060']])

    await wrapper.setProps({ submissionKey: 'phone_submit' })
    await flushPromises()
    expect(wrapper.emitted('phone-ready')).toEqual([['+27749433060'], ['+27749433060']])
  })

  it('emits cancellation only after a confirmed cancellation completes', async () => {
    receiverMocks.cancel.mockResolvedValue('expired')
    const wrapper = mountReceiver()
    await claimNumber(wrapper)

    const cancelButton = wrapper.findAll('button').find((button) => button.text().includes('取消'))
    expect(cancelButton).toBeDefined()
    await cancelButton!.trigger('click')
    await flushPromises()
    expect(receiverMocks.cancel).not.toHaveBeenCalled()
    expect(wrapper.emitted('session-cancelled')).toBeUndefined()

    await wrapper.get('[data-testid="confirm-sms-receiver-action"]').trigger('click')
    await flushPromises()
    expect(receiverMocks.cancel).toHaveBeenCalledTimes(1)
    expect(wrapper.emitted('session-cancelled')).toEqual([[]])
  })

  it('cancels an automation-owned number immediately when the workflow resets', async () => {
    receiverState.hasActiveSession = true
    receiverMocks.cancel.mockResolvedValue('expired')
    const wrapper = mount(PixlabSMSReceiver, {
      props: { active: true, automationMode: true, automationCancelSignal: 0 },
      global: {
        stubs: {
          BaseDialog: true,
          Icon: true,
          SMSReceiverActionDialog: SMSReceiverActionDialogStub
        }
      }
    })

    await wrapper.setProps({ automationCancelSignal: 1 })
    await flushPromises()

    expect(receiverMocks.cancel).toHaveBeenCalledTimes(1)
    expect(wrapper.find('[data-testid="sms-confirm-dialog"]').exists()).toBe(false)
    expect(wrapper.emitted('session-cancelled')).toEqual([[]])
  })

})

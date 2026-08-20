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

const { receiverMocks, copyToClipboardMock } = vi.hoisted(() => ({
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
      canRefresh: ref(true),
      canChangeNumber: ref(false),
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
    await wrapper.get('[title="点击复制完整国际号码"]').trigger('click')
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
    expect(wrapper.find('[data-testid="request-sms-phone"]').exists()).toBe(false)
  })

})

import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import PixlabSMSReceiver from '../PixlabSMSReceiver.vue'

const { receiverMocks } = vi.hoisted(() => ({
  receiverMocks: {
    start: vi.fn(),
    stop: vi.fn(),
    refresh: vi.fn(),
    changeNumber: vi.fn(),
    cancel: vi.fn(),
    refreshQueueStatus: vi.fn(),
    appendCardKeys: vi.fn(),
    clearQueuedCardKeys: vi.fn()
  }
}))

vi.mock('@/composables/usePixlabSMSReceiver', async () => {
  const { ref } = await import('vue')
  return {
    usePixlabSMSReceiver: () => ({
      phase: ref('idle'),
      phoneDisplay: ref('--'),
      phoneForCopy: ref(''),
      region: ref('--'),
      code: ref('--'),
      queuedKeyCount: ref(3),
      statusText: ref('等待取号'),
      statusClass: ref('text-cyan-600'),
      canRefresh: ref(false),
      canChangeNumber: ref(false),
      canCancel: ref(false),
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
  useClipboard: () => ({ copyToClipboard: vi.fn().mockResolvedValue(true) })
}))

describe('PixlabSMSReceiver', () => {
  beforeEach(() => {
    Object.values(receiverMocks).forEach((mock) => mock.mockReset())
    receiverMocks.start.mockResolvedValue('waiting')
  })

  it('requires an explicit request before claiming a number during reauthorization', async () => {
    const wrapper = mount(PixlabSMSReceiver, {
      props: { active: true, manualStart: true },
      global: { stubs: { BaseDialog: true, Icon: true } }
    })

    await flushPromises()
    expect(receiverMocks.start).not.toHaveBeenCalled()
    expect(wrapper.get('[data-testid="request-sms-phone"]').text()).toContain('获取手机号')

    await wrapper.get('[data-testid="request-sms-phone"]').trigger('click')
    await flushPromises()
    expect(receiverMocks.start).toHaveBeenCalledTimes(1)
    expect(wrapper.find('[data-testid="request-sms-phone"]').exists()).toBe(false)
  })

  it('continues to claim a number automatically for a newly added account', async () => {
    mount(PixlabSMSReceiver, {
      props: { active: true, manualStart: false },
      global: { stubs: { BaseDialog: true, Icon: true } }
    })

    await flushPromises()
    expect(receiverMocks.start).toHaveBeenCalledTimes(1)
  })
})

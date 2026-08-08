import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('@/api/admin/smsReceiver', () => ({
  getStatus: vi.fn(),
  addCardKeys: vi.fn(),
  clearCardKeys: vi.fn(),
  redeem: vi.fn(),
  resume: vi.fn(),
  check: vi.fn(),
  change: vi.fn(),
  cancel: vi.fn()
}))

import * as smsReceiverAPI from '@/api/admin/smsReceiver'
import { usePixlabSMSReceiver } from '@/composables/usePixlabSMSReceiver'

const ACTIVE_SESSION_STORAGE_KEY = 'xiass-api:pixlab-sms:active-session-id'

describe('usePixlabSMSReceiver', () => {
  let receiver: ReturnType<typeof usePixlabSMSReceiver>

  beforeEach(() => {
    localStorage.clear()
    vi.clearAllMocks()
    receiver = usePixlabSMSReceiver()
  })

  afterEach(() => receiver.stop())

  it('stores submitted card keys on the server and keeps only an opaque session locally', async () => {
    vi.mocked(smsReceiverAPI.addCardKeys).mockResolvedValue({ added_count: 2, queued_count: 2 })
    vi.mocked(smsReceiverAPI.getStatus).mockResolvedValue({ queued_count: 2, active_count: 0 })
    vi.mocked(smsReceiverAPI.redeem).mockResolvedValue({
      session_id: 'server-session-a',
      status: 'WAITING',
      number: '8613812345678',
      country: '中国',
      queued_count: 1
    })

    await expect(receiver.appendCardKeys('CARD-A\nCARD-B')).resolves.toBe(2)
    await expect(receiver.start()).resolves.toBe('waiting')

    expect(smsReceiverAPI.addCardKeys).toHaveBeenCalledWith('CARD-A\nCARD-B')
    expect(smsReceiverAPI.redeem).toHaveBeenCalledTimes(1)
    expect(receiver.phoneDisplay.value).toBe('+86 13812345678')
    expect(receiver.phoneForCopy.value).toBe('13812345678')
    expect(receiver.countryCallingCode.value).toBe('86')
    expect(receiver.localPhoneNumber.value).toBe('13812345678')
    expect(receiver.queuedKeyCount.value).toBe(1)
    expect(localStorage.getItem(ACTIVE_SESSION_STORAGE_KEY)).toBe('server-session-a')
    expect(JSON.stringify(localStorage)).not.toContain('CARD-A')
    expect(JSON.stringify(localStorage)).not.toContain('CARD-B')
  })

  it('removes the opaque local session after the server reports a verification code', async () => {
    vi.mocked(smsReceiverAPI.getStatus).mockResolvedValue({ queued_count: 1, active_count: 0 })
    vi.mocked(smsReceiverAPI.redeem).mockResolvedValue({
      session_id: 'server-session-b',
      status: 'RECEIVED',
      number: '12025550123',
      country: '美国',
      code: '654321',
      queued_count: 0
    })

    await expect(receiver.start()).resolves.toBe('received')

    expect(receiver.code.value).toBe('654321')
    expect(receiver.phase.value).toBe('received')
    expect(localStorage.getItem(ACTIVE_SESSION_STORAGE_KEY)).toBeNull()
    expect(receiver.queuedKeyCount.value).toBe(0)
  })

  it('expands country codes to a complete region name and keeps only the local number copyable', async () => {
    vi.mocked(smsReceiverAPI.getStatus).mockResolvedValue({ queued_count: 1, active_count: 0 })
    vi.mocked(smsReceiverAPI.redeem).mockResolvedValue({
      session_id: 'server-session-region',
      status: 'WAITING',
      number: '12025550123',
      country: 'US',
      queued_count: 0
    })

    await receiver.start()

    expect(receiver.countryCallingCode.value).toBe('1')
    expect(receiver.localPhoneNumber.value).toBe('2025550123')
    expect(receiver.phoneForCopy.value).toBe('2025550123')
    expect(receiver.region.value).toBe('美国')
    expect(receiver.countryFlag.value).toBe('🇺🇸')
  })

  it('separates Colombian country code and renders its full region with flag', async () => {
    vi.mocked(smsReceiverAPI.getStatus).mockResolvedValue({ queued_count: 1, active_count: 0 })
    vi.mocked(smsReceiverAPI.redeem).mockResolvedValue({
      session_id: 'server-session-colombia',
      status: 'WAITING',
      number: '573242390811',
      country: '🌎 哥伦',
      queued_count: 0
    })

    await receiver.start()

    expect(receiver.countryCallingCode.value).toBe('57')
    expect(receiver.localPhoneNumber.value).toBe('3242390811')
    expect(receiver.phoneForCopy.value).toBe('3242390811')
    expect(receiver.region.value).toBe('哥伦比亚')
    expect(receiver.countryFlag.value).toBe('🇨🇴')
  })

  it('uses one server-side change request and replaces the opaque session ID', async () => {
    vi.mocked(smsReceiverAPI.getStatus).mockResolvedValue({ queued_count: 2, active_count: 0 })
    vi.mocked(smsReceiverAPI.redeem).mockResolvedValue({
      session_id: 'server-session-old',
      status: 'WAITING',
      number: '8613812345678',
      queued_count: 1
    })
    vi.mocked(smsReceiverAPI.change).mockResolvedValue({
      session_id: 'server-session-new',
      status: 'WAITING',
      number: '8613912345678',
      queued_count: 0
    })

    await receiver.start()
    await expect(receiver.changeNumber()).resolves.toBe('waiting')

    expect(smsReceiverAPI.change).toHaveBeenCalledWith('server-session-old')
    expect(smsReceiverAPI.cancel).not.toHaveBeenCalled()
    expect(localStorage.getItem(ACTIVE_SESSION_STORAGE_KEY)).toBe('server-session-new')
    expect(receiver.phoneForCopy.value).toBe('13912345678')
    expect(receiver.queuedKeyCount.value).toBe(0)
  })

  it('clears a locally stored session when the server retires a limit-reached card', async () => {
    vi.mocked(smsReceiverAPI.getStatus).mockResolvedValue({ queued_count: 2, active_count: 0 })
    vi.mocked(smsReceiverAPI.redeem).mockResolvedValue({
      session_id: 'server-session-limit',
      status: 'WAITING',
      number: '8613812345678',
      queued_count: 1
    })
    vi.mocked(smsReceiverAPI.check).mockResolvedValue({
      status: 'EXHAUSTED',
      queued_count: 1
    })

    await receiver.start()
    await expect(receiver.refresh()).resolves.toBe('expired')

    expect(receiver.phase.value).toBe('expired')
    expect(localStorage.getItem(ACTIVE_SESSION_STORAGE_KEY)).toBeNull()
    expect(receiver.queuedKeyCount.value).toBe(1)
  })
})

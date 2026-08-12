import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'

const complianceApiMocks = vi.hoisted(() => ({
  getStatus: vi.fn(),
}))

vi.mock('@/api/admin/compliance', () => ({
  default: {
    getStatus: complianceApiMocks.getStatus,
    accept: vi.fn(),
  },
  adminComplianceAPI: {
    getStatus: complianceApiMocks.getStatus,
    accept: vi.fn(),
  },
}))

import { useAdminComplianceStore } from '@/stores/adminCompliance'

describe('admin compliance status', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    complianceApiMocks.getStatus.mockReset()
  })

  it('coalesces concurrent status checks into one request', async () => {
    let resolveStatus: ((value: {
      required: boolean
      version: string
      document_path_zh: string
      document_path_en: string
      document_url_zh: string
      document_url_en: string
      ack_phrase_zh: string
      ack_phrase_en: string
    }) => void) | undefined
    complianceApiMocks.getStatus.mockImplementation(() => new Promise((resolve) => {
      resolveStatus = resolve
    }))

    const store = useAdminComplianceStore()
    const first = store.fetchStatus()
    const second = store.fetchStatus()

    expect(complianceApiMocks.getStatus).toHaveBeenCalledTimes(1)
    expect(store.loading).toBe(true)

    resolveStatus?.({
      required: false,
      version: '1',
      document_path_zh: 'zh.md',
      document_path_en: 'en.md',
      document_url_zh: 'https://example.com/zh.md',
      document_url_en: 'https://example.com/en.md',
      ack_phrase_zh: '确认',
      ack_phrase_en: 'confirm',
    })

    await expect(first).resolves.toEqual(await second)
    expect(store.loading).toBe(false)
    expect(store.initialized).toBe(true)
  })
})

import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import DirectAccountImportDialog from '../DirectAccountImportDialog.vue'
import type { AdminDataPayload } from '@/types'

const { importDataMock, showErrorMock, showSuccessMock } = vi.hoisted(() => ({
  importDataMock: vi.fn(),
  showErrorMock: vi.fn(),
  showSuccessMock: vi.fn(),
}))

vi.mock('@/api/admin', () => ({
  adminAPI: { accounts: { importData: importDataMock } },
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError: showErrorMock, showSuccess: showSuccessMock }),
}))

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key }),
}))

const BaseDialogStub = {
  props: ['show'],
  emits: ['close'],
  template: '<div v-if="show"><slot /><slot name="footer" /></div>',
}

const payload: AdminDataPayload = {
  type: 'sub2api-data',
  version: 1,
  exported_at: '2026-08-20T00:00:00Z',
  proxies: [],
  accounts: [{
    name: 'Converted account',
    platform: 'openai',
    type: 'oauth',
    credentials: { access_token: 'secret' },
    concurrency: 3,
    priority: 50,
  }],
}

describe('DirectAccountImportDialog', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    importDataMock.mockResolvedValue({
      proxy_created: 0,
      proxy_reused: 0,
      proxy_failed: 0,
      account_created: 1,
      account_failed: 0,
      errors: [],
    })
  })

  it('does not upload until the administrator explicitly confirms', async () => {
    const wrapper = mount(DirectAccountImportDialog, {
      props: { show: true, payload, accountCount: 1 },
      global: { stubs: { BaseDialog: BaseDialogStub, Icon: true } },
    })

    expect(importDataMock).not.toHaveBeenCalled()
    await wrapper.get('[data-test="confirm-direct-import"]').trigger('click')
    await flushPromises()

    expect(importDataMock).toHaveBeenCalledTimes(1)
    expect(importDataMock).toHaveBeenCalledWith({
      data: payload,
      skip_default_group_bind: true,
    })
    expect(wrapper.emitted('imported')).toHaveLength(1)
    expect(wrapper.get('[data-test="direct-import-result"]').exists()).toBe(true)
  })

  it('disables confirmation for an empty payload', () => {
    const wrapper = mount(DirectAccountImportDialog, {
      props: { show: true, payload: null, accountCount: 0 },
      global: { stubs: { BaseDialog: BaseDialogStub, Icon: true } },
    })

    expect(wrapper.get('[data-test="confirm-direct-import"]').attributes('disabled')).toBeDefined()
    expect(importDataMock).not.toHaveBeenCalled()
  })
})

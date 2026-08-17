import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import TempUnschedStatusModal from '../TempUnschedStatusModal.vue'
import type { Account } from '@/types'

const mocks = vi.hoisted(() => ({
  getStatus: vi.fn(),
  recoverState: vi.fn(),
  showSuccess: vi.fn(),
  showError: vi.fn()
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key })
  }
})

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showSuccess: mocks.showSuccess,
    showError: mocks.showError
  })
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      getTempUnschedulableStatus: mocks.getStatus,
      recoverState: mocks.recoverState
    }
  }
}))

const account = {
  id: 271,
  name: 'nowind8',
  platform: 'openai',
  type: 'oauth',
  status: 'active',
  schedulable: true
} as Account

describe('TempUnschedStatusModal', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mocks.getStatus.mockResolvedValue({ active: false })
    mocks.recoverState.mockResolvedValue(account)
  })

  it('allows idempotent recovery when the active cooldown has already expired', async () => {
    const wrapper = mount(TempUnschedStatusModal, {
      props: { show: true, account },
      global: {
        stubs: {
          BaseDialog: {
            template: '<div><slot /><slot name="footer" /></div>'
          }
        }
      }
    })

    await flushPromises()

    const recoverButton = wrapper.findAll('button').find((button) =>
      button.text().includes('admin.accounts.recoverState')
    )
    expect(recoverButton).toBeDefined()
    expect(recoverButton!.attributes('disabled')).toBeUndefined()

    await recoverButton!.trigger('click')
    await flushPromises()

    expect(mocks.recoverState).toHaveBeenCalledWith(271)
    expect(wrapper.emitted('reset')?.[0]).toEqual([account])
    expect(wrapper.emitted('close')).toHaveLength(1)
  })
})

import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import UserAllowedGroupsModal from '../UserAllowedGroupsModal.vue'
import type { AdminUser } from '@/types'

const { listGroups, updateUser, showSuccess } = vi.hoisted(() => ({
  listGroups: vi.fn(),
  updateUser: vi.fn(),
  showSuccess: vi.fn(),
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    groups: { list: listGroups },
    users: { update: updateUser },
  },
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showSuccess }),
}))

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key }),
}))

const BaseDialogStub = {
  props: ['show', 'title'],
  template: '<div v-if="show"><slot /><slot name="footer" /></div>',
}

const user: AdminUser = {
  id: 9,
  username: 'member',
  email: 'member@example.com',
  role: 'user',
  balance: 10,
  concurrency: 3,
  status: 'active',
  allowed_groups: [30],
  restrict_public_groups: false,
  balance_notify_enabled: false,
  balance_notify_threshold: null,
  balance_notify_extra_emails: [],
  created_at: '2026-08-17T00:00:00Z',
  updated_at: '2026-08-17T00:00:00Z',
  notes: '',
}

function mountModal() {
  return mount(UserAllowedGroupsModal, {
    props: { show: false, user },
    global: {
      stubs: {
        BaseDialog: BaseDialogStub,
        PlatformIcon: { template: '<span />' },
      },
    },
  })
}

describe('UserAllowedGroupsModal', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    listGroups.mockResolvedValue({
      items: [
        { id: 10, name: 'OpenAI 0.1', platform: 'openai', is_exclusive: false, status: 'active', subscription_type: 'standard', rate_multiplier: 0.1 },
        { id: 20, name: 'Claude 0.2', platform: 'anthropic', is_exclusive: false, status: 'active', subscription_type: 'standard', rate_multiplier: 0.2 },
        { id: 30, name: 'Private', platform: 'openai', is_exclusive: true, status: 'active', subscription_type: 'standard', rate_multiplier: 0.3 },
      ],
    })
    updateUser.mockResolvedValue({})
  })

  it('lets an administrator hide a public group and saves a real allowlist', async () => {
    const wrapper = mountModal()
    await wrapper.setProps({ show: true })
    await flushPromises()

    expect((wrapper.get('[data-testid="group-toggle-10"]').element as HTMLInputElement).checked).toBe(true)
    expect((wrapper.get('[data-testid="group-toggle-20"]').element as HTMLInputElement).checked).toBe(true)

    await wrapper.get('[data-testid="group-toggle-20"]').setValue(false)
    const save = wrapper.findAll('button').find((button) => button.text() === 'common.save')
    expect(save).toBeDefined()
    await save!.trigger('click')
    await flushPromises()

    expect(updateUser).toHaveBeenCalledWith(9, expect.objectContaining({
      restrict_public_groups: true,
      allowed_groups: [30, 10],
    }))
  })
})

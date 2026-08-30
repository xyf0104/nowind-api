import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

import AnnouncementEmailDispatchDialog from '../AnnouncementEmailDispatchDialog.vue'

const {
  listUsers,
  dispatchEmailNotifications,
  getEmailDeliverySummary,
  showError,
  showSuccess,
} = vi.hoisted(() => ({
  listUsers: vi.fn(),
  dispatchEmailNotifications: vi.fn(),
  getEmailDeliverySummary: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn(),
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    users: {
      list: listUsers,
    },
    announcements: {
      dispatchEmailNotifications,
      getEmailDeliverySummary,
    },
  },
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError, showSuccess }),
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key }),
  }
})

const BaseDialogStub = {
  props: ['show', 'title', 'width', 'zIndex'],
  emits: ['close'],
  template: '<section v-if="show" :data-title="title"><slot /><slot name="footer" /></section>',
}

const announcement = {
  id: 41,
  title: 'GPT Pro 20x limited offer',
  content: 'Announcement content',
  status: 'active' as const,
  notify_mode: 'email' as const,
  targeting: { any_of: [] },
  created_at: '2026-08-30T00:00:00Z',
  updated_at: '2026-08-30T00:00:00Z',
}

const mountDialog = () => mount(AnnouncementEmailDispatchDialog, {
  props: {
    show: true,
    announcement,
  },
  global: {
    stubs: {
      BaseDialog: BaseDialogStub,
      Icon: true,
      Pagination: true,
    },
  },
})

describe('AnnouncementEmailDispatchDialog', () => {
  beforeEach(() => {
    listUsers.mockReset()
    dispatchEmailNotifications.mockReset()
    getEmailDeliverySummary.mockReset()
    showError.mockReset()
    showSuccess.mockReset()

    listUsers.mockResolvedValue({
      items: [
        { id: 11, email: 'alice@example.com', username: 'alice' },
        { id: 12, email: 'bob@example.com', username: 'bob' },
      ],
      total: 2,
      page: 1,
      page_size: 20,
      pages: 1,
    })
    getEmailDeliverySummary.mockResolvedValue({ total: 0, claimed: 0, sent: 0, failed: 0 })
    dispatchEmailNotifications.mockResolvedValue({
      targeted: 1,
      claimed: 1,
      sent: 1,
      failed: 0,
      already_sent: 0,
      skipped: 0,
    })
  })

  it('requires a second confirmation before sending to manually selected users', async () => {
    const wrapper = mountDialog()
    await flushPromises()

    expect(listUsers).toHaveBeenCalledTimes(1)
    expect(getEmailDeliverySummary).toHaveBeenCalledWith(41)

    await wrapper.get('[data-testid="announcement-email-scope-selected"]').trigger('click')
    await wrapper.get('[data-testid="announcement-email-select-user-11"]').setValue(true)

    await wrapper.get('[data-testid="announcement-email-confirm"]').trigger('click')
    expect(dispatchEmailNotifications).not.toHaveBeenCalled()
    expect(wrapper.find('[data-testid="announcement-email-send-now"]').exists()).toBe(true)

    await wrapper.get('[data-testid="announcement-email-send-now"]').trigger('click')
    await flushPromises()

    expect(dispatchEmailNotifications).toHaveBeenCalledTimes(1)
    expect(dispatchEmailNotifications).toHaveBeenCalledWith(41, {
      scope: 'selected',
      user_ids: [11],
    })
    expect(getEmailDeliverySummary).toHaveBeenCalledTimes(2)
    expect(showSuccess).toHaveBeenCalledWith('admin.announcements.emailDispatch.sentResult')
  })

  it('keeps selections while the administrator changes pages', async () => {
    listUsers.mockImplementation(async (page: number) => ({
      items: page === 1
        ? [{ id: 11, email: 'alice@example.com', username: 'alice' }]
        : [{ id: 12, email: 'bob@example.com', username: 'bob' }],
      total: 2,
      page,
      page_size: 1,
      pages: 2,
    }))

    const wrapper = mountDialog()
    await flushPromises()

    await wrapper.get('[data-testid="announcement-email-scope-selected"]').trigger('click')
    await wrapper.get('[data-testid="announcement-email-select-user-11"]').setValue(true)

    const setupState = (wrapper.vm as any).$?.setupState
    setupState.handlePageChange(2)
    await flushPromises()

    await wrapper.get('[data-testid="announcement-email-select-user-12"]').setValue(true)
    await wrapper.get('[data-testid="announcement-email-confirm"]').trigger('click')
    await wrapper.get('[data-testid="announcement-email-send-now"]').trigger('click')
    await flushPromises()

    expect(dispatchEmailNotifications).toHaveBeenCalledWith(41, {
      scope: 'selected',
      user_ids: [11, 12],
    })
  })
})

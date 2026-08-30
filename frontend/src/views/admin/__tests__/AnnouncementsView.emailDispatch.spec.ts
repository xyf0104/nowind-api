import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

import AnnouncementsView from '../AnnouncementsView.vue'

const { listAnnouncements, getAllGroups } = vi.hoisted(() => ({
  listAnnouncements: vi.fn(),
  getAllGroups: vi.fn(),
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    announcements: {
      list: listAnnouncements,
      create: vi.fn(),
      update: vi.fn(),
      delete: vi.fn(),
      getReadStatus: vi.fn(),
      dispatchEmailNotifications: vi.fn(),
      getEmailDeliverySummary: vi.fn(),
    },
    groups: { getAll: getAllGroups },
  },
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError: vi.fn(), showSuccess: vi.fn() }),
}))

vi.mock('@/composables/usePersistedPageSize', () => ({
  getPersistedPageSize: () => 20,
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key }),
  }
})

const DataTableStub = {
  props: ['data', 'loading'],
  template: '<div><template v-for="row in data" :key="row.id"><slot name="cell-actions" :row="row" /></template></div>',
}

const mountView = () => mount(AnnouncementsView, {
  global: {
    stubs: {
      AppLayout: { template: '<div><slot /></div>' },
      TablePageLayout: { template: '<div><slot name="filters" /><slot name="table" /><slot name="pagination" /></div>' },
      DataTable: DataTableStub,
      Pagination: true,
      BaseDialog: true,
      ConfirmDialog: true,
      Select: true,
      EmptyState: true,
      Icon: true,
      AnnouncementTargetingEditor: true,
      AnnouncementReadStatusDialog: true,
      AnnouncementEmailDispatchDialog: true,
      AnnouncementPopup: true,
    },
  },
})

describe('AnnouncementsView email notification action', () => {
  beforeEach(() => {
    listAnnouncements.mockReset()
    getAllGroups.mockReset()
    listAnnouncements.mockResolvedValue({
      items: [
        {
          id: 1,
          title: 'Email campaign',
          content: 'Send through email',
          status: 'active',
          notify_mode: 'email',
          targeting: { any_of: [] },
          created_at: '2026-08-30T00:00:00Z',
          updated_at: '2026-08-30T00:00:00Z',
        },
        {
          id: 2,
          title: 'Popup campaign',
          content: 'Show in app',
          status: 'active',
          notify_mode: 'popup',
          targeting: { any_of: [] },
          created_at: '2026-08-30T00:00:00Z',
          updated_at: '2026-08-30T00:00:00Z',
        },
      ],
      total: 2,
      page: 1,
      page_size: 20,
      pages: 1,
    })
    getAllGroups.mockResolvedValue([])
  })

  it('shows the mail action only for saved email announcements', async () => {
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.findAll('[data-testid="announcement-email-dispatch"]')).toHaveLength(1)
    expect(listAnnouncements).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })
})

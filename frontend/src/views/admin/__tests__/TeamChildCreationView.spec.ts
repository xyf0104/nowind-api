import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

const { teamChildAPI, groupsAPI, appStore } = vi.hoisted(() => ({
  teamChildAPI: {
    getMailboxStatus: vi.fn(),
    createMailbox: vi.fn(),
    generateOpenAIAuthUrl: vi.fn(),
    pollMailboxCode: vi.fn(),
    deleteMailboxSession: vi.fn(),
    importMailboxConfig: vi.fn(),
    createBrowserSession: vi.fn(),
    refreshTeamChildMembers: vi.fn(),
    inspectTeamChildSeat: vi.fn(),
    inviteTeamChildMember: vi.fn(),
    updateTeamChildMember: vi.fn(),
    removeTeamChildMember: vi.fn(),
    createOpenAIAccountFromOAuth: vi.fn()
  },
  groupsAPI: { getAll: vi.fn() },
  appStore: {
    showError: vi.fn(),
    showInfo: vi.fn(),
    showSuccess: vi.fn()
  }
}))

vi.mock('@/api/admin/teamChild', () => ({ teamChildAPI }))
vi.mock('@/api/admin/groups', () => ({ groupsAPI }))
vi.mock('@/stores/app', () => ({ useAppStore: () => appStore }))

import TeamChildCreationView from '../TeamChildCreationView.vue'

const PixlabSMSReceiverStub = {
  props: { active: { type: Boolean, default: false } },
  template: '<div data-testid="team-sms-receiver" :data-active="String(active)" />'
}

const BrowserWorkspaceStub = {
  template: '<button type="button" data-testid="team-browser-workspace" @click="$emit(\'open-modular\')" />'
}

const MembersWorkspaceStub = {
  template: '<button type="button" data-testid="team-members-workspace" @click="$emit(\'open-browser\')" />'
}

const GroupSelectorStub = {
  template: '<div data-testid="group-selector" />'
}

describe('TeamChildCreationView', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    teamChildAPI.getMailboxStatus.mockResolvedValue({ configured: true, browser_configured: false })
    groupsAPI.getAll.mockResolvedValue([])
    teamChildAPI.createMailbox.mockResolvedValue({
      session_id: 'mailbox-session',
      email: 'team-child@example.test',
      expires_at: '2026-08-22T12:30:00.000Z'
    })
    teamChildAPI.generateOpenAIAuthUrl.mockResolvedValue({
      auth_url: 'https://auth.example.test/authorize?state=team-state',
      session_id: 'oauth-session'
    })
    teamChildAPI.pollMailboxCode.mockResolvedValue({ status: 'waiting' })
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('activates the shared OAuth SMS receiver only after an authorization URL exists', async () => {
    const wrapper = mount(TeamChildCreationView, {
      global: {
        stubs: {
          Icon: true,
          PixlabSMSReceiver: PixlabSMSReceiverStub,
          TeamChildBrowserWorkspace: BrowserWorkspaceStub,
          TeamChildMembersWorkspace: MembersWorkspaceStub,
          GroupSelector: GroupSelectorStub,
          RouterLink: true
        }
      }
    })

    await flushPromises()
    expect(wrapper.get('[data-testid="team-sms-receiver"]').attributes('data-active')).toBe('false')

    const startButton = wrapper.findAll('button').find((button) => button.text().includes('开始创建'))
    expect(startButton).toBeDefined()
    await startButton!.trigger('click')
    await flushPromises()

    expect(teamChildAPI.createMailbox).toHaveBeenCalledTimes(1)
    expect(teamChildAPI.generateOpenAIAuthUrl).toHaveBeenCalledTimes(1)
    expect(wrapper.get('[data-testid="team-sms-receiver"]').attributes('data-active')).toBe('true')

    wrapper.unmount()
  })

  it('loads the member automation workspace without minting a graphical browser session', async () => {
    teamChildAPI.getMailboxStatus.mockResolvedValue({ configured: true, browser_configured: true })
    teamChildAPI.refreshTeamChildMembers.mockResolvedValue({
      ready: true,
      members: [{ id: 'member@example.test', email: 'member@example.test', role: 'member' }],
      seat_email: 'member@example.test'
    })
    teamChildAPI.createBrowserSession.mockResolvedValue({ embed_url: '/browser?ticket=test', expires_at: '2026-08-22T12:30:00.000Z' })

    const wrapper = mount(TeamChildCreationView, {
      global: {
        stubs: {
          Icon: true,
          PixlabSMSReceiver: PixlabSMSReceiverStub,
          TeamChildBrowserWorkspace: BrowserWorkspaceStub,
          TeamChildMembersWorkspace: MembersWorkspaceStub,
          GroupSelector: GroupSelectorStub,
          RouterLink: true
        }
      }
    })

    await flushPromises()
    expect(teamChildAPI.refreshTeamChildMembers).toHaveBeenCalledTimes(1)
    expect(teamChildAPI.createBrowserSession).not.toHaveBeenCalled()
    expect(wrapper.find('[data-testid="team-members-workspace"]').exists()).toBe(true)

    await wrapper.get('[data-testid="team-members-workspace"]').trigger('click')
    await flushPromises()
    expect(teamChildAPI.createBrowserSession).toHaveBeenCalledTimes(1)
    expect(wrapper.find('[data-testid="team-browser-workspace"]').exists()).toBe(true)

    wrapper.unmount()
  })
})

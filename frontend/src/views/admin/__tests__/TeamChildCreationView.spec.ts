import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

const { teamChildAPI, groupsAPI, appStore } = vi.hoisted(() => ({
  teamChildAPI: {
    getMailboxStatus: vi.fn(),
    createMailbox: vi.fn(),
    getActiveMailbox: vi.fn(),
    generateOpenAIAuthUrl: vi.fn(),
    pollMailboxCode: vi.fn(),
    deleteMailboxSession: vi.fn(),
    importMailboxConfig: vi.fn(),
    createBrowserSession: vi.fn(),
    heartbeatTeamChildBrowserControl: vi.fn(),
    releaseTeamChildBrowserControl: vi.fn(),
    refreshTeamChildMembers: vi.fn(),
    inspectTeamChildSeat: vi.fn(),
    inviteTeamChildMember: vi.fn(),
    updateTeamChildMember: vi.fn(),
    removeTeamChildMember: vi.fn(),
    startTeamChildWorkflow: vi.fn(),
    getTeamChildWorkflow: vi.fn(),
    continueTeamChildWorkflow: vi.fn(),
    submitTeamChildWorkflowPhone: vi.fn(),
    submitTeamChildWorkflowCode: vi.fn(),
    restartTeamChildWorkflowOAuth: vi.fn(),
    cancelTeamChildWorkflow: vi.fn(),
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
  template: `<div data-testid="team-sms-receiver" :data-active="String(active)">
    <button type="button" data-testid="sms-phone-ready" @click="$emit('phone-ready', '+14155550123')" />
    <button type="button" data-testid="sms-code-received" @click="$emit('code-received', '123456')" />
    <button type="button" data-testid="sms-session-cancelled" @click="$emit('session-cancelled')" />
  </div>`
}

const BrowserWorkspaceStub = {
  template: '<button type="button" data-testid="team-browser-workspace" @click="$emit(\'open-modular\')" />'
}

const MembersWorkspaceStub = {
  template: `<div>
    <button type="button" data-testid="team-member-select" @click="$emit('select', 'member@example.test')" />
    <button type="button" data-testid="team-start-workflow" @click="$emit('start-workflow')" />
    <button type="button" data-testid="team-members-workspace" @click="$emit('open-browser')" />
  </div>`
}

const ConfirmDialogStub = {
  props: { show: { type: Boolean, default: false } },
  template: '<button v-if="show" type="button" data-testid="confirm-dialog" @click="$emit(\'confirm\')" />'
}

const GroupSelectorStub = {
  template: '<div data-testid="group-selector" />'
}

function mountView() {
  return mount(TeamChildCreationView, {
    global: {
      stubs: {
        Icon: true,
        PixlabSMSReceiver: PixlabSMSReceiverStub,
        TeamChildBrowserWorkspace: BrowserWorkspaceStub,
        TeamChildMembersWorkspace: MembersWorkspaceStub,
        GroupSelector: GroupSelectorStub,
        ConfirmDialog: ConfirmDialogStub,
        RouterLink: true
      }
    }
  })
}

describe('TeamChildCreationView', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    teamChildAPI.getMailboxStatus.mockResolvedValue({ configured: true, browser_configured: true })
    groupsAPI.getAll.mockResolvedValue([])
    teamChildAPI.refreshTeamChildMembers.mockResolvedValue({
      ready: true,
      members: [{ id: 'member@example.test', email: 'member@example.test', role: 'member' }],
      seat_email: 'member@example.test'
    })
    teamChildAPI.createMailbox.mockResolvedValue({
      session_id: 'mailbox-session',
      email: 'team-child@example.test',
      expires_at: '2026-08-22T12:30:00.000Z'
    })
    teamChildAPI.getActiveMailbox.mockResolvedValue(null)
    teamChildAPI.generateOpenAIAuthUrl.mockResolvedValue({
      auth_url: 'https://auth.openai.com/authorize?state=team-state',
      session_id: 'oauth-session'
    })
    teamChildAPI.pollMailboxCode.mockResolvedValue({ status: 'waiting' })
    teamChildAPI.heartbeatTeamChildBrowserControl.mockResolvedValue({
      controller_id: 'controller-token-abcdefghijklmnop',
      control_expires_at: '2026-08-22T12:02:00.000Z'
    })
    teamChildAPI.releaseTeamChildBrowserControl.mockResolvedValue({
      controller_id: 'controller-token-abcdefghijklmnop',
      released: true
    })
    teamChildAPI.createBrowserSession.mockResolvedValue({
      embed_url: '/browser?ticket=test',
      expires_at: '2026-08-22T12:30:00.000Z',
      controller_id: 'controller-token-abcdefghijklmnop',
      control_expires_at: '2026-08-22T12:02:00.000Z'
    })
    teamChildAPI.startTeamChildWorkflow.mockResolvedValue({
      id: 'workflow-token-abcdefghijklmnop',
      status: 'manual_required',
      manual_required: true,
      expires_at: '2026-08-22T12:30:00.000Z',
      steps: [
        { key: 'members', number: 1, label: '读取成员席位', status: 'completed' },
        { key: 'remove', number: 2, label: '移除已选成员', status: 'completed' },
        { key: 'invite', number: 3, label: '邀请临时邮箱', status: 'completed' },
        { key: 'oauth', number: 4, label: '打开 OpenAI 授权页', status: 'completed' },
        { key: 'verify', number: 5, label: '完成外部验证并捕获回调', status: 'waiting' }
      ]
    })
    teamChildAPI.getTeamChildWorkflow.mockResolvedValue({
      id: 'workflow-token-abcdefghijklmnop',
      status: 'manual_required',
      manual_required: true,
      expires_at: '2026-08-22T12:30:00.000Z',
      steps: []
    })
    teamChildAPI.submitTeamChildWorkflowPhone.mockResolvedValue({
      id: 'workflow-token-abcdefghijklmnop',
      status: 'manual_required',
      manual_required: true,
      expires_at: '2026-08-22T12:30:00.000Z',
      steps: []
    })
    teamChildAPI.submitTeamChildWorkflowCode.mockResolvedValue({
      id: 'workflow-token-abcdefghijklmnop',
      status: 'manual_required',
      manual_required: true,
      expires_at: '2026-08-22T12:30:00.000Z',
      steps: []
    })
    teamChildAPI.restartTeamChildWorkflowOAuth.mockResolvedValue({
      id: 'workflow-token-abcdefghijklmnop',
      status: 'manual_required',
      manual_required: true,
      expires_at: '2026-08-22T12:30:00.000Z',
      steps: []
    })
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('defaults to the member automation workspace without minting a graphical browser session', async () => {
    const wrapper = mountView()

    await flushPromises()
    expect(teamChildAPI.refreshTeamChildMembers).toHaveBeenCalledTimes(1)
    expect(teamChildAPI.createBrowserSession).not.toHaveBeenCalled()
    expect(wrapper.find('[data-testid="team-members-workspace"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="team-sms-receiver"]').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('需要你在 OpenAI 页面完成验证')

    await wrapper.get('[data-testid="team-members-workspace"]').trigger('click')
    await flushPromises()
    expect(teamChildAPI.createBrowserSession).toHaveBeenCalledWith({})
    expect(wrapper.find('[data-testid="team-browser-workspace"]').exists()).toBe(true)

    wrapper.unmount()
    expect(teamChildAPI.releaseTeamChildBrowserControl).toHaveBeenCalledWith('controller-token-abcdefghijklmnop')
  })

  it('keeps the mailbox configuration action on one line', async () => {
    const wrapper = mountView()
    await flushPromises()

    const configButton = wrapper.findAll('button').find((button) => button.text().includes('导入邮箱配置'))
    expect(configButton).toBeDefined()
    expect(configButton!.classes()).toContain('whitespace-nowrap')

    wrapper.unmount()
  })

  it('requires the in-page confirmation before starting replace-seat authorization', async () => {
    const wrapper = mountView()
    await flushPromises()

    const mailboxButton = wrapper.findAll('button').find((button) => button.text().includes('获取临时邮箱'))
    expect(mailboxButton).toBeDefined()
    await mailboxButton!.trigger('click')
    await flushPromises()
    expect(teamChildAPI.createMailbox).toHaveBeenCalledTimes(1)
    expect(teamChildAPI.generateOpenAIAuthUrl).toHaveBeenCalledTimes(1)

    await wrapper.get('[data-testid="team-member-select"]').trigger('click')
    await wrapper.get('[data-testid="team-start-workflow"]').trigger('click')
    await flushPromises()
    expect(teamChildAPI.startTeamChildWorkflow).not.toHaveBeenCalled()

    await wrapper.get('[data-testid="confirm-dialog"]').trigger('click')
    await flushPromises()
    expect(teamChildAPI.startTeamChildWorkflow).toHaveBeenCalledWith({
      seat_email: 'member@example.test',
      invite_email: 'team-child@example.test',
      auth_url: 'https://auth.openai.com/authorize?state=team-state',
      seat_already_removed: false,
      confirmed: true
    })
    expect(wrapper.get('[data-testid="team-sms-receiver"]').attributes('data-active')).toBe('true')

    wrapper.unmount()
  })

  it('continues after a manually released seat only when the live members are protected', async () => {
    teamChildAPI.refreshTeamChildMembers.mockResolvedValue({
      ready: true,
      members: [
        { id: 'owner@example.test', email: 'owner@example.test', role: 'owner', protected: true },
        { id: 'admin@example.test', email: 'admin@example.test', role: 'admin', protected: true }
      ]
    })
    const wrapper = mountView()
    await flushPromises()

    const mailboxButton = wrapper.findAll('button').find((button) => button.text().includes('获取临时邮箱'))
    expect(mailboxButton).toBeDefined()
    await mailboxButton!.trigger('click')
    await flushPromises()
    await wrapper.get('[data-testid="team-start-workflow"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-testid="confirm-dialog"]').trigger('click')
    await flushPromises()

    expect(teamChildAPI.startTeamChildWorkflow).toHaveBeenCalledWith({
      invite_email: 'team-child@example.test',
      auth_url: 'https://auth.openai.com/authorize?state=team-state',
      seat_already_removed: true,
      confirmed: true
    })
    wrapper.unmount()
  })

  it('restores the active temporary mailbox after a refresh without creating another address', async () => {
    teamChildAPI.getActiveMailbox.mockResolvedValue({
      session_id: 'restored-mailbox-session',
      email: 'restored@example.test',
      expires_at: '2026-08-22T12:30:00.000Z'
    })
    const wrapper = mountView()
    await flushPromises()

    expect(teamChildAPI.getActiveMailbox).toHaveBeenCalledTimes(1)
    expect(teamChildAPI.createMailbox).not.toHaveBeenCalled()
    expect(teamChildAPI.generateOpenAIAuthUrl).toHaveBeenCalledTimes(1)
    expect(wrapper.text()).toContain('restored@example.test')
    wrapper.unmount()
  })

  it('forwards confirmed SMS events to the active OAuth workflow', async () => {
    const wrapper = mountView()
    await flushPromises()
    const mailboxButton = wrapper.findAll('button').find((button) => button.text().includes('获取临时邮箱'))
    await mailboxButton!.trigger('click')
    await flushPromises()
    await wrapper.get('[data-testid="team-member-select"]').trigger('click')
    await wrapper.get('[data-testid="team-start-workflow"]').trigger('click')
    await wrapper.get('[data-testid="confirm-dialog"]').trigger('click')
    await flushPromises()

    await wrapper.get('[data-testid="sms-phone-ready"]').trigger('click')
    await wrapper.get('[data-testid="sms-code-received"]').trigger('click')
    await flushPromises()

    expect(teamChildAPI.submitTeamChildWorkflowPhone).toHaveBeenCalledWith('workflow-token-abcdefghijklmnop', '+14155550123')
    expect(teamChildAPI.submitTeamChildWorkflowCode).toHaveBeenCalledWith('workflow-token-abcdefghijklmnop', '123456')
    wrapper.unmount()
  })

  it('restarts only OAuth after confirmed SMS cancellation', async () => {
    const wrapper = mountView()
    await flushPromises()
    const mailboxButton = wrapper.findAll('button').find((button) => button.text().includes('获取临时邮箱'))
    await mailboxButton!.trigger('click')
    await flushPromises()
    await wrapper.get('[data-testid="team-member-select"]').trigger('click')
    await wrapper.get('[data-testid="team-start-workflow"]').trigger('click')
    await wrapper.get('[data-testid="confirm-dialog"]').trigger('click')
    await flushPromises()

    await wrapper.get('[data-testid="sms-session-cancelled"]').trigger('click')
    await flushPromises()

    expect(teamChildAPI.generateOpenAIAuthUrl).toHaveBeenCalledTimes(2)
    expect(teamChildAPI.restartTeamChildWorkflowOAuth).toHaveBeenCalledWith(
      'workflow-token-abcdefghijklmnop',
      'https://auth.openai.com/authorize?state=team-state'
    )
    expect(teamChildAPI.cancelTeamChildWorkflow).not.toHaveBeenCalled()
    wrapper.unmount()
  })
})

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

const { teamChildAPI, groupsAPI, appStore, nativeGenerateAuthUrl } = vi.hoisted(() => ({
  teamChildAPI: {
    getMailboxStatus: vi.fn(),
    createMailbox: vi.fn(),
    getActiveMailbox: vi.fn(),
    getActiveTeamChildWorkflow: vi.fn(),
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
    runTeamChildWorkflowStep: vi.fn(),
    getTeamChildWorkflow: vi.fn(),
    continueTeamChildWorkflow: vi.fn(),
    submitTeamChildWorkflowPhone: vi.fn(),
    submitTeamChildWorkflowCode: vi.fn(),
    restartTeamChildWorkflowOAuth: vi.fn(),
    cancelTeamChildWorkflow: vi.fn(),
    createOpenAIAccountFromOAuth: vi.fn()
  },
  groupsAPI: { getAll: vi.fn() },
  nativeGenerateAuthUrl: vi.fn(),
  appStore: {
    showError: vi.fn(),
    showInfo: vi.fn(),
    showSuccess: vi.fn()
  }
}))

vi.mock('@/api/admin/teamChild', () => ({ teamChildAPI }))
vi.mock('@/api/admin/groups', () => ({ groupsAPI }))
vi.mock('@/composables/useOpenAIOAuth', async () => {
  const { ref } = await import('vue')
  return {
    useOpenAIOAuth: () => {
      const authUrl = ref('')
      const sessionId = ref('')
      const oauthState = ref('')
      const loading = ref(false)
      const error = ref('')
      return {
        authUrl,
        sessionId,
        oauthState,
        loading,
        error,
        resetState: () => {
          authUrl.value = ''
          sessionId.value = ''
          oauthState.value = ''
          loading.value = false
          error.value = ''
        },
        generateAuthUrl: async () => {
          loading.value = true
          const result = await nativeGenerateAuthUrl()
          authUrl.value = result.auth_url
          sessionId.value = result.session_id
          oauthState.value = new URL(result.auth_url).searchParams.get('state') || ''
          loading.value = false
          return true
        }
      }
    }
  }
})
vi.mock('@/stores/app', () => ({ useAppStore: () => appStore }))

import TeamChildCreationView from '../TeamChildCreationView.vue'

const testAuthURL = 'https://auth.openai.com/oauth/authorize?client_id=app_EMoamEEZ73f0CkXaXp7hrann&code_challenge=test-challenge&code_challenge_method=S256&codex_cli_simplified_flow=true&id_token_add_organizations=true&redirect_uri=http%3A%2F%2Flocalhost%3A1455%2Fauth%2Fcallback&response_type=code&scope=openid+profile+email+offline_access&state=team-state'
const freshTestAuthURL = 'https://auth.openai.com/oauth/authorize?client_id=app_EMoamEEZ73f0CkXaXp7hrann&code_challenge=fresh-challenge&code_challenge_method=S256&codex_cli_simplified_flow=true&id_token_add_organizations=true&redirect_uri=http%3A%2F%2Flocalhost%3A1455%2Fauth%2Fcallback&response_type=code&scope=openid+profile+email+offline_access&state=fresh-team-state'

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
    <button type="button" data-testid="team-run-step" @click="$emit('run-step', 'invite')" />
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
    teamChildAPI.getActiveTeamChildWorkflow.mockResolvedValue(null)
    nativeGenerateAuthUrl.mockResolvedValue({
      auth_url: testAuthURL,
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
    teamChildAPI.runTeamChildWorkflowStep.mockResolvedValue({
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

  it('restores an active workflow after the page is reopened', async () => {
    teamChildAPI.getActiveTeamChildWorkflow.mockResolvedValue({
      id: 'workflow-token-abcdefghijklmnop',
      status: 'manual_required',
      manual_required: true,
      expires_at: '2026-08-22T12:30:00.000Z',
      steps: [
        { key: 'invite', number: 3, label: '邀请临时邮箱', status: 'completed' },
        { key: 'oauth', number: 4, label: '打开 OpenAI 授权页', status: 'completed' },
        { key: 'verify', number: 5, label: '完成外部验证并捕获回调', status: 'waiting' }
      ]
    })

    const wrapper = mountView()
    await flushPromises()

    expect(teamChildAPI.getActiveTeamChildWorkflow).toHaveBeenCalledTimes(1)
    expect(wrapper.text()).toContain('等待外部验证')
    wrapper.unmount()
  })

  it('keeps the mailbox configuration action on one line', async () => {
    const wrapper = mountView()
    await flushPromises()

    const configButton = wrapper.findAll('button').find((button) => button.text().includes('导入邮箱配置'))
    expect(configButton).toBeDefined()
    expect(configButton!.classes()).toContain('whitespace-nowrap')

    wrapper.unmount()
  })

  it('requires the in-page confirmation and reuses the displayed official OAuth session', async () => {
    const wrapper = mountView()
    await flushPromises()

    const mailboxButton = wrapper.findAll('button').find((button) => button.text().includes('获取临时邮箱'))
    expect(mailboxButton).toBeDefined()
    await mailboxButton!.trigger('click')
    await flushPromises()
    expect(teamChildAPI.createMailbox).toHaveBeenCalledTimes(1)
    expect(nativeGenerateAuthUrl).toHaveBeenCalledTimes(1)

    await wrapper.get('[data-testid="team-member-select"]').trigger('click')
    await wrapper.get('[data-testid="team-start-workflow"]').trigger('click')
    await flushPromises()
    expect(teamChildAPI.startTeamChildWorkflow).not.toHaveBeenCalled()

    await wrapper.get('[data-testid="confirm-dialog"]').trigger('click')
    await flushPromises()
    expect(nativeGenerateAuthUrl).toHaveBeenCalledTimes(1)
    expect(teamChildAPI.startTeamChildWorkflow).toHaveBeenCalledWith({
      seat_email: 'member@example.test',
      invite_email: 'team-child@example.test',
      auth_url: testAuthURL,
      seat_already_removed: false,
      start_step: 'members',
      confirmed: true
    })
    expect(wrapper.get('[data-testid="team-sms-receiver"]').attributes('data-active')).toBe('true')

    wrapper.unmount()
  })

  it('runs a selected unfinished workflow step only after in-page confirmation', async () => {
    teamChildAPI.startTeamChildWorkflow.mockResolvedValueOnce({
      id: 'workflow-token-abcdefghijklmnop',
      status: 'manual_required',
      manual_required: true,
      expires_at: '2026-08-22T12:30:00.000Z',
      steps: [
        { key: 'members', number: 1, label: '读取成员席位', status: 'completed' },
        { key: 'remove', number: 2, label: '移除已选成员', status: 'completed' },
        { key: 'invite', number: 3, label: '邀请临时邮箱', status: 'pending' },
        { key: 'oauth', number: 4, label: '打开 OpenAI 授权页', status: 'pending' },
        { key: 'verify', number: 5, label: '完成外部验证并捕获回调', status: 'pending' }
      ]
    })
    const wrapper = mountView()
    await flushPromises()
    const mailboxButton = wrapper.findAll('button').find((button) => button.text().includes('获取临时邮箱'))
    await mailboxButton!.trigger('click')
    await flushPromises()
    await wrapper.get('[data-testid="team-member-select"]').trigger('click')
    await wrapper.get('[data-testid="team-start-workflow"]').trigger('click')
    await wrapper.get('[data-testid="confirm-dialog"]').trigger('click')
    await flushPromises()

    await wrapper.get('[data-testid="team-run-step"]').trigger('click')
    expect(teamChildAPI.runTeamChildWorkflowStep).not.toHaveBeenCalled()
    await wrapper.get('[data-testid="confirm-dialog"]').trigger('click')
    await flushPromises()

    expect(teamChildAPI.runTeamChildWorkflowStep).toHaveBeenCalledWith('workflow-token-abcdefghijklmnop', 'invite')
    wrapper.unmount()
  })

  it('starts directly at OAuth after the live page shows only protected members and a pending invite', async () => {
    teamChildAPI.refreshTeamChildMembers.mockResolvedValue({
      ready: true,
      pending_invites: 1,
      members: [{ id: 'owner@example.test', email: 'owner@example.test', role: 'owner', protected: true }]
    })
    const wrapper = mountView()
    await flushPromises()
    const mailboxButton = wrapper.findAll('button').find((button) => button.text().includes('获取临时邮箱'))
    await mailboxButton!.trigger('click')
    await flushPromises()

    await wrapper.get('[data-testid="team-step-verify"]').trigger('click')
    await wrapper.get('[data-testid="confirm-dialog"]').trigger('click')
    await flushPromises()

    expect(teamChildAPI.startTeamChildWorkflow).toHaveBeenCalledWith({
      invite_email: 'team-child@example.test',
      auth_url: testAuthURL,
      seat_already_removed: true,
      start_step: 'oauth',
      run_only_step: true,
      confirmed: true
    })
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
      auth_url: testAuthURL,
      seat_already_removed: true,
      start_step: 'members',
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
    expect(nativeGenerateAuthUrl).toHaveBeenCalledTimes(1)
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
    nativeGenerateAuthUrl
      .mockResolvedValueOnce({ auth_url: testAuthURL, session_id: 'oauth-session-initial' })
      .mockResolvedValueOnce({ auth_url: freshTestAuthURL, session_id: 'oauth-session-fresh' })
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

    expect(nativeGenerateAuthUrl).toHaveBeenCalledTimes(2)
    expect(teamChildAPI.restartTeamChildWorkflowOAuth).toHaveBeenCalledWith(
      'workflow-token-abcdefghijklmnop',
      freshTestAuthURL
    )
    expect(teamChildAPI.cancelTeamChildWorkflow).not.toHaveBeenCalled()
    wrapper.unmount()
  })
})

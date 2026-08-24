import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import TeamChildOAuthWorkspace from '../TeamChildOAuthWorkspace.vue'

const PixlabSMSReceiverStub = {
  props: { active: { type: Boolean, default: false } },
  template: '<div data-testid="team-sms-receiver" :data-active="String(active)" />'
}

const IconStub = { template: '<span />' }

function workflow(overrides: Record<string, unknown> = {}) {
  return {
    id: 'workflow-token-abcdefghijklmnop',
    status: 'manual_required',
    manual_required: true,
    current_node: 'sms_confirm',
    expires_at: '2026-08-24T00:00:00.000Z',
    steps: [
      { key: 'members', number: 1, label: '读取成员席位', status: 'completed', message: '已完成' },
      { key: 'remove', number: 2, label: '移除已选成员', status: 'completed', message: '已完成' },
      { key: 'invite', number: 3, label: '邀请临时邮箱', status: 'completed', message: '已完成' },
      { key: 'oauth', number: 4, label: '打开 OpenAI 授权页', status: 'completed', message: '授权页已打开' },
      { key: 'verify', number: 5, label: '完成外部授权并提交回调', status: 'waiting', message: '正在等待验证码输入' }
    ],
    ...overrides
  }
}

describe('TeamChildOAuthWorkspace', () => {
  it('activates the confirmed SMS receiver only at the phone node', async () => {
    const wrapper = mount(TeamChildOAuthWorkspace, {
      props: {
        workflow: workflow(),
        mailboxEmail: 'team-child@example.test',
        authUrl: 'https://auth.openai.com/oauth/authorize?state=test-state'
      },
      global: {
        stubs: {
          Icon: IconStub,
          PixlabSMSReceiver: PixlabSMSReceiverStub
        }
      }
    })

    expect(wrapper.get('[data-testid="team-oauth-workspace"]').text()).toContain('成员授权工作区')
    expect(wrapper.get('[data-testid="team-oauth-current-node"]').text()).toContain('领取手机号')
    expect(wrapper.text()).toContain('已完成')
    expect(wrapper.get('[data-testid="team-sms-receiver"]').attributes('data-active')).toBe('true')
    expect(wrapper.find('[data-testid="team-oauth-manual-code"]').exists()).toBe(false)
    expect(wrapper.text()).toContain('不会在此处展示或写入工作流记录')
    const authLink = wrapper.get('[data-testid="team-open-auth"]')
    expect(authLink.element.tagName).toBe('BUTTON')
    expect(authLink.attributes('href')).toBeUndefined()
    expect(authLink.text()).toContain('内嵌浏览器')
    expect(wrapper.text()).toContain('上方只显示当前节点')
    expect(wrapper.get('[data-testid="team-oauth-progress-card"]').classes()).toContain('sticky')
    expect(wrapper.get('[data-testid="team-oauth-operation-card"]').text()).toContain('XIASS 官方 OAuth PKCE 链接')
    expect(wrapper.get('[data-testid="team-oauth-progress-card"]').find('[data-testid="team-open-auth"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="team-oauth-operation-card"]').find('[data-testid="team-open-auth"]').exists()).toBe(true)
  })

  it('shows callback state without activating the receiver for import', () => {
    const wrapper = mount(TeamChildOAuthWorkspace, {
      props: {
        workflow: workflow({ status: 'callback_ready', manual_required: false }),
        mailboxEmail: 'team-child@example.test',
        callbackURL: 'http://localhost:1455/auth/callback?code=one&state=two'
      },
      global: {
        stubs: {
          Icon: IconStub,
          PixlabSMSReceiver: PixlabSMSReceiverStub
        }
      }
    })

    expect(wrapper.text()).toContain('回调已就绪')
    expect(wrapper.text()).toContain('复制回调地址')
    expect(wrapper.get('[data-testid="team-sms-receiver"]').attributes('data-active')).toBe('false')
  })

  it('emits an in-app browser navigation request without opening a local window', async () => {
    const open = vi.spyOn(window, 'open')
    const wrapper = mount(TeamChildOAuthWorkspace, {
      props: {
        workflow: workflow(),
        authUrl: 'https://auth.openai.com/oauth/authorize?state=test-state'
      },
      global: {
        stubs: {
          Icon: IconStub,
          PixlabSMSReceiver: PixlabSMSReceiverStub
        }
      }
    })

    await wrapper.get('[data-testid="team-open-auth"]').trigger('click')

    expect(open).not.toHaveBeenCalled()
    expect(wrapper.emitted('open-auth-in-browser')).toHaveLength(1)
    open.mockRestore()
  })

  it('offers embedded-browser recovery and continuation for every failed node', async () => {
    const wrapper = mount(TeamChildOAuthWorkspace, {
      props: {
        workflow: workflow({ status: 'failed', current_node: 'profile', error: '资料页字段变化' }),
        authUrl: 'https://auth.openai.com/oauth/authorize?state=test-state'
      },
      global: {
        stubs: {
          Icon: IconStub,
          PixlabSMSReceiver: PixlabSMSReceiverStub
        }
      }
    })

    await wrapper.get('[data-testid="team-open-browser-after-failure"]').trigger('click')
    await wrapper.get('[data-testid="team-continue-after-failure"]').trigger('click')

    expect(wrapper.get('[data-testid="team-oauth-progress-card"]').find('[data-testid="team-open-browser-after-failure"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="team-oauth-operation-card"]').find('[data-testid="team-open-browser-after-failure"]').exists()).toBe(true)
    expect(wrapper.emitted('open-browser')).toHaveLength(1)
    expect(wrapper.emitted('continue-workflow')).toHaveLength(1)
  })

  it('routes a rejected phone directly to one confirmed replacement instead of generic continuation', () => {
    const wrapper = mount(TeamChildOAuthWorkspace, {
      props: {
        workflow: workflow({ status: 'failed', current_node: 'phone_submit', error: '当前手机号不可用或使用次数过多' })
      },
      global: {
        stubs: {
          Icon: IconStub,
          PixlabSMSReceiver: {
            props: ['active', 'replacementRequired'],
            template: '<div data-testid="team-sms-receiver" :data-replacement-required="String(replacementRequired)" />'
          }
        }
      }
    })

    expect(wrapper.get('[data-testid="team-sms-receiver"]').attributes('data-replacement-required')).toBe('true')
    expect(wrapper.find('[data-testid="team-continue-after-failure"]').exists()).toBe(false)
  })
})

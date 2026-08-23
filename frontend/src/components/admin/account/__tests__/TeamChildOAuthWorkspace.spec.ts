import { describe, expect, it } from 'vitest'
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
    expires_at: '2026-08-24T00:00:00.000Z',
    steps: [
      { key: 'members', number: 1, label: '读取成员席位', status: 'completed', message: '已完成' },
      { key: 'remove', number: 2, label: '移除已选成员', status: 'completed', message: '已完成' },
      { key: 'invite', number: 3, label: '邀请临时邮箱', status: 'completed', message: '已完成' },
      { key: 'oauth', number: 4, label: '打开 OpenAI 授权页', status: 'completed', message: '授权页已打开' },
      { key: 'verify', number: 5, label: '完成外部验证并捕获回调', status: 'waiting', message: '正在等待验证码输入' }
    ],
    ...overrides
  }
}

describe('TeamChildOAuthWorkspace', () => {
  it('keeps the external verification state in the modular workspace and submits a temporary code', async () => {
    const wrapper = mount(TeamChildOAuthWorkspace, {
      props: {
        workflow: workflow(),
        mailboxEmail: 'team-child@example.test'
      },
      global: {
        stubs: {
          Icon: IconStub,
          PixlabSMSReceiver: PixlabSMSReceiverStub
        }
      }
    })

    expect(wrapper.get('[data-testid="team-oauth-workspace"]').text()).toContain('官方 OAuth 验证')
    expect(wrapper.get('[data-testid="team-sms-receiver"]').attributes('data-active')).toBe('true')

    const codeInput = wrapper.get('[data-testid="team-oauth-manual-code"]')
    await codeInput.setValue('123456')
    await codeInput.trigger('keyup.enter')

    expect(wrapper.emitted('code-received')).toEqual([['123456']])
    expect((codeInput.element as HTMLInputElement).value).toBe('')
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
})

import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import TeamChildHistoryPanel from '../TeamChildHistoryPanel.vue'

const IconStub = { template: '<span />' }

function teamAccount(overrides: Record<string, unknown> = {}) {
  return {
    id: 317,
    name: 'team1004@example.test',
    platform: 'openai',
    type: 'oauth',
    credentials_status: { has_xiass_team_child_password_encrypted: true },
    extra: { xiass_team_child: true, xiass_team_child_email: 'team1004@example.test' },
    proxy_id: null,
    concurrency: 10,
    priority: 1,
    status: 'error',
    error_message: 'OpenAI upstream returned HTTP 401 Unauthorized',
    last_used_at: null,
    created_at: '2026-08-25T00:00:00Z',
    updated_at: '2026-08-25T00:00:00Z',
    schedulable: false,
    ...overrides
  }
}

describe('TeamChildHistoryPanel', () => {
  it('shows the exact 401 reason and emits reauthorization for a saved Team login', async () => {
    const account = teamAccount()
    const wrapper = mount(TeamChildHistoryPanel, {
      props: {
        entries: [{
          email: 'team1004@example.test',
          account: account as never,
          usage: { needs_reauth: true, error_code: 'unauthenticated', error: 'HTTP 401' } as never,
          passwordAvailable: true
        }]
      },
      global: {
        stubs: {
          Icon: IconStub,
          RouterLink: { template: '<a><slot /></a>' }
        }
      }
    })

    expect(wrapper.text()).toContain('401 · 授权失效')
    expect(wrapper.text()).toContain('OpenAI OAuth 凭据已失效，需要重新授权')
    const button = wrapper.get('[data-testid="team-history-reauthorize-317"]')
    await button.trigger('click')
    expect(wrapper.emitted('reauthorize')?.[0]?.[0]).toMatchObject({ email: 'team1004@example.test', account })
  })

  it('distinguishes a 403 while keeping the manual reauthorization entry available', () => {
    const wrapper = mount(TeamChildHistoryPanel, {
      props: {
        entries: [{
          email: 'team1004@example.test',
          account: teamAccount({ error_message: 'HTTP 403 Forbidden' }) as never,
          usage: { error_code: 'forbidden', is_forbidden: true } as never,
          passwordAvailable: true
        }]
      },
      global: { stubs: { Icon: IconStub, RouterLink: true } }
    })

    expect(wrapper.text()).toContain('403 · 访问受限')
    expect(wrapper.find('[data-testid="team-history-reauthorize-317"]').exists()).toBe(true)
  })

  it('offers manual reauthorization for a normal imported Team account', async () => {
    const account = teamAccount({ status: 'active', error_message: '', schedulable: true })
    const wrapper = mount(TeamChildHistoryPanel, {
      props: {
        entries: [{
          email: 'team1004@example.test',
          account: account as never,
          usage: null,
          passwordAvailable: true
        }]
      },
      global: { stubs: { Icon: IconStub, RouterLink: true } }
    })

    expect(wrapper.text()).toContain('正常')
    const button = wrapper.get('[data-testid="team-history-reauthorize-317"]')
    expect(button.attributes('disabled')).toBeUndefined()
    await button.trigger('click')
    expect(wrapper.emitted('reauthorize')?.[0]?.[0]).toMatchObject({ account })
  })

  it('keeps reauthorization available when a Team account has no saved password', async () => {
    const account = teamAccount({
      status: 'error',
      credentials_status: {},
      error_message: 'OpenAI upstream returned HTTP 401 Unauthorized'
    })
    const wrapper = mount(TeamChildHistoryPanel, {
      props: {
        entries: [{
          email: 'team1004@example.test',
          account: account as never,
          usage: { needs_reauth: true, error_code: 'unauthenticated', error: 'HTTP 401' } as never,
          passwordAvailable: false
        }]
      },
      global: { stubs: { Icon: IconStub, RouterLink: true } }
    })

    const button = wrapper.get('[data-testid="team-history-reauthorize-317"]')
    expect(button.attributes('disabled')).toBeUndefined()
    expect(button.attributes('title')).toContain('等待人工输入')
    await button.trigger('click')
    expect(wrapper.emitted('reauthorize')?.[0]?.[0]).toMatchObject({ account })
  })

  it('links to the exact account and emits direct account deletion', async () => {
    const account = teamAccount({ status: 'active', error_message: '', schedulable: true })
    const wrapper = mount(TeamChildHistoryPanel, {
      props: {
        entries: [{
          email: 'team1004@example.test',
          account: account as never,
          usage: null,
          passwordAvailable: true
        }]
      },
      global: {
        stubs: {
          Icon: IconStub,
          RouterLink: { name: 'RouterLink', props: ['to'], template: '<a><slot /></a>' }
        }
      }
    })

    expect(wrapper.getComponent({ name: 'RouterLink' }).props('to')).toEqual({
      name: 'AdminAccounts',
      query: { account_id: '317' }
    })
    await wrapper.get('[data-testid="team-history-delete-317"]').trigger('click')
    expect(wrapper.emitted('delete-account')?.[0]?.[0]).toMatchObject({ account })
  })
})

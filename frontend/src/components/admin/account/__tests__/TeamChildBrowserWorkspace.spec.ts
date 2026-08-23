import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'

import TeamChildBrowserWorkspace from '../TeamChildBrowserWorkspace.vue'

describe('TeamChildBrowserWorkspace', () => {
  it('shows the persistent browser and makes the temporary invitation mailbox easy to copy', async () => {
    const wrapper = mount(TeamChildBrowserWorkspace, {
      props: {
        configured: true,
        embedUrl: '/api/v1/team-child-browser/?ticket=one-time-ticket',
        mailboxEmail: 'team-child@example.test'
      },
      global: {
        stubs: {
          Icon: true
        }
      }
    })

    expect(wrapper.get('iframe').attributes('src')).toBe('/api/v1/team-child-browser/?ticket=one-time-ticket')
    expect(wrapper.get('[data-testid="team-browser-frame"]').classes()).toContain('aspect-video')
    expect(wrapper.get('[data-testid="team-browser-frame"]').classes()).toContain('overflow-hidden')
    expect(wrapper.text()).toContain('邀请成员邮箱')
    expect(wrapper.text()).toContain('team-child@example.test')

    await wrapper.get('[aria-label="复制邀请成员邮箱"]').trigger('click')
    expect(wrapper.emitted('copyMailbox')).toHaveLength(1)

    await wrapper.get('[aria-label="刷新服务器浏览器"]').trigger('click')
    expect(wrapper.emitted('reload')).toHaveLength(1)
  })

  it('explains the deployment state when the server browser is disabled', () => {
    const wrapper = mount(TeamChildBrowserWorkspace, {
      props: {
        configured: false,
        embedUrl: ''
      },
      global: {
        stubs: {
          Icon: true
        }
      }
    })

    expect(wrapper.find('iframe').exists()).toBe(false)
    expect(wrapper.text()).toContain('服务器浏览器尚未启用')
  })

  it('keeps text actions on one line', () => {
    const wrapper = mount(TeamChildBrowserWorkspace, {
      props: {
        configured: true,
        embedUrl: '',
        membersReady: true,
        controlConflict: true
      },
      global: {
        stubs: {
          Icon: true
        }
      }
    })

    for (const button of wrapper.findAll('button').filter((item) => item.text().trim())) {
      expect(button.classes()).toContain('whitespace-nowrap')
    }
  })
})

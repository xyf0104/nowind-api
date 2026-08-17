import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import OpenAIReauthorizationPanel from '../OpenAIReauthorizationPanel.vue'
import {
  exchangeOpenAIReauthorization,
  startOpenAIReauthorization,
} from '@/api/tokenTools'

vi.mock('@/api/tokenTools', () => ({
  startOpenAIReauthorization: vi.fn(),
  exchangeOpenAIReauthorization: vi.fn(),
}))

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string) => {
      const messages: Record<string, string> = {
        'tokenConverter.reauthorization.title': '401 重新登录授权',
        'tokenConverter.reauthorization.generate': '获取授权链接',
        'tokenConverter.reauthorization.getToken': '获取 Token',
      }
      return messages[key] ?? key
    },
  }),
}))

function mountPanel() {
  return mount(OpenAIReauthorizationPanel, {
    global: {
      stubs: {
        Icon: { template: '<i />' },
      },
    },
  })
}

describe('OpenAIReauthorizationPanel', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    vi.mocked(startOpenAIReauthorization).mockResolvedValue({
      auth_url: 'https://auth.openai.com/oauth/authorize?state=expected-state',
      session_id: 'session-id',
    })
    vi.mocked(exchangeOpenAIReauthorization).mockResolvedValue({
      access_token: 'access-token',
      refresh_token: 'refresh-token',
      expires_in: 3600,
      expires_at: 1_788_000_000,
    })
  })

  it('starts a free flow and exchanges a complete localhost callback URL', async () => {
    const wrapper = mountPanel()

    await wrapper.findAll('button').find((button) => button.text().includes('获取授权链接'))!.trigger('click')
    await flushPromises()

    expect(startOpenAIReauthorization).toHaveBeenCalledOnce()
    expect(wrapper.get('a[href^="https://auth.openai.com/"]').attributes('target')).toBe('_blank')

    await wrapper.get('#openai-oauth-callback').setValue(
      'http://localhost:1455/auth/callback?code=callback-code&state=expected-state',
    )
    await wrapper.findAll('button').find((button) => button.text().includes('获取 Token'))!.trigger('click')
    await flushPromises()

    expect(exchangeOpenAIReauthorization).toHaveBeenCalledWith({
      session_id: 'session-id',
      code: 'callback-code',
      state: 'expected-state',
    })
    const emitted = wrapper.emitted('tokens')?.[0]?.[0] as string
    expect(JSON.parse(emitted)).toMatchObject({
      access_token: 'access-token',
      refresh_token: 'refresh-token',
    })
  })

  it('accepts a raw Code and uses the state from the generated authorization URL', async () => {
    const wrapper = mountPanel()
    await wrapper.findAll('button').find((button) => button.text().includes('获取授权链接'))!.trigger('click')
    await flushPromises()

    await wrapper.get('#openai-oauth-callback').setValue('raw-code')
    await wrapper.findAll('button').find((button) => button.text().includes('获取 Token'))!.trigger('click')
    await flushPromises()

    expect(exchangeOpenAIReauthorization).toHaveBeenCalledWith({
      session_id: 'session-id',
      code: 'raw-code',
      state: 'expected-state',
    })
  })

  it('shows authorization-start failures and clears any stale session link', async () => {
    const wrapper = mountPanel()
    const generateButton = () => wrapper.findAll('button').find((button) => button.text().includes('获取授权链接'))!

    await generateButton().trigger('click')
    await flushPromises()
    expect(wrapper.find('a[href^="https://auth.openai.com/"]').exists()).toBe(true)

    vi.mocked(startOpenAIReauthorization).mockRejectedValueOnce(new Error('Redis unavailable'))
    await generateButton().trigger('click')
    await flushPromises()

    expect(wrapper.find('a[href^="https://auth.openai.com/"]').exists()).toBe(false)
    expect(wrapper.get('[role="alert"]').text()).toContain('Redis unavailable')
  })
})

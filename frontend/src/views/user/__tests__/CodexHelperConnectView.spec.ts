import { flushPromises, mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'

const { getPublicSettings, listKeys } = vi.hoisted(() => ({
  getPublicSettings: vi.fn(),
  listKeys: vi.fn()
}))

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key })
}))

vi.mock('vue-router', () => ({
  useRoute: () => ({
    query: {
      callback: 'http://127.0.0.1:43123/callback',
      state: 'abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNO1'
    }
  })
}))

vi.mock('@/api', () => ({
  authAPI: { getPublicSettings },
  keysAPI: { list: listKeys }
}))

vi.mock('@/components/layout/AppLayout.vue', () => ({
  default: { template: '<main><slot /></main>' }
}))

vi.mock('@/components/icons/Icon.vue', () => ({
  default: { template: '<span />' }
}))

import CodexHelperConnectView from '../CodexHelperConnectView.vue'

describe('CodexHelperConnectView', () => {
  it('shows only active API keys assigned to an OpenAI group', async () => {
    getPublicSettings.mockResolvedValue({ api_base_url: 'https://gateway.example.com' })
    listKeys.mockResolvedValue({
      items: [
        {
          id: 1,
          key: 'sk-openai-active-1234567890',
          name: 'Codex 主密钥',
          status: 'active',
          group: { name: 'OpenAI', platform: 'openai' }
        },
        {
          id: 2,
          key: 'sk-openai-disabled-1234567890',
          name: '停用密钥',
          status: 'inactive',
          group: { name: 'OpenAI', platform: 'openai' }
        },
        {
          id: 3,
          key: 'sk-anthropic-active-1234567890',
          name: 'Claude 密钥',
          status: 'active',
          group: { name: 'Anthropic', platform: 'anthropic' }
        }
      ],
      page: 1,
      page_size: 100,
      pages: 1,
      total: 3
    })

    const wrapper = mount(CodexHelperConnectView, {
      global: {
        stubs: {
          RouterLink: { template: '<a><slot /></a>' }
        }
      }
    })
    await flushPromises()

    expect(wrapper.text()).toContain('Codex 主密钥')
    expect(wrapper.text()).not.toContain('停用密钥')
    expect(wrapper.text()).not.toContain('Claude 密钥')
    expect(listKeys).toHaveBeenCalledWith(1, 100, {
      status: 'active',
      sort_by: 'created_at',
      sort_order: 'desc'
    })
  })

  it('syncs the key-specific Codex catalog before allowing model selection', async () => {
    getPublicSettings.mockResolvedValue({ api_base_url: 'https://gateway.example.com' })
    listKeys.mockResolvedValue({
      items: [{
        id: 1,
        key: 'sk-openai-active-1234567890',
        name: 'Codex 主密钥',
        status: 'active',
        group: { name: 'OpenAI', platform: 'openai' }
      }],
      page: 1,
      page_size: 100,
      pages: 1,
      total: 1
    })
    const originalFetch = globalThis.fetch
    globalThis.fetch = vi.fn(async () => new Response(JSON.stringify({
      models: [{ slug: 'gpt-6-astra' }, { slug: 'gpt-5.6-sol' }]
    }), { status: 200 })) as typeof fetch

    try {
      const wrapper = mount(CodexHelperConnectView, {
        global: { stubs: { RouterLink: { template: '<a><slot /></a>' } } }
      })
      await flushPromises()
      const syncButton = wrapper.findAll('button').find((button) => button.text().includes('codexHelper.syncModels'))
      expect(syncButton).toBeDefined()
      await syncButton!.trigger('click')
      await flushPromises()

      const fetchMock = globalThis.fetch as typeof globalThis.fetch & {
        mock: { calls: Array<[string, RequestInit]> }
      }
      const [requestURL, requestInit] = fetchMock.mock.calls[0]
      expect(String(requestURL)).toBe('https://gateway.example.com/v1/models?client_version=0.146.0')
      expect(requestInit.cache).toBe('no-store')
      expect(requestInit.headers).toEqual({
        Accept: 'application/json',
        Authorization: 'Bearer sk-openai-active-1234567890'
      })
      expect(wrapper.findAll('select')[0].text()).toContain('gpt-6-astra')
    } finally {
      globalThis.fetch = originalFetch
    }
  })
})

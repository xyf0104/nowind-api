import { afterEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import TeamMailboxShareView from '../TeamMailboxShareView.vue'

const IconStub = { template: '<span />' }

function response(data: unknown, status = 200) {
  return {
    ok: status >= 200 && status < 300,
    status,
    json: async () => data,
  }
}

afterEach(() => {
  window.location.hash = ''
  vi.unstubAllGlobals()
  vi.useRealTimers()
})

describe('TeamMailboxShareView', () => {
  it('uses the fragment token for a scoped full inbox and reads verification messages without a copy control', async () => {
    window.location.hash = '#t=tm2.share-id.share-secret'
    const fetchMock = vi.fn((input: RequestInfo | URL) => {
      const url = String(input)
      if (url.endsWith('/public/team-mailbox/messages')) {
        return Promise.resolve(response({
          code: 0,
          data: {
            email: 'team1061@example.test',
            messages: [
              {
                id: 'family-update',
                from: 'Family <family@example.test>',
                subject: 'Family update',
                preview: 'Dinner is at 19:00.',
                received_at: '2026-08-30T12:00:00Z',
              },
              {
                id: 'openai-code',
                from: 'OpenAI <noreply@openai.com>',
                subject: 'OpenAI verification code: 418204',
                preview: 'Your verification code is 418204.',
                code: '418204',
                received_at: '2026-08-30T12:01:00Z',
              },
            ],
            checked_at: '2026-08-30T12:01:00Z',
            poll_after_ms: 5000,
          },
        }))
      }
      if (url.endsWith('/public/team-mailbox/messages/family-update')) {
        return Promise.resolve(response({
          code: 0,
          data: {
            id: 'family-update',
            from: 'Family <family@example.test>',
            subject: 'Family update',
            received_at: '2026-08-30T12:00:00Z',
            body: 'Dinner is at 19:00. Please bring dessert.',
          },
        }))
      }
      return Promise.reject(new Error(`unexpected request: ${url}`))
    })
    vi.stubGlobal('fetch', fetchMock)

    const wrapper = mount(TeamMailboxShareView, {
      global: { stubs: { Icon: IconStub } },
    })
    await flushPromises()
    await flushPromises()

    expect(fetchMock).toHaveBeenCalledWith('/api/v1/public/team-mailbox/messages', expect.objectContaining({
      credentials: 'omit',
      cache: 'no-store',
      referrerPolicy: 'no-referrer',
      headers: expect.objectContaining({ Authorization: 'Bearer tm2.share-id.share-secret' }),
    }))
    expect(wrapper.text()).toContain('team1061@example.test')
    expect(wrapper.text()).toContain('Family update')
    expect(wrapper.text()).toContain('OpenAI verification code: 418204')
    expect(wrapper.text()).toContain('Please bring dessert.')
    expect(wrapper.get('[data-testid="team-mailbox-message-family-update"]').exists()).toBe(true)
    expect(wrapper.get('[data-testid="team-mailbox-message-openai-code"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="team-mailbox-share-code"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('renders server-provided rich mail content in a sandboxed dark document after a second sanitization pass', async () => {
    window.location.hash = '#t=tm2.share-id.html-secret'
    const fetchMock = vi.fn((input: RequestInfo | URL) => {
      const url = String(input)
      if (url.endsWith('/public/team-mailbox/messages')) {
        return Promise.resolve(response({
          code: 0,
          data: {
            email: 'family@example.test',
            messages: [{
              id: 'rich-message',
              from: 'OpenAI <noreply@tm.openai.com>',
              subject: 'Account notice',
              preview: 'Your account needs attention.',
              received_at: '2026-08-30T12:01:00Z',
            }],
            checked_at: '2026-08-30T12:01:00Z',
            poll_after_ms: 5000,
          },
        }))
      }
      if (url.endsWith('/public/team-mailbox/messages/rich-message')) {
        return Promise.resolve(response({
          code: 0,
          data: {
            id: 'rich-message',
            from: 'OpenAI <noreply@tm.openai.com>',
            subject: 'Account notice',
            body: 'Your account needs attention.',
            html: '<style>.notice { color: #c2410c; }</style><div class="notice"><h2>Account notice</h2><p>Your account needs attention.</p><a href="https://help.openai.com/">Read more</a><img src="https://cdn.openai.com/logo.png" alt="OpenAI"><script>window.__unsafe = true</script><iframe src="https://unsafe.example.test"></iframe></div>',
          },
        }))
      }
      return Promise.reject(new Error(`unexpected request: ${url}`))
    })
    vi.stubGlobal('fetch', fetchMock)

    const wrapper = mount(TeamMailboxShareView, {
      global: { stubs: { Icon: IconStub } },
    })
    await flushPromises()
    await flushPromises()

    const rendered = wrapper.find<HTMLIFrameElement>('.team-mailbox-email-frame')
    expect(rendered.exists()).toBe(true)
    expect(rendered.attributes('sandbox')).toBe('allow-same-origin allow-popups')
    expect(rendered.attributes('referrerpolicy')).toBe('no-referrer')
    const source = rendered.attributes('srcdoc') || ''
    expect(source).toContain('Read more')
    expect(source).toContain('cdn.openai.com/logo.png')
    expect(source).toContain('.notice { color: #c2410c; }')
    expect(source).toContain("default-src 'none'")
    expect(source).not.toContain('<script')
    expect(source).not.toContain('<iframe')
    expect(wrapper.find('pre').exists()).toBe(false)
    wrapper.unmount()
  })

  it('replaces the active mailbox safely when a different share link is pasted into the same tab', async () => {
    window.location.hash = '#t=tm2.share-id.first-secret'
    let resolveFirstInbox: ((value: ReturnType<typeof response>) => void) | null = null
    const fetchMock = vi.fn((input: RequestInfo | URL, options?: RequestInit) => {
      const token = String((options?.headers as Record<string, string> | undefined)?.Authorization || '')
      if (!String(input).endsWith('/public/team-mailbox/messages')) {
        return Promise.reject(new Error(`unexpected request: ${String(input)}`))
      }
      if (token.endsWith('first-secret')) {
        return new Promise<ReturnType<typeof response>>((resolve) => {
          resolveFirstInbox = resolve
        })
      }
      if (token.endsWith('second-secret')) {
        return Promise.resolve(response({
          code: 0,
          data: {
            email: 'second@example.test',
            messages: [],
            checked_at: '2026-08-31T12:00:00Z',
            poll_after_ms: 5000,
          },
        }))
      }
      return Promise.reject(new Error(`unexpected token: ${token}`))
    })
    vi.stubGlobal('fetch', fetchMock)

    const wrapper = mount(TeamMailboxShareView, {
      global: { stubs: { Icon: IconStub } },
    })
    await flushPromises()

    window.location.hash = '#t=tm2.share-id.second-secret'
    window.dispatchEvent(new Event('hashchange'))
    await flushPromises()
    await flushPromises()

    expect(wrapper.text()).toContain('second@example.test')
    expect(fetchMock).toHaveBeenLastCalledWith('/api/v1/public/team-mailbox/messages', expect.objectContaining({
      headers: expect.objectContaining({ Authorization: 'Bearer tm2.share-id.second-secret' }),
    }))

    resolveFirstInbox?.(response({
      code: 0,
      data: {
        email: 'first@example.test',
        messages: [],
        checked_at: '2026-08-31T12:00:00Z',
      },
    }))
    await flushPromises()
    expect(wrapper.text()).toContain('second@example.test')
    expect(wrapper.text()).not.toContain('first@example.test')
    wrapper.unmount()
  })

  it('keeps polling only on the external mailbox page at the requested five-second interval', async () => {
    vi.useFakeTimers()
    window.location.hash = '#t=tm2.share-id.poll-secret'
    const fetchMock = vi.fn().mockResolvedValue(response({
      code: 0,
      data: {
        email: 'team1061@example.test',
        messages: [],
        checked_at: '2026-08-30T12:01:00Z',
        poll_after_ms: 5000,
      },
    }))
    vi.stubGlobal('fetch', fetchMock)

    const wrapper = mount(TeamMailboxShareView, {
      global: { stubs: { Icon: IconStub } },
    })
    await flushPromises()

    expect(fetchMock).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(4999)
    expect(fetchMock).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(1)
    await flushPromises()
    expect(fetchMock).toHaveBeenCalledTimes(2)

    wrapper.unmount()
    await vi.advanceTimersByTimeAsync(5000)
    expect(fetchMock).toHaveBeenCalledTimes(2)
    vi.useRealTimers()
  })

  it('shows no mailbox or account information when a shared token has been revoked', async () => {
    window.location.hash = '#t=tm2.share-id.revoked-secret'
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(response({ code: 404, message: '接码链接不可用' }, 404)))

    const wrapper = mount(TeamMailboxShareView, {
      global: { stubs: { Icon: IconStub } },
    })
    await flushPromises()

    expect(wrapper.text()).toContain('接码链接不可用')
    expect(wrapper.text()).not.toContain('team1061@example.test')
    wrapper.unmount()
  })
})

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
  it('uses the fragment token for a scoped full inbox and keeps verification-code copy convenient', async () => {
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
    const clipboardWrite = vi.fn().mockResolvedValue(undefined)
    vi.stubGlobal('fetch', fetchMock)
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText: clipboardWrite },
    })

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

    await wrapper.get('[data-testid="team-mailbox-share-code"]').trigger('click')
    expect(clipboardWrite).toHaveBeenCalledWith('418204')
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
    await vi.runAllTicks()
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

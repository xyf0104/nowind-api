import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import OAuthBillingBreakdownDialog from '../OAuthBillingBreakdownDialog.vue'
import type { OAuthAccountBillingBreakdown } from '@/types'

const { getOAuthBillingBreakdown } = vi.hoisted(() => ({
  getOAuthBillingBreakdown: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      getOAuthBillingBreakdown
    }
  }
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key
    })
  }
})

const BaseDialogStub = {
  name: 'BaseDialog',
  props: ['show', 'title', 'width'],
  emits: ['close'],
  template: '<section v-if="show" class="dialog-stub"><slot /></section>'
}

const globalStubs = {
  BaseDialog: BaseDialogStub,
  LoadingSpinner: {
    name: 'LoadingSpinner',
    template: '<div class="loading-spinner-stub">loading</div>'
  },
  Icon: {
    name: 'Icon',
    template: '<span class="icon-stub" />'
  }
}

const account = {
  id: 91,
  name: 'OAuth Team 14',
  type: 'oauth',
  status: 'active'
} as any

const initialRange = {
  windowLabel: '5h',
  startTime: '2026-08-13T01:15:00Z',
  endTime: '2026-08-13T06:15:00Z'
}

const summary = (overrides: Partial<OAuthAccountBillingBreakdown['summary']> = {}) => ({
  requests: 0,
  tokens: 0,
  account_cost: 0,
  user_cost: 0,
  ...overrides
})

const usersResponse = (
  username = 'Alice',
  overrides: Partial<OAuthAccountBillingBreakdown> = {}
): OAuthAccountBillingBreakdown => ({
  account_id: account.id,
  range: {
    start_time: initialRange.startTime,
    end_time: initialRange.endTime,
    timezone: 'Asia/Shanghai'
  },
  summary: summary({ requests: 12, tokens: 3456, account_cost: 8.25, user_cost: 10.5 }),
  users: [
    {
      user_id: 7,
      username,
      email: 'alice@example.com',
      requests: 12,
      tokens: 3456,
      account_cost: 8.25,
      user_cost: 10.5
    }
  ],
  ...overrides
})

const modelsResponse = (): OAuthAccountBillingBreakdown => ({
  account_id: account.id,
  range: {
    start_time: initialRange.startTime,
    end_time: initialRange.endTime,
    timezone: 'Asia/Shanghai'
  },
  summary: summary({ requests: 9, tokens: 2100, account_cost: 6.25, user_cost: 7.5 }),
  selected_user: {
    user_id: 7,
    username: 'Alice',
    email: 'alice@example.com'
  },
  models: [
    {
      model: 'gpt-5.6',
      requests: 9,
      tokens: 2100,
      account_cost: 6.25,
      user_cost: 7.5
    }
  ]
})

function deferred<T>() {
  let resolve!: (value: T | PromiseLike<T>) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise
    reject = rejectPromise
  })
  return { promise, resolve, reject }
}

function mountDialog() {
  return mount(OAuthBillingBreakdownDialog, {
    props: {
      show: true,
      account,
      initialRange
    },
    global: {
      stubs: globalStubs
    }
  })
}

function buttonContaining(wrapper: VueWrapper, text: string) {
  const button = wrapper.findAll('button').find((candidate) => candidate.text().includes(text))
  expect(button, `expected a button containing ${text}`).toBeDefined()
  return button!
}

describe('OAuthBillingBreakdownDialog', () => {
  beforeEach(() => {
    getOAuthBillingBreakdown.mockReset()
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('shows loading while the initial exact-window request is pending', async () => {
    const pending = deferred<OAuthAccountBillingBreakdown>()
    getOAuthBillingBreakdown.mockReturnValueOnce(pending.promise)

    const wrapper = mountDialog()
    await flushPromises()

    expect(wrapper.find('.loading-spinner-stub').exists()).toBe(true)
    expect(getOAuthBillingBreakdown).toHaveBeenCalledWith(account.id, expect.objectContaining({
      start_time: initialRange.startTime,
      end_time: initialRange.endTime
    }))

    pending.resolve(usersResponse())
    await flushPromises()
    expect(wrapper.find('.loading-spinner-stub').exists()).toBe(false)
  })

  it('drills from users into models and returns to the user list', async () => {
    getOAuthBillingBreakdown
      .mockResolvedValueOnce(usersResponse())
      .mockResolvedValueOnce(modelsResponse())
      .mockResolvedValueOnce(usersResponse())

    const wrapper = mountDialog()
    await flushPromises()

    expect(wrapper.text()).toContain('Alice')
    expect(wrapper.text()).toContain('alice@example.com')
    expect(wrapper.text()).toContain('usage.accountBilled $8.25')
    expect(wrapper.text()).toContain('usage.userBilled ¥10.50')
    expect(buttonContaining(wrapper, 'Alice').get('.text-emerald-600').text()).toContain('usage.accountBilled $8.25')

    await buttonContaining(wrapper, 'Alice').trigger('click')
    await flushPromises()

    expect(getOAuthBillingBreakdown).toHaveBeenNthCalledWith(2, account.id, expect.objectContaining({
      start_time: initialRange.startTime,
      end_time: initialRange.endTime,
      user_id: 7
    }))
    expect(wrapper.text()).toContain('gpt-5.6')
    expect(wrapper.text()).toContain('usage.modelBillingDetails')

    await wrapper.get('button[title="usage.backToUsers"]').trigger('click')
    await flushPromises()

    const thirdParams = getOAuthBillingBreakdown.mock.calls[2][1]
    expect(thirdParams).not.toHaveProperty('user_id')
    expect(wrapper.text()).toContain('Alice')
    expect(wrapper.text()).not.toContain('gpt-5.6')
  })

  it('keeps the mobile billing summary and detail rows centered without changing desktop alignment', async () => {
    getOAuthBillingBreakdown.mockResolvedValueOnce(usersResponse())

    const wrapper = mountDialog()
    await flushPromises()

    const summaryCards = wrapper.findAll('.grid.grid-cols-2 > div')
    expect(summaryCards).toHaveLength(4)
    for (const card of summaryCards) {
      expect(card.classes()).toContain('text-center')
      expect(card.classes()).toContain('sm:text-left')
    }

    const detailRow = buttonContaining(wrapper, 'Alice')
    expect(detailRow.classes()).toContain('text-center')
    expect(detailRow.classes()).toContain('md:text-left')
    expect(detailRow.findAll('span').some((span) => span.classes().includes('md:text-right'))).toBe(true)
    expect(detailRow.text()).toContain('usage.accountBilled $8.25')
    expect(detailRow.text()).toContain('usage.userBilled ¥10.50')
  })

  it('supports an exact custom minute range', async () => {
    getOAuthBillingBreakdown
      .mockResolvedValueOnce(usersResponse())
      .mockResolvedValueOnce(usersResponse('Minute Filter User'))

    const wrapper = mountDialog()
    await flushPromises()

    await wrapper.get('[data-test="billing-start-time"]').setValue('2026-08-14T00:00')
    await wrapper.get('[data-test="billing-end-time"]').setValue('2026-08-14T01:23')
    await wrapper.get('[data-test="billing-apply-time"]').trigger('click')
    await flushPromises()

    const minuteParams = getOAuthBillingBreakdown.mock.calls[1][1]
    expect(new Date(minuteParams.start_time as string).getTime()).toBe(new Date('2026-08-14T00:00').getTime())
    expect(new Date(minuteParams.end_time as string).getTime()).toBe(new Date('2026-08-14T01:23').getTime())
    expect(minuteParams).not.toHaveProperty('start_date')
    expect(minuteParams).not.toHaveProperty('end_date')
    expect(wrapper.text()).toContain('2026-08-14 00:00 - 2026-08-14 01:23')
    expect(wrapper.text()).toContain('Minute Filter User')
  })

  it('supports quick minute and hour ranges using the current click time', async () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-08-14T09:23:00+08:00'))
    getOAuthBillingBreakdown
      .mockResolvedValueOnce(usersResponse())
      .mockResolvedValueOnce(usersResponse('Quick Filter User'))

    const wrapper = mountDialog()
    await flushPromises()
    await wrapper.get('[data-test="billing-quick-30"]').trigger('click')
    await flushPromises()

    expect(getOAuthBillingBreakdown).toHaveBeenNthCalledWith(2, account.id, expect.objectContaining({
      start_time: '2026-08-14T00:53:00.000Z',
      end_time: '2026-08-14T01:23:00.000Z'
    }))
    expect(wrapper.get('[data-test="billing-quick-30"]').classes()).toContain('border-primary-500')
    expect(wrapper.text()).toContain('Quick Filter User')
  })

  it('rejects an invalid custom minute range without issuing a request', async () => {
    getOAuthBillingBreakdown.mockResolvedValueOnce(usersResponse())
    const wrapper = mountDialog()
    await flushPromises()

    await wrapper.get('[data-test="billing-start-time"]').setValue('2026-08-14T01:23')
    await wrapper.get('[data-test="billing-end-time"]').setValue('2026-08-14T00:00')
    await wrapper.get('[data-test="billing-apply-time"]').trigger('click')
    await flushPromises()

    expect(getOAuthBillingBreakdown).toHaveBeenCalledTimes(1)
    expect(wrapper.text()).toContain('usage.invalidMinuteRange')
  })

  it('shows an error and retries the same request successfully', async () => {
    getOAuthBillingBreakdown
      .mockRejectedValueOnce(new Error('network unavailable'))
      .mockResolvedValueOnce(usersResponse('Recovered User'))

    const wrapper = mountDialog()
    await flushPromises()

    expect(wrapper.text()).toContain('network unavailable')
    expect(wrapper.text()).toContain('common.retry')

    await buttonContaining(wrapper, 'common.retry').trigger('click')
    await flushPromises()

    expect(getOAuthBillingBreakdown).toHaveBeenCalledTimes(2)
    expect(getOAuthBillingBreakdown.mock.calls[1][1]).toEqual(getOAuthBillingBreakdown.mock.calls[0][1])
    expect(wrapper.text()).toContain('Recovered User')
    expect(wrapper.text()).not.toContain('network unavailable')
  })

  it('renders the empty state when the selected range has no billing rows', async () => {
    getOAuthBillingBreakdown.mockResolvedValueOnce(usersResponse('', {
      summary: summary(),
      users: []
    }))

    const wrapper = mountDialog()
    await flushPromises()

    expect(wrapper.text()).toContain('usage.noBillingDetails')
    expect(wrapper.text()).toContain('$0.00')
    expect(wrapper.text()).toContain('¥0.00')
  })

  it('does not let an expired request overwrite a newer result', async () => {
    const oldRequest = deferred<OAuthAccountBillingBreakdown>()
    getOAuthBillingBreakdown
      .mockReturnValueOnce(oldRequest.promise)
      .mockResolvedValueOnce(usersResponse('Newest Result'))

    const wrapper = mountDialog()
    await flushPromises()
    expect(wrapper.find('.loading-spinner-stub').exists()).toBe(true)

    await wrapper.get('[data-test="billing-start-time"]').setValue('2026-08-10T00:00')
    await wrapper.get('[data-test="billing-end-time"]').setValue('2026-08-12T00:00')
    await wrapper.get('[data-test="billing-apply-time"]').trigger('click')
    await flushPromises()

    expect(wrapper.text()).toContain('Newest Result')
    expect(wrapper.find('.loading-spinner-stub').exists()).toBe(false)

    oldRequest.resolve(usersResponse('Expired Result'))
    await flushPromises()

    expect(wrapper.text()).toContain('Newest Result')
    expect(wrapper.text()).not.toContain('Expired Result')
    expect(wrapper.find('.loading-spinner-stub').exists()).toBe(false)
  })
})

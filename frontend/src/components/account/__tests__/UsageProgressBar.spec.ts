import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import UsageProgressBar from '../UsageProgressBar.vue'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key
    })
  }
})

describe('UsageProgressBar', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-03-17T00:00:00Z'))
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('showNowWhenIdle=true 且利用率为 0 时显示“现在”', () => {
    const wrapper = mount(UsageProgressBar, {
      props: {
        label: '5h',
        utilization: 0,
        resetsAt: '2026-03-17T02:30:00Z',
        showNowWhenIdle: true,
        color: 'indigo'
      }
    })

    expect(wrapper.text()).toContain('usage.resetNow')
    expect(wrapper.text()).not.toContain('2h 30m')
  })

  it('showNowWhenIdle=true 但利用率大于 0 时显示倒计时', () => {
    const wrapper = mount(UsageProgressBar, {
      props: {
        label: '7d',
        utilization: 12,
        resetsAt: '2026-03-17T02:30:00Z',
        showNowWhenIdle: true,
        color: 'emerald'
      }
    })

    expect(wrapper.text()).toContain('2h 30m')
    expect(wrapper.text()).not.toContain('usage.resetNow')
    expect(wrapper.text()).not.toContain('usage.resetPending')
  })

  it('showNowWhenIdle=false 时保持原有倒计时行为', () => {
    const wrapper = mount(UsageProgressBar, {
      props: {
        label: '1d',
        utilization: 0,
        resetsAt: '2026-03-17T02:30:00Z',
        showNowWhenIdle: false,
        color: 'indigo'
      }
    })

    expect(wrapper.text()).toContain('2h 30m')
    expect(wrapper.text()).not.toContain('usage.resetNow')
  })

  it('resetsAt 已过期且利用率大于 0 时显示「待刷新」', () => {
    const wrapper = mount(UsageProgressBar, {
      props: {
        label: '5h',
        utilization: 53,
        // 早于 fake system time 2026-03-17T00:00:00Z
        resetsAt: '2026-03-16T22:00:00Z',
        color: 'indigo'
      }
    })

    expect(wrapper.text()).toContain('usage.resetPending')
    expect(wrapper.text()).not.toContain('usage.resetNow')
  })

  it('resetsAt 已过期且利用率为 0 时仍显示「现在」', () => {
    const wrapper = mount(UsageProgressBar, {
      props: {
        label: '5h',
        utilization: 0,
        resetsAt: '2026-03-16T22:00:00Z',
        color: 'indigo'
      }
    })

    expect(wrapper.text()).toContain('usage.resetNow')
    expect(wrapper.text()).not.toContain('usage.resetPending')
  })

  it('剩余容量模式在 100% 时显示满格绿色', () => {
    const wrapper = mount(UsageProgressBar, {
      props: {
        label: 'Req',
        utilization: 100,
        remainingCapacity: true,
        color: 'indigo'
      }
    })

    expect(wrapper.text()).toContain('100%')
    expect(wrapper.get('.h-1\\.5 > div').attributes('style')).toContain('width: 100%')
    expect(wrapper.get('.h-1\\.5 > div').classes()).toContain('bg-green-500')
  })

  it('剩余容量模式在低量和耗尽时缩短并变红', async () => {
    const wrapper = mount(UsageProgressBar, {
      props: {
        label: 'Req',
        utilization: 15,
        remainingCapacity: true,
        color: 'indigo'
      }
    })

    expect(wrapper.text()).toContain('15%')
    expect(wrapper.get('.h-1\\.5 > div').attributes('style')).toContain('width: 15%')
    expect(wrapper.get('.h-1\\.5 > div').classes()).toContain('bg-red-500')

    await wrapper.setProps({ utilization: 0 })

    expect(wrapper.text()).toContain('0%')
    expect(wrapper.get('.h-1\\.5 > div').attributes('style')).toContain('width: 0%')
    expect(wrapper.get('.h-1\\.5 > div').classes()).toContain('bg-red-500')
  })

  it('默认利用率模式仍把超限显示为满格红色', () => {
    const wrapper = mount(UsageProgressBar, {
      props: {
        label: '5h',
        utilization: 120,
        color: 'indigo'
      }
    })

    expect(wrapper.text()).toContain('120%')
    expect(wrapper.get('.h-1\\.5 > div').attributes('style')).toContain('width: 100%')
    expect(wrapper.get('.h-1\\.5 > div').classes()).toContain('bg-red-500')
  })

  it('所有账号统一显示中文请求、账号计费和可点击的人民币用户扣费', async () => {
    const wrapper = mount(UsageProgressBar, {
      props: {
        label: '5h',
        utilization: 25,
        color: 'indigo',
        windowStats: {
          requests: 2,
          tokens: 300,
          cost: 1.25,
          standard_cost: 2,
          user_cost: 0.75
        }
      }
    })

    expect(wrapper.text()).toContain('2 usage.requestCountUnit')
    expect(wrapper.text()).toContain('usage.accountBilled $1.25')
    expect(wrapper.text()).toContain('usage.userBilled ¥0.75')
    expect(wrapper.text()).not.toContain('A $')
    expect(wrapper.text()).not.toContain('U $')
    expect(wrapper.get('[data-test="account-billing"]').classes()).toContain('text-gray-500')
    expect(wrapper.get('[data-test="user-billing"]').classes()).toContain('text-gray-500')
    await wrapper.get('[data-test="user-billing"]').trigger('click')
    expect(wrapper.emitted('open-billing-details')).toHaveLength(1)
  })

  it('OAuth 保持原统计行，只替换请求、计费标签、币种和颜色', async () => {
    const wrapper = mount(UsageProgressBar, {
      props: {
        label: '5h',
        utilization: 10,
        color: 'indigo',
        oauthBillingDetails: true,
        highlightBilling: true,
        windowStats: {
          requests: 1500,
          tokens: 191_200_000,
          cost: 10,
          standard_cost: 10,
          user_cost: 42.37
        }
      }
    })

    expect(wrapper.text()).toContain('1500 usage.requestCountUnit')
    expect(wrapper.text()).toContain('191.2M')
    expect(wrapper.text()).toContain('usage.accountBilled $10.00')
    expect(wrapper.text()).toContain('usage.userBilled ¥42.37')
    expect(wrapper.text()).not.toContain('A $10.00')
    expect(wrapper.text()).not.toContain('U $42.37')
    expect(wrapper.text()).not.toContain('1.5K req')
    expect(wrapper.get('[title="usage.accountBilled"]').classes()).toContain('text-red-600')
    expect(wrapper.get('[data-test="user-billing"]').classes()).toContain('text-amber-600')

    await wrapper.get('[data-test="user-billing"]').trigger('click')
    expect(wrapper.emitted('open-billing-details')).toHaveLength(1)
  })

  it('无近期计费时保留中文标签、恢复原版灰色且仍可点击', async () => {
    const wrapper = mount(UsageProgressBar, {
      props: {
        label: '7d',
        utilization: 10,
        color: 'emerald',
        oauthBillingDetails: true,
        highlightBilling: false,
        windowStats: {
          requests: 1500,
          tokens: 191_200_000,
          cost: 178.6,
          standard_cost: 178.6,
          user_cost: 42.37
        }
      }
    })

    expect(wrapper.text()).toContain('1500 usage.requestCountUnit')
    expect(wrapper.text()).toContain('usage.accountBilled $178.60')
    expect(wrapper.text()).toContain('usage.userBilled ¥42.37')
    expect(wrapper.get('[data-test="account-billing"]').classes()).toContain('text-gray-500')
    expect(wrapper.get('[data-test="user-billing"]').classes()).toContain('text-gray-500')
    expect(wrapper.get('[data-test="account-billing"]').classes()).not.toContain('text-red-600')
    expect(wrapper.get('[data-test="user-billing"]').classes()).not.toContain('text-amber-600')
    await wrapper.get('[data-test="user-billing"]').trigger('click')
    expect(wrapper.emitted('open-billing-details')).toHaveLength(1)
  })

  it('仅在明确启用时加宽进度条', () => {
    const wideWrapper = mount(UsageProgressBar, {
      props: {
        label: '7d',
        utilization: 25,
        color: 'emerald',
        wide: true
      }
    })
    const defaultWrapper = mount(UsageProgressBar, {
      props: {
        label: '7d',
        utilization: 25,
        color: 'emerald'
      }
    })

    expect(wideWrapper.get('.h-1\\.5').classes()).toContain('w-16')
    expect(defaultWrapper.get('.h-1\\.5').classes()).toContain('w-8')
  })

  it('加宽模式保持统计行、进度行和操作行一致间距并按实际百分比填充', () => {
    const wrapper = mount(UsageProgressBar, {
      props: {
        label: '7d',
        utilization: 6,
        color: 'emerald',
        wide: true,
        windowStats: {
          requests: 1500,
          tokens: 16_100_000,
          cost: 178.6,
          standard_cost: 178.6,
          user_cost: 42.37
        }
      }
    })

    expect(wrapper.get('.mb-1').exists()).toBe(true)
    expect(wrapper.text()).toContain('6%')
    expect(wrapper.get('.h-1\\.5 > div').attributes('style')).toContain('width: 6%')
  })

})

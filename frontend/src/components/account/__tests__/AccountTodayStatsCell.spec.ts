import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'

import AccountTodayStatsCell from '../AccountTodayStatsCell.vue'

const { formatCurrency } = vi.hoisted(() => ({
  formatCurrency: vi.fn((value: number, currency: string) =>
    `${currency === 'CNY' ? '¥' : '$'}${value.toFixed(2)} ${currency}`
  )
}))

vi.mock('@/utils/format', () => ({
  formatNumber: (value: number) => value.toLocaleString(),
  formatCurrency
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

describe('AccountTodayStatsCell', () => {
  it('formats account usage in USD and user deductions in RMB', () => {
    const wrapper = mount(AccountTodayStatsCell, {
      props: {
        stats: {
          requests: 12,
          tokens: 3456,
          cost: 1.25,
          user_cost: 0.75
        }
      }
    })

    expect(formatCurrency).toHaveBeenCalledWith(1.25, 'USD')
    expect(formatCurrency).toHaveBeenCalledWith(0.75, 'CNY')
    expect(wrapper.text()).toContain('$1.25 USD')
    expect(wrapper.text()).toContain('¥0.75 CNY')
  })
})

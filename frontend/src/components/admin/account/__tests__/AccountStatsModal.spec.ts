import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import AccountStatsModal from '../AccountStatsModal.vue'

const { getStats } = vi.hoisted(() => ({
  getStats: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      getStats
    }
  }
}))

vi.mock('chart.js', () => ({
  Chart: { register: vi.fn() },
  CategoryScale: {},
  LinearScale: {},
  PointElement: {},
  LineElement: {},
  Title: {},
  Tooltip: {},
  Legend: {},
  Filler: {}
}))

vi.mock('vue-chartjs', () => ({
  Line: {
    props: ['data', 'options'],
    template: '<div class="line-chart-stub">{{ JSON.stringify(data) }}</div>'
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

const stats = {
  history: [
    {
      date: '2026-08-11',
      label: '08-11',
      requests: 3,
      tokens: 400,
      cost: 20,
      actual_cost: 12.34,
      user_cost: 9.87
    }
  ],
  summary: {
    days: 30,
    actual_days_used: 1,
    total_cost: 12.34,
    total_user_cost: 9.87,
    total_standard_cost: 20,
    total_requests: 3,
    total_tokens: 400,
    avg_daily_cost: 12.34,
    avg_daily_user_cost: 9.87,
    avg_daily_requests: 3,
    avg_daily_tokens: 400,
    avg_duration_ms: 250,
    today: {
      date: '2026-08-11',
      cost: 1.25,
      user_cost: 0.75,
      requests: 3,
      tokens: 400
    },
    highest_cost_day: {
      date: '2026-08-11',
      label: '08-11',
      cost: 1.25,
      user_cost: 0.75,
      requests: 3
    },
    highest_request_day: {
      date: '2026-08-11',
      label: '08-11',
      requests: 3,
      cost: 1.25,
      user_cost: 0.75
    }
  },
  models: [],
  endpoints: [],
  upstream_endpoints: []
}

describe('AccountStatsModal', () => {
  beforeEach(() => {
    getStats.mockReset()
    getStats.mockResolvedValue(stats)
  })

  it('renders usage costs and chart cost labels in USD', async () => {
    const wrapper = mount(AccountStatsModal, {
      props: {
        show: false,
        account: {
          id: 42,
          name: 'Usage Account',
          status: 'active'
        } as any
      },
      global: {
        stubs: {
          BaseDialog: {
            props: ['show'],
            template: '<div v-if="show"><slot /><slot name="footer" /></div>'
          },
          LoadingSpinner: true,
          ModelDistributionChart: true,
          EndpointDistributionChart: true,
          Icon: true
        }
      }
    })

    await wrapper.setProps({ show: true })
    await flushPromises()

    expect(getStats).toHaveBeenCalledWith(42, 30)
    expect(wrapper.text()).toContain('$12.34')
    expect(wrapper.text()).toContain('$9.87')
    expect(wrapper.text()).toContain('$20.00')
    expect(wrapper.text()).not.toContain('¥')

    const options = (wrapper.vm as any).$?.setupState.lineChartOptions
    expect(options.plugins.tooltip.callbacks.label({
      dataset: { label: 'usage.accountBilled (USD)' },
      raw: 0.125
    })).toBe('usage.accountBilled (USD): $0.125')
    expect(options.scales.y.ticks.callback(0.125)).toBe('$0.125')
  })
})

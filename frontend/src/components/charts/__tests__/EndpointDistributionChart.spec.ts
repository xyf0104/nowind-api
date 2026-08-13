import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'

import EndpointDistributionChart from '../EndpointDistributionChart.vue'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key
    })
  }
})

vi.mock('vue-chartjs', () => ({
  Doughnut: {
    props: ['data'],
    template: '<div class="chart-data">{{ JSON.stringify(data) }}</div>'
  }
}))

describe('EndpointDistributionChart', () => {
  it('renders user deductions in RMB and standard costs in USD', () => {
    const wrapper = mount(EndpointDistributionChart, {
      props: {
        endpointStats: [
          {
            endpoint: '/v1/messages',
            requests: 2,
            total_tokens: 300,
            cost: 0.5,
            actual_cost: 0.25
          }
        ],
        metric: 'actual_cost',
        enableBreakdown: false
      },
      global: {
        stubs: {
          LoadingSpinner: true,
          UserBreakdownSubTable: true
        }
      }
    })

    const row = wrapper.get('tbody tr')
    expect(row.text()).toContain('¥0.250')
    expect(row.text()).toContain('$0.500')

    const options = (wrapper.vm as any).$?.setupState.doughnutOptions
    expect(options.plugins.tooltip.callbacks.label({
      label: '/v1/messages',
      raw: 0.25,
      dataset: { data: [0.25] }
    })).toBe('/v1/messages: ¥0.250 (100.0%)')
  })
})

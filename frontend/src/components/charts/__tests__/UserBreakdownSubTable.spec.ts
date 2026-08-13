import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'

import UserBreakdownSubTable from '../UserBreakdownSubTable.vue'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key
    })
  }
})

describe('UserBreakdownSubTable', () => {
  it('renders user deduction in RMB and account/standard usage costs in USD', () => {
    const wrapper = mount(UserBreakdownSubTable, {
      props: {
        items: [
          {
            user_id: 7,
            email: 'user@example.com',
            requests: 2,
            input_tokens: 100,
            output_tokens: 50,
            cache_tokens: 0,
            total_tokens: 150,
            cost: 0.5,
            actual_cost: 0.25,
            account_cost: 0.375
          }
        ]
      },
      global: {
        stubs: {
          LoadingSpinner: true
        }
      }
    })

    const row = wrapper.get('tbody tr')
    expect(row.text()).toContain('¥0.250')
    expect(row.text()).toContain('$0.375')
    expect(row.text()).toContain('$0.500')
  })
})

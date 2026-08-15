import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import SMSRechargeDialog from '../SMSRechargeDialog.vue'

const { getCheckoutInfo } = vi.hoisted(() => ({
  getCheckoutInfo: vi.fn()
}))

vi.mock('@/api/payment', () => ({
  paymentAPI: {
    getCheckoutInfo,
    createOrder: vi.fn()
  }
}))

vi.mock('@/stores', () => ({
  useAuthStore: () => ({ refreshUser: vi.fn() }),
  useAppStore: () => ({ showError: vi.fn(), showSuccess: vi.fn() })
}))

vi.mock('vue-router', () => ({
  useRouter: () => ({ resolve: vi.fn(() => ({ href: '/payment' })) })
}))

vi.mock('@/utils/device', () => ({ isMobileDevice: () => false }))

describe('SMSRechargeDialog', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    getCheckoutInfo.mockResolvedValue({
      data: {
        methods: {
          alipay: {
            display_name: '支付宝',
            fee_rate: 0,
            available: true,
            single_min: 0,
            single_max: 0
          }
        },
        global_min: 20,
        global_max: 50,
        plans: [],
        balance_disabled: false,
        balance_recharge_multiplier: 1,
        subscription_usd_to_cny_rate: 0,
        recharge_fee_rate: 0,
        help_text: '',
        help_image_url: '',
        stripe_publishable_key: ''
      }
    })
  })

  it('disables fixed tiers outside the configured global checkout range', async () => {
    const wrapper = mount(SMSRechargeDialog, {
      props: { open: false },
      global: {
        stubs: {
          Icon: true,
          PaymentMethodSelector: true,
          PaymentStatusPanel: true
        }
      },
      attachTo: document.body
    })
    await wrapper.setProps({ open: true })
    await flushPromises()

    const tier = (label: string) => Array.from(document.body.querySelectorAll('button'))
      .find((button) => button.textContent?.trim() === label)
    expect(tier('¥10')).toBeInstanceOf(HTMLButtonElement)
    expect(tier('¥10')?.hasAttribute('disabled')).toBe(true)
    expect(tier('¥20')?.hasAttribute('disabled')).toBe(false)
    expect(tier('¥50')?.hasAttribute('disabled')).toBe(false)
    expect(tier('¥100')?.hasAttribute('disabled')).toBe(true)

    wrapper.unmount()
  })
})

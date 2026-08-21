import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import PricingAreaTabs from '../PricingAreaTabs.vue'

const isFeatureFlagEnabled = vi.hoisted(() => vi.fn())

vi.mock('@/utils/featureFlags', () => ({
  FeatureFlags: {
    channelMonitor: { key: 'channel_monitor_enabled' },
    modelPricing: { key: 'model_pricing_enabled' },
  },
  isFeatureFlagEnabled,
}))

vi.mock('vue-i18n', () => ({
  useI18n: () => {
    const labels: Record<string, string> = {
      'pricingAreaTabs.label': 'Model pricing and channel monitor',
      'pricingAreaTabs.pricing': 'Model Pricing',
      'pricingAreaTabs.monitor': 'Channel Monitor',
    }
    return {
      t: (key: string) => labels[key] ?? key,
    }
  },
}))

const RouterLinkStub = {
  props: ['to'],
  template: '<a :href="to"><slot /></a>',
}

function mountTabs(activeTab: 'pricing' | 'monitor') {
  return mount(PricingAreaTabs, {
    props: { activeTab },
    global: {
      components: { RouterLink: RouterLinkStub },
    },
  })
}

describe('PricingAreaTabs', () => {
  beforeEach(() => {
    isFeatureFlagEnabled.mockReset().mockReturnValue(true)
  })

  it('keeps model pricing and customer channel monitoring under one primary area', () => {
    const wrapper = mountTabs('monitor')

    expect(wrapper.get('[data-testid="pricing-area-tab-pricing"]').attributes('href')).toBe('/pricing')
    expect(wrapper.get('[data-testid="pricing-area-tab-monitor"]').attributes('href')).toBe('/pricing/monitor')
    expect(wrapper.get('[data-testid="pricing-area-tab-monitor"]').attributes('aria-current')).toBe('page')
    expect(wrapper.get('[data-testid="pricing-area-tab-pricing"]').attributes('aria-current')).toBeUndefined()
  })

  it('hides the monitoring tab when the public channel-monitor flag is explicitly disabled', () => {
    isFeatureFlagEnabled.mockImplementation((flag: { key: string }) => flag.key !== 'channel_monitor_enabled')

    const wrapper = mountTabs('pricing')

    expect(wrapper.find('[data-testid="pricing-area-tab-pricing"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="pricing-area-tab-monitor"]').exists()).toBe(false)
  })

  it('hides the pricing tab when the public model-pricing flag is explicitly disabled', () => {
    isFeatureFlagEnabled.mockImplementation((flag: { key: string }) => flag.key !== 'model_pricing_enabled')

    const wrapper = mountTabs('monitor')

    expect(wrapper.find('[data-testid="pricing-area-tab-pricing"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="pricing-area-tab-monitor"]').exists()).toBe(true)
  })

  it('renders no pricing-area tab when both features are disabled', () => {
    isFeatureFlagEnabled.mockReturnValue(false)

    const wrapper = mountTabs('pricing')

    expect(wrapper.find('[data-testid="pricing-area-tab-pricing"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="pricing-area-tab-monitor"]').exists()).toBe(false)
  })
})

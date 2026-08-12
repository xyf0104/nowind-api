import { describe, expect, it } from 'vitest'
import { buildTopupTiers, calculateTopupCreditedAmount, topupBonusForAmount } from './pricingTiers'

const defaultRules = [
  { threshold: 50, bonus: 2.99 },
  { threshold: 100, bonus: 8 },
  { threshold: 200, bonus: 18 },
  { threshold: 500, bonus: 50 },
]

describe('top-up bonus tiers', () => {
  it.each([
    [49.99, 0],
    [50, 2.99],
    [50.5, 2.99],
    [99.99, 2.99],
    [100, 8],
    [199.99, 8],
    [200, 18],
    [499.99, 18],
    [500, 50],
    [1000, 50],
  ])('returns the expected bonus for %s', (amount, bonus) => {
    expect(topupBonusForAmount(amount, true, defaultRules)).toBe(bonus)
  })

  it('adds the bonus after applying the recharge multiplier', () => {
    expect(calculateTopupCreditedAmount(50.5, 1, true, defaultRules)).toBe(53.49)
    expect(calculateTopupCreditedAmount(50, 0.14, true, defaultRules)).toBe(9.99)
  })

  it('builds the configured preset amounts', () => {
    expect(buildTopupTiers(defaultRules, true).map(({ priceRMB, bonusUSD }) => [priceRMB, bonusUSD])).toEqual([
      [50, 2.99],
      [100, 8],
      [200, 18],
      [500, 50],
    ])
  })

  it('uses the recharge multiplier when building preset amounts', () => {
    const [tier] = buildTopupTiers([{ threshold: 50, bonus: 2.99 }], true, 0.14)

    expect(tier.creditUSD).toBe(7)
    expect(tier.bonusUSD).toBe(2.99)
    expect(tier.locales.zh.features).toContain('获得 ¥9.99 额度')
  })

  it('does not add a bonus when the campaign is disabled', () => {
    expect(topupBonusForAmount(500, false, defaultRules)).toBe(0)
    expect(calculateTopupCreditedAmount(500, 1, false, defaultRules)).toBe(500)
    expect(buildTopupTiers(defaultRules, false).every((tier) => tier.bonusUSD === 0)).toBe(true)
  })
})

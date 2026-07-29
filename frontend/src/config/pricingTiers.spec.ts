import { describe, expect, it } from 'vitest'
import { TOPUP_TIERS, calculateTopupCreditedAmount, topupBonusForAmount } from './pricingTiers'

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
    expect(topupBonusForAmount(amount)).toBe(bonus)
  })

  it('adds the bonus after applying the recharge multiplier', () => {
    expect(calculateTopupCreditedAmount(50.5)).toBe(53.49)
    expect(calculateTopupCreditedAmount(50, 0.14)).toBe(9.99)
  })

  it('contains only the four configured preset amounts', () => {
    expect(TOPUP_TIERS.map(({ priceRMB, bonusUSD }) => [priceRMB, bonusUSD])).toEqual([
      [50, 2.99],
      [100, 8],
      [200, 18],
      [500, 50],
    ])
  })
})

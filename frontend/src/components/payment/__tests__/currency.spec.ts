import { describe, expect, it } from 'vitest'
import { currencySymbol, formatPaymentAmount, paymentCurrencyName } from '../currency'

describe('formatPaymentAmount', () => {
  it('uses the currency default fraction digits', () => {
    expect(formatPaymentAmount(100, 'JPY', 'en-US')).not.toContain('.00')
    expect(formatPaymentAmount(100, 'KRW', 'en-US')).not.toContain('.00')
    expect(formatPaymentAmount(100, 'HKD', 'en-US')).toContain('.00')
  })
})

describe('currencySymbol', () => {
  it('maps common payment currencies and falls back safely', () => {
    expect(currencySymbol('USD')).toBe('$')
    expect(currencySymbol('cny')).toBe('¥')
    expect(currencySymbol('EUR')).toBe('€')
    expect(currencySymbol('')).toBe('¥')
    expect(currencySymbol('XYZ')).toBe('XYZ')
  })
})

describe('paymentCurrencyName', () => {
  it('uses Chinese currency names for the Chinese interface', () => {
    expect(paymentCurrencyName('CNY', 'zh-CN')).toBe('人民币')
    expect(paymentCurrencyName('USD', 'zh-CN')).toBe('美元')
    expect(paymentCurrencyName('HKD', 'zh-CN')).toBe('港币')
    expect(paymentCurrencyName('XYZ', 'zh-CN')).toBe('XYZ')
  })
})

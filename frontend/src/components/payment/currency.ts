export const DEFAULT_PAYMENT_CURRENCY = 'CNY'

const PAYMENT_CURRENCY_SYMBOLS: Record<string, string> = {
  USD: '$',
  CNY: '¥',
  RMB: '¥',
  EUR: '€',
  GBP: '£',
  JPY: '¥',
  HKD: 'HK$',
  TWD: 'NT$',
  KRW: '₩',
  AUD: 'A$',
  CAD: 'C$',
  SGD: 'S$',
  NZD: 'NZ$',
  MOP: 'MOP$',
  MYR: 'RM',
  THB: '฿',
  PHP: '₱',
  INR: '₹',
}

const PAYMENT_CURRENCY_ZH_NAMES: Record<string, string> = {
  USD: '美元',
  CNY: '人民币',
  RMB: '人民币',
  EUR: '欧元',
  GBP: '英镑',
  JPY: '日元',
  HKD: '港币',
  TWD: '新台币',
  KRW: '韩元',
  AUD: '澳元',
  CAD: '加元',
  SGD: '新加坡元',
  NZD: '新西兰元',
  MOP: '澳门元',
  MYR: '马来西亚林吉特',
  THB: '泰铢',
  PHP: '菲律宾比索',
  INR: '印度卢比',
}

const PAYMENT_CURRENCY_EN_NAMES: Record<string, string> = {
  USD: 'US dollar',
  CNY: 'Chinese yuan',
  RMB: 'Chinese yuan',
  EUR: 'euro',
  GBP: 'British pound',
  JPY: 'Japanese yen',
  HKD: 'Hong Kong dollar',
  TWD: 'New Taiwan dollar',
  KRW: 'South Korean won',
  AUD: 'Australian dollar',
  CAD: 'Canadian dollar',
  SGD: 'Singapore dollar',
  NZD: 'New Zealand dollar',
  MOP: 'Macanese pataca',
  MYR: 'Malaysian ringgit',
  THB: 'Thai baht',
  PHP: 'Philippine peso',
  INR: 'Indian rupee',
}

export function normalizePaymentCurrency(currency?: string | null): string {
  const normalized = String(currency || '').trim().toUpperCase()
  return /^[A-Z]{3}$/.test(normalized) ? normalized : DEFAULT_PAYMENT_CURRENCY
}

export function currencySymbol(currency?: string | null): string {
  const normalized = normalizePaymentCurrency(currency)
  return PAYMENT_CURRENCY_SYMBOLS[normalized] || normalized
}

export function paymentCurrencyName(currency?: string | null, locale?: string): string {
  const normalized = normalizePaymentCurrency(currency)
  const normalizedLocale = String(locale || '').toLowerCase()

  if (normalizedLocale.startsWith('zh')) {
    return PAYMENT_CURRENCY_ZH_NAMES[normalized] || normalized
  }

  return PAYMENT_CURRENCY_EN_NAMES[normalized] || normalized
}

function paymentCurrencyFractionDigits(currency: string): number {
  try {
    return new Intl.NumberFormat(undefined, {
      style: 'currency',
      currency,
    }).resolvedOptions().maximumFractionDigits ?? 2
  } catch {
    return 2
  }
}

export function formatPaymentAmount(amount: number, currency?: string | null, locale?: string): string {
  const normalized = normalizePaymentCurrency(currency)
  const fractionDigits = paymentCurrencyFractionDigits(normalized)
  try {
    return new Intl.NumberFormat(locale || undefined, {
      style: 'currency',
      currency: normalized,
      currencyDisplay: 'narrowSymbol',
      minimumFractionDigits: fractionDigits,
      maximumFractionDigits: fractionDigits,
    }).format(Number.isFinite(amount) ? amount : 0)
  } catch {
    return `${normalized} ${(Number.isFinite(amount) ? amount : 0).toFixed(fractionDigits)}`
  }
}

export interface RechargeBonusRule {
  threshold: number;
  bonus: number;
}

export interface PricingTier {
  id: string;
  priceRMB: number;
  creditUSD: number;
  bonusUSD: number;
  tagColor?: string;
  buttonClass?: string;
  locales: {
    [key: string]: {
      title: string;
      subtitle: string;
      tag?: string;
      features: string[];
    };
  };
}

export function topupBonusForAmount(amount: number, enabled = true, rules: RechargeBonusRule[] = []): number {
  if (!enabled || !Number.isFinite(amount)) return 0;
  let bonus = 0;
  let highestThreshold = -1;
  for (const rule of rules) {
    if (!Number.isFinite(rule.threshold) || !Number.isFinite(rule.bonus)) continue;
    if (amount >= rule.threshold && rule.threshold >= highestThreshold) {
      highestThreshold = rule.threshold;
      bonus = rule.bonus;
    }
  }
  return Math.round(bonus * 100) / 100;
}

export function calculateTopupCreditedAmount(
  amount: number,
  multiplier = 1,
  bonusEnabled = true,
  bonusRules: RechargeBonusRule[] = [],
): number {
  if (!Number.isFinite(amount)) return 0;
  const normalizedMultiplier = Number.isFinite(multiplier) && multiplier > 0 ? multiplier : 1;
  const baseAmount = Math.round(amount * normalizedMultiplier * 100) / 100;
  return Math.round((baseAmount + topupBonusForAmount(amount, bonusEnabled, bonusRules)) * 100) / 100;
}

export function buildTopupTiers(
  rules: RechargeBonusRule[],
  bonusEnabled: boolean,
  multiplier = 1,
): PricingTier[] {
  return [...rules]
    .sort((a, b) => a.threshold - b.threshold)
    .map((rule, index) => {
      const creditedAmount = calculateTopupCreditedAmount(
        rule.threshold,
        multiplier,
        bonusEnabled,
        rules,
      );
      const baseAmount = Math.round(rule.threshold * (Number.isFinite(multiplier) && multiplier > 0 ? multiplier : 1) * 100) / 100;
      return {
        id: `recharge_${rule.threshold}`,
        priceRMB: rule.threshold,
        creditUSD: baseAmount,
        bonusUSD: bonusEnabled ? rule.bonus : 0,
        locales: {
          zh: {
            title: ['体验', '标准', '进阶', '专业'][index] || `充值 ${index + 1}`,
            subtitle: bonusEnabled && rule.bonus > 0 ? `充值满 ¥${rule.threshold.toFixed(2)}，赠送 ¥${rule.bonus.toFixed(2)}` : '按实付金额到账',
            tag: index === rules.length - 1 && bonusEnabled && rule.bonus > 0 ? '更多赠送' : undefined,
            features: [`获得 ¥${creditedAmount.toFixed(2)} 额度`, '永不过期', '支持全部模型'],
          },
          en: {
            title: ['Trial', 'Standard', 'Advanced', 'Professional'][index] || `Top-up ${index + 1}`,
            subtitle: bonusEnabled && rule.bonus > 0 ? `Top up ¥${rule.threshold.toFixed(2)}, receive ¥${rule.bonus.toFixed(2)}` : 'Credit equals the paid amount',
            tag: index === rules.length - 1 && bonusEnabled && rule.bonus > 0 ? 'More bonus' : undefined,
            features: [`Get ¥${creditedAmount.toFixed(2)} credit`, 'Never expires', 'All models supported'],
          },
        },
      };
    });
}

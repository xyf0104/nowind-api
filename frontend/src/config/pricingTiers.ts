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

export function topupBonusForAmount(amount: number): number {
  if (!Number.isFinite(amount) || amount < 50) return 0;
  if (amount < 100) return 2.99;
  if (amount < 200) return 8;
  if (amount < 500) return 18;
  return 50;
}

export function calculateTopupCreditedAmount(amount: number, multiplier = 1): number {
  if (!Number.isFinite(amount)) return 0;
  const normalizedMultiplier = Number.isFinite(multiplier) && multiplier > 0 ? multiplier : 1;
  const baseAmount = Math.round(amount * normalizedMultiplier * 100) / 100;
  return Math.round((baseAmount + topupBonusForAmount(amount)) * 100) / 100;
}

export const TOPUP_TIERS: PricingTier[] = [
  {
    id: "plan_1",
    priceRMB: 50,
    creditUSD: 50,
    bonusUSD: 2.99,
    locales: {
      zh: {
        title: "体验",
        subtitle: "适合初次体验",
        features: ["获得 ¥52.99 额度", "永不过期", "支持全部模型"],
      },
      en: {
        title: "Trial",
        subtitle: "For first-time users",
        features: ["Get ¥52.99 credit", "Never expires", "All models supported"],
      }
    }
  },
  {
    id: "plan_2",
    priceRMB: 100,
    creditUSD: 100,
    bonusUSD: 8,
    locales: {
      zh: {
        title: "标准",
        subtitle: "开发者常用",
        features: ["获得 ¥108.00 额度", "永不过期", "支持全部模型"],
      },
      en: {
        title: "Standard",
        subtitle: "For regular developers",
        features: ["Get ¥108.00 credit", "Never expires", "All models supported"],
      }
    }
  },
  {
    id: "plan_3",
    priceRMB: 200,
    creditUSD: 200,
    bonusUSD: 18,
    locales: {
      zh: {
        title: "进阶",
        subtitle: "高频使用",
        tag: "人气推荐",
        features: ["获得 ¥218.00 额度", "永不过期", "支持全部模型"],
      },
      en: {
        title: "Advanced",
        subtitle: "For frequent users",
        tag: "Popular",
        features: ["Get ¥218.00 credit", "Never expires", "All models supported"],
      }
    }
  },
  {
    id: "plan_4",
    priceRMB: 500,
    creditUSD: 500,
    bonusUSD: 50,
    tagColor: "bg-orange-600",
    buttonClass: "bg-orange-600 hover:bg-orange-700 text-white shadow-orange-500/30",
    locales: {
      zh: {
        title: "专业",
        subtitle: "专业开发者",
        tag: "最佳价值",
        features: ["获得 ¥550.00 额度", "永不过期", "支持全部模型"],
      },
      en: {
        title: "Professional",
        subtitle: "For pro developers",
        tag: "Best Value",
        features: ["Get ¥550.00 credit", "Never expires", "All models supported"],
      }
    }
  },
];

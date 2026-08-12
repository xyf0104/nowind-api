package service

import "testing"

func TestCalculateBalanceRechargeBonusByPaidAmount(t *testing.T) {
	t.Parallel()
	rules := defaultRechargeBonusRules()

	tests := []struct {
		name   string
		amount float64
		want   float64
	}{
		{name: "below first tier", amount: 49.99, want: 0},
		{name: "first tier", amount: 50, want: 2.99},
		{name: "inside first tier", amount: 50.5, want: 2.99},
		{name: "below second tier", amount: 99.99, want: 2.99},
		{name: "second tier", amount: 100, want: 8},
		{name: "below third tier", amount: 199.99, want: 8},
		{name: "third tier", amount: 200, want: 18},
		{name: "below fourth tier", amount: 499.99, want: 18},
		{name: "fourth tier", amount: 500, want: 50},
		{name: "above fourth tier", amount: 1000, want: 50},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := calculateBalanceRechargeBonus(tt.amount, true, rules); got != tt.want {
				t.Fatalf("calculateBalanceRechargeBonus(%v) = %v, want %v", tt.amount, got, tt.want)
			}
		})
	}
}

func TestCalculateCreditedBalanceUsesCampaignSnapshot(t *testing.T) {
	// The credit amount is stored on the pending order at checkout time. This
	// locks the campaign result even if an administrator changes the campaign
	// before the provider callback arrives.
	originalRules := defaultRechargeBonusRules()
	creditedAtCheckout := calculateCreditedBalance(50, 1, true, originalRules)
	reconfiguredRules := []RechargeBonusRule{{Threshold: 50, Bonus: 20}}

	if creditedAtCheckout != 52.99 {
		t.Fatalf("credited amount at checkout = %v, want 52.99", creditedAtCheckout)
	}
	if calculateCreditedBalance(50, 1, true, reconfiguredRules) != 70 {
		t.Fatal("sanity check: later rules should affect only newly-created orders")
	}
	if calculateCreditedBalance(50, 1, false, originalRules) != 50 {
		t.Fatal("sanity check: a disabled campaign should affect only newly-created orders")
	}
}

func TestCalculateBalanceRechargeBonusUsesHighestEligibleThreshold(t *testing.T) {
	t.Parallel()

	rules := []RechargeBonusRule{
		{Threshold: 200, Bonus: 18},
		{Threshold: 50, Bonus: 2.99},
		{Threshold: 100, Bonus: 8},
	}

	if got := calculateBalanceRechargeBonus(150, true, rules); got != 8 {
		t.Fatalf("calculateBalanceRechargeBonus with unsorted rules = %v, want 8", got)
	}
}

func TestCalculateCreditedBalanceAddsBonusAfterMultiplier(t *testing.T) {
	t.Parallel()
	rules := defaultRechargeBonusRules()

	tests := []struct {
		name       string
		amount     float64
		multiplier float64
		want       float64
	}{
		{name: "custom decimal amount", amount: 50.5, multiplier: 1, want: 53.49},
		{name: "first preset", amount: 50, multiplier: 1, want: 52.99},
		{name: "second preset", amount: 100, multiplier: 1, want: 108},
		{name: "third preset", amount: 200, multiplier: 1, want: 218},
		{name: "fourth preset", amount: 500, multiplier: 1, want: 550},
		{name: "multiplier before bonus", amount: 50, multiplier: 0.14, want: 9.99},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := calculateCreditedBalance(tt.amount, tt.multiplier, true, rules); got != tt.want {
				t.Fatalf("calculateCreditedBalance(%v, %v) = %v, want %v", tt.amount, tt.multiplier, got, tt.want)
			}
		})
	}
}

func TestCalculateCreditedBalanceDisablesBonusCampaign(t *testing.T) {
	t.Parallel()

	if got := calculateCreditedBalance(500, 1, false, defaultRechargeBonusRules()); got != 500 {
		t.Fatalf("calculateCreditedBalance(500, 1, disabled) = %v, want 500", got)
	}
}

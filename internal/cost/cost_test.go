package cost

import (
	"math"
	"testing"
)

func TestCalculate_BasicSum(t *testing.T) {
	t.Parallel()

	p := Pricing{
		InputPerMTok:      3.0,
		OutputPerMTok:     15.0,
		CacheReadPerMTok:  0.3,
		CacheWritePerMTok: 3.75,
	}
	u := Usage{
		InputTokens:      1_000_000,
		OutputTokens:     500_000,
		CacheReadTokens:  2_000_000,
		CacheWriteTokens: 100_000,
	}
	// 1M*3 + 0.5M*15 + 2M*0.3 + 0.1M*3.75
	// = 3 + 7.5 + 0.6 + 0.375 = 11.475
	got := Calculate(u, p)
	want := 11.475
	if math.Abs(got.USD-want) > 1e-9 {
		t.Fatalf("Calculate.USD = %.9f, want %.9f", got.USD, want)
	}
}

func TestCalculate_ZeroUsage(t *testing.T) {
	t.Parallel()

	got := Calculate(Usage{}, Pricing{InputPerMTok: 3, OutputPerMTok: 15})
	if got.USD != 0 {
		t.Fatalf("Calculate(zero, _).USD = %v, want 0", got.USD)
	}
}

func TestCalculate_ZeroPricing(t *testing.T) {
	t.Parallel()

	got := Calculate(Usage{InputTokens: 1_000_000, OutputTokens: 999}, Pricing{})
	if got.USD != 0 {
		t.Fatalf("Calculate(_, zero).USD = %v, want 0", got.USD)
	}
}

func TestCalculate_NegativeTokensClampedToZero(t *testing.T) {
	t.Parallel()

	// A misbehaving provider reports negative tokens; we should ignore
	// rather than produce a negative cost.
	got := Calculate(Usage{InputTokens: -1_000_000, OutputTokens: 1_000_000}, Pricing{InputPerMTok: 3, OutputPerMTok: 15})
	if math.Abs(got.USD-15.0) > 1e-9 {
		t.Fatalf("Calculate(neg in).USD = %v, want 15.0", got.USD)
	}
}

func TestMoney_Format(t *testing.T) {
	t.Parallel()

	cases := []struct {
		usd  float64
		want string
	}{
		{0, "$0.0000"},
		{0.123456, "$0.1235"},
		{1234.5, "$1234.5000"},
		{0.00004, "$0.0000"},  // rounds down to 0
		{0.00005, "$0.0001"},  // rounds half-up
		{-0.5, "-$0.5000"},    // negative
		{math.Inf(1), "$Inf"}, // sentinel
		{math.Inf(-1), "-$Inf"},
	}
	for _, c := range cases {
		got := Money{USD: c.usd}.Format()
		if got != c.want {
			t.Errorf("Money{%v}.Format() = %q, want %q", c.usd, got, c.want)
		}
	}

	// NaN is handled separately because reflect comparison of NaN is fiddly.
	if got := (Money{USD: math.NaN()}).Format(); got != "$NaN" {
		t.Errorf("Money{NaN}.Format() = %q, want %q", got, "$NaN")
	}
}

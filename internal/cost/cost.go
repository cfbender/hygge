// Package cost provides token-accounting and dollar-cost computation for
// model usage.  It owns three concerns:
//
//   - A pure [Calculate] function that turns a [Usage] (token counts) into a
//     [Money] given a [Pricing] (rates per million tokens).
//   - A [Catalog] that resolves model pricing through a cascade of sources:
//     in-memory cache -> on-disk JSON cache -> live HTTP fetch from
//     models.dev -> hard-coded fallback table.
//   - A small set of typed errors so callers can distinguish "model is not
//     priced anywhere" from transport or parse failures.
//
// # Units
//
// Every rate in [Pricing] is dollars per ONE MILLION tokens.  This matches
// the convention models.dev uses in its public catalog and avoids the
// per-1k-vs-per-1M confusion that plagues pricing code.  Convert at the API
// boundary — never store mixed units.
//
// # No live network in tests
//
// Tests under this package MUST NOT make real HTTP calls.  All network paths
// are exercised against [net/http/httptest] servers.  The real models.dev
// URL is only contacted from production code paths, and even then the
// catalog falls back gracefully on failure (see [Catalog.LookUp]).
//
// # Resilience
//
// The catalog is designed to never panic and never return a hard error for
// transient network issues alone.  See the resolution table in
// [Catalog.LookUp]'s doc comment for the full degradation policy.
package cost

import (
	"fmt"
	"math"
	"time"
)

// Pricing describes per-token rates for one model.  All rates are USD per
// one million tokens.  Zero values are legal and mean "this model does not
// charge for that token class" — for example, a model without prompt caching
// has CacheReadPerMTok = CacheWritePerMTok = 0.
type Pricing struct {
	// Provider is the catalog-level provider id (e.g. "anthropic").
	Provider string

	// Model is the model id as the provider exposes it (e.g.
	// "claude-sonnet-4-5").  When the live models.dev catalog uses a
	// different spelling (e.g. "claude-sonnet-4-5"), the catalog still
	// returns Pricing.Model spelled the way the caller asked for.
	Model string

	// InputPerMTok is the dollar cost per million input tokens.
	InputPerMTok float64

	// OutputPerMTok is the dollar cost per million output tokens.
	OutputPerMTok float64

	// CacheReadPerMTok is the dollar cost per million tokens read from the
	// prompt cache.  Zero if the model does not support caching.
	CacheReadPerMTok float64

	// CacheWritePerMTok is the dollar cost per million tokens written to
	// the prompt cache.  Zero if the model does not support caching.
	CacheWritePerMTok float64

	// UpdatedAt is when this Pricing entry was last refreshed.  For
	// fallback entries this is the zero time.
	UpdatedAt time.Time
}

// Usage is the token-count payload from a provider response.  All four
// fields are independent; do not assume any sum/equality relationship
// between them.
type Usage struct {
	InputTokens      int64
	OutputTokens     int64
	CacheReadTokens  int64
	CacheWriteTokens int64
}

// Money is a dollar amount with explicit precision semantics.  Use [Money.Format]
// for display and the embedded float for arithmetic.  Money values are
// produced by [Calculate]; do not construct them by hand outside of tests.
type Money struct {
	USD float64
}

// Calculate computes the cost for u given p.  It is a pure function:
// idempotent, free of side effects, safe to call concurrently.
//
// The formula is the obvious sum, scaled by 1e6 because rates are per
// million tokens:
//
//	cost = (input*input_rate + output*output_rate
//	      + cache_read*cache_read_rate + cache_write*cache_write_rate) / 1_000_000
//
// Negative token counts are clamped to zero — providers do not report
// negative usage, and treating an obvious data bug as a negative refund
// would be worse than ignoring it.
func Calculate(u Usage, p Pricing) Money {
	const perMillion = 1_000_000.0

	in := nonNeg(u.InputTokens)
	out := nonNeg(u.OutputTokens)
	cr := nonNeg(u.CacheReadTokens)
	cw := nonNeg(u.CacheWriteTokens)

	dollars := (float64(in)*p.InputPerMTok +
		float64(out)*p.OutputPerMTok +
		float64(cr)*p.CacheReadPerMTok +
		float64(cw)*p.CacheWritePerMTok) / perMillion

	return Money{USD: dollars}
}

// Format renders m as a string like "$0.0123" with four decimal places.
// Values are rounded to 4 decimals using banker's-free round-half-away-from-
// zero.  Negative values render with a leading minus before the dollar sign
// ("-$0.5000") — but Calculate never produces negatives, so this only
// affects values constructed by tests.
func (m Money) Format() string {
	v := m.USD
	if math.IsNaN(v) {
		return "$NaN"
	}
	if math.IsInf(v, 0) {
		if v < 0 {
			return "-$Inf"
		}
		return "$Inf"
	}
	if v < 0 {
		return fmt.Sprintf("-$%.4f", -v)
	}
	return fmt.Sprintf("$%.4f", v)
}

func nonNeg(n int64) int64 {
	if n < 0 {
		return 0
	}
	return n
}

package cost

import "errors"

// ErrModelNotPriced is returned by [Catalog.LookUp] when the requested
// (provider, model) pair cannot be priced from any source: the live catalog
// did not include it, no usable disk cache contains it, and the hard-coded
// fallback catalog does not list it either.
//
// Callers that want graceful behavior should detect this with [errors.Is]
// and either prompt the user to provide pricing or skip cost accounting for
// that turn — never panic.
var ErrModelNotPriced = errors.New("cost: model not priced by any source")

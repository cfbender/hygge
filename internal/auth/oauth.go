package auth

import "context"

// StartOAuth begins an OAuth flow for provider and returns the URL the
// user should visit to authorise hygge.  v0.1 stubs this out — every
// call returns [ErrOAuthUnsupported].  The signature is fixed now so
// callers (the `hygge provider auth` CLI in particular) can wire the
// OAuth branch without subsequent API churn.
func StartOAuth(_ context.Context, _ string, _ LoadOptions) (authURL string, err error) {
	return "", ErrOAuthUnsupported
}

// CompleteOAuth finishes an OAuth flow started by [StartOAuth] and
// writes the resulting [Credential] to the store.  v0.1 stubs this out
// — every call returns [ErrOAuthUnsupported].
func CompleteOAuth(_ context.Context, _ string, _ string, _ LoadOptions) error {
	return ErrOAuthUnsupported
}

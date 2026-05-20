package openrouter

import (
	"context"
	"net/http"
	"sync"
)

// sessionIDContextKey is the unexported type used as a context key for the
// current-turn session ID.  Callers must use [ContextWithSessionID] and
// [SessionIDFromContext] so the key type stays package-private.
type sessionIDContextKey struct{}

// xSessionIDHeader is the OpenRouter header name for session tracking.
// See https://openrouter.ai/docs/api-reference/chat-completion.
const xSessionIDHeader = "x-session-id"

// maxSessionIDLen is the OpenRouter-documented maximum length for x-session-id.
const maxSessionIDLen = 256

// ContextWithSessionID returns a child context that carries sessionID so
// [SessionHeaderTransport] can read it on outgoing HTTP requests.
func ContextWithSessionID(ctx context.Context, sessionID string) context.Context {
	return context.WithValue(ctx, sessionIDContextKey{}, sessionID)
}

// SessionIDFromContext returns the session ID stored by [ContextWithSessionID],
// or "" when none is set.
func SessionIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(sessionIDContextKey{}).(string)
	return id
}

// RootIDCache resolves and caches the root session ID for any child session.
// It wraps a resolver function (typically session.ResolveRootSessionID) and
// caches results forever — parent chains are immutable once written.
//
// RootIDCache is safe for concurrent use.
type RootIDCache struct {
	mu       sync.RWMutex
	resolved map[string]string // childID → rootID

	// Resolve is called on cache miss.  It must be safe for concurrent use.
	// On error, the fallback is to return childID unchanged.
	Resolve func(ctx context.Context, sessionID string) (string, error)
}

// NewRootIDCache constructs a cache backed by the given resolver.
func NewRootIDCache(resolve func(ctx context.Context, sessionID string) (string, error)) *RootIDCache {
	return &RootIDCache{
		resolved: make(map[string]string),
		Resolve:  resolve,
	}
}

// RootOf returns the root session ID for childID, consulting the cache first
// and calling Resolve on a miss.  On resolver error the cache is not
// populated and childID is returned as a safe fallback.
func (c *RootIDCache) RootOf(ctx context.Context, childID string) string {
	if childID == "" {
		return ""
	}

	// Fast path: already cached.
	c.mu.RLock()
	if rootID, ok := c.resolved[childID]; ok {
		c.mu.RUnlock()
		return rootID
	}
	c.mu.RUnlock()

	// Slow path: resolve via the store.
	rootID, err := c.Resolve(ctx, childID)
	if err != nil || rootID == "" {
		// Best-effort: return the child itself so the caller still gets
		// something useful for the header (avoids a blank header).
		return childID
	}

	c.mu.Lock()
	c.resolved[childID] = rootID
	c.mu.Unlock()

	return rootID
}

// SessionHeaderTransport wraps an inner http.RoundTripper and injects the
// x-session-id header on every outgoing request.  The session ID is read from
// the request context (set via [ContextWithSessionID]); the root session ID is
// resolved via [RootIDCache].
//
// The header is only added when:
//   - The request context carries a non-empty session ID.
//   - The resolved root ID is non-empty.
//
// Values longer than maxSessionIDLen are silently truncated to conform to the
// OpenRouter API contract.
//
// SessionHeaderTransport is safe for concurrent use.
type SessionHeaderTransport struct {
	// Inner is the wrapped transport.  Nil falls back to http.DefaultTransport.
	Inner http.RoundTripper

	// Cache resolves child session IDs to their root.  Required.
	Cache *RootIDCache
}

// RoundTrip implements http.RoundTripper.
func (t *SessionHeaderTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	inner := t.Inner
	if inner == nil {
		inner = http.DefaultTransport
	}

	sessionID := SessionIDFromContext(req.Context())
	if sessionID == "" || t.Cache == nil {
		return inner.RoundTrip(req)
	}

	rootID := t.Cache.RootOf(req.Context(), sessionID)
	if rootID == "" {
		return inner.RoundTrip(req)
	}

	if len(rootID) > maxSessionIDLen {
		rootID = rootID[:maxSessionIDLen]
	}

	// Clone the request to avoid mutating shared headers.
	cloned := req.Clone(req.Context())
	cloned.Header.Set(xSessionIDHeader, rootID)

	return inner.RoundTrip(cloned)
}

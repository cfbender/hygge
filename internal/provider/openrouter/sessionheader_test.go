package openrouter_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	or "github.com/cfbender/hygge/internal/provider/openrouter"
)

// TestContextWithSessionID_RoundTrip verifies that ContextWithSessionID stores
// a value that SessionIDFromContext correctly retrieves.
func TestContextWithSessionID_RoundTrip(t *testing.T) {
	ctx := context.Background()
	ctx = or.ContextWithSessionID(ctx, "sess-abc")
	got := or.SessionIDFromContext(ctx)
	if got != "sess-abc" {
		t.Errorf("got %q, want %q", got, "sess-abc")
	}
}

// TestSessionIDFromContext_Empty verifies the zero value when no session ID is set.
func TestSessionIDFromContext_Empty(t *testing.T) {
	got := or.SessionIDFromContext(context.Background())
	if got != "" {
		t.Errorf("got %q, want empty string", got)
	}
}

// TestRootIDCache_CacheHit verifies that a second call for the same session ID
// does not invoke the resolver again.
func TestRootIDCache_CacheHit(t *testing.T) {
	calls := 0
	cache := or.NewRootIDCache(func(_ context.Context, id string) (string, error) {
		calls++
		return "root-" + id, nil
	})

	got1 := cache.RootOf(context.Background(), "child")
	got2 := cache.RootOf(context.Background(), "child")

	if got1 != "root-child" {
		t.Errorf("first RootOf = %q, want %q", got1, "root-child")
	}
	if got2 != "root-child" {
		t.Errorf("second RootOf = %q, want %q", got2, "root-child")
	}
	if calls != 1 {
		t.Errorf("resolver called %d times, want 1", calls)
	}
}

// TestRootIDCache_EmptyChildID returns empty without calling the resolver.
func TestRootIDCache_EmptyChildID(t *testing.T) {
	calls := 0
	cache := or.NewRootIDCache(func(_ context.Context, _ string) (string, error) {
		calls++
		return "root", nil
	})

	got := cache.RootOf(context.Background(), "")
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
	if calls != 0 {
		t.Errorf("resolver should not be called for empty ID; got %d calls", calls)
	}
}

// TestRootIDCache_ResolverError returns the child ID as a safe fallback without
// caching.
func TestRootIDCache_ResolverError(t *testing.T) {
	calls := 0
	sentinelErr := errors.New("store unavailable")
	cache := or.NewRootIDCache(func(context.Context, string) (string, error) {
		calls++
		return "", sentinelErr
	})

	got1 := cache.RootOf(context.Background(), "child")
	got2 := cache.RootOf(context.Background(), "child")

	// Fallback to the child itself on error.
	if got1 != "child" {
		t.Errorf("first RootOf = %q, want %q", got1, "child")
	}
	if got2 != "child" {
		t.Errorf("second RootOf = %q, want %q", got2, "child")
	}
	// Error result must NOT be cached — each call should re-invoke the resolver
	// in case the store becomes available later.
	if calls != 2 {
		t.Errorf("resolver called %d times, want 2 (no error caching)", calls)
	}
}

// roundTripFunc is an http.RoundTripper backed by a plain function.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// TestSessionHeaderTransport_InjectsHeader verifies that the transport adds
// x-session-id when the context carries a session ID and the cache resolves it.
func TestSessionHeaderTransport_InjectsHeader(t *testing.T) {
	cache := or.NewRootIDCache(func(_ context.Context, id string) (string, error) {
		return "root-" + id, nil
	})

	var capturedHeader string
	inner := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		capturedHeader = r.Header.Get("x-session-id")
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(http.NoBody),
		}, nil
	})

	transport := &or.SessionHeaderTransport{Inner: inner, Cache: cache}

	req, _ := http.NewRequestWithContext(
		or.ContextWithSessionID(context.Background(), "child-123"),
		http.MethodPost, "https://openrouter.ai/api/v1/chat/completions", nil,
	)
	if _, err := transport.RoundTrip(req); err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}

	if capturedHeader != "root-child-123" {
		t.Errorf("x-session-id = %q, want %q", capturedHeader, "root-child-123")
	}
}

// TestSessionHeaderTransport_NoSessionID verifies that no header is added when
// the context carries no session ID.
func TestSessionHeaderTransport_NoSessionID(t *testing.T) {
	cache := or.NewRootIDCache(func(_ context.Context, id string) (string, error) {
		return "root-" + id, nil
	})

	var capturedHeader string
	inner := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		capturedHeader = r.Header.Get("x-session-id")
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(http.NoBody),
		}, nil
	})

	transport := &or.SessionHeaderTransport{Inner: inner, Cache: cache}

	req, _ := http.NewRequestWithContext(
		context.Background(), // no session ID
		http.MethodPost, "https://openrouter.ai/api/v1/chat/completions", nil,
	)
	if _, err := transport.RoundTrip(req); err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}

	if capturedHeader != "" {
		t.Errorf("x-session-id should be absent, got %q", capturedHeader)
	}
}

// TestSessionHeaderTransport_NilCache is a no-op guard when Cache is nil.
func TestSessionHeaderTransport_NilCache(t *testing.T) {
	var capturedHeader string
	inner := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		capturedHeader = r.Header.Get("x-session-id")
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(http.NoBody),
		}, nil
	})

	transport := &or.SessionHeaderTransport{Inner: inner, Cache: nil}

	req, _ := http.NewRequestWithContext(
		or.ContextWithSessionID(context.Background(), "child-456"),
		http.MethodPost, "https://openrouter.ai/api/v1/chat/completions", nil,
	)
	if _, err := transport.RoundTrip(req); err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}

	if capturedHeader != "" {
		t.Errorf("x-session-id should be absent with nil cache, got %q", capturedHeader)
	}
}

// TestSessionHeaderTransport_TruncatesLongID verifies that IDs exceeding 256
// characters are silently truncated before being set as the header value.
func TestSessionHeaderTransport_TruncatesLongID(t *testing.T) {
	// Construct a 300-character root ID.
	longRootID := make([]byte, 300)
	for i := range longRootID {
		longRootID[i] = byte('a' + i%26)
	}

	cache := or.NewRootIDCache(func(_ context.Context, _ string) (string, error) {
		return string(longRootID), nil
	})

	var capturedHeader string
	inner := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		capturedHeader = r.Header.Get("x-session-id")
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(http.NoBody),
		}, nil
	})

	transport := &or.SessionHeaderTransport{Inner: inner, Cache: cache}

	req, _ := http.NewRequestWithContext(
		or.ContextWithSessionID(context.Background(), "child"),
		http.MethodPost, "https://openrouter.ai/api/v1/chat/completions", nil,
	)
	if _, err := transport.RoundTrip(req); err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}

	if len(capturedHeader) != 256 {
		t.Errorf("header length = %d, want 256 (truncated)", len(capturedHeader))
	}
}

// TestSessionHeaderTransport_NilInner falls back to http.DefaultTransport when
// Inner is nil, which means a real HTTP call would go out.  We test this by
// pointing at an httptest server.
func TestSessionHeaderTransport_NilInner(t *testing.T) {
	var capturedHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedHeader = r.Header.Get("x-session-id")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cache := or.NewRootIDCache(func(_ context.Context, id string) (string, error) {
		return "root-" + id, nil
	})

	transport := &or.SessionHeaderTransport{Inner: nil, Cache: cache}

	req, _ := http.NewRequestWithContext(
		or.ContextWithSessionID(context.Background(), "ses"),
		http.MethodGet, srv.URL, nil,
	)
	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	_ = resp.Body.Close()

	if capturedHeader != "root-ses" {
		t.Errorf("x-session-id = %q, want %q", capturedHeader, "root-ses")
	}
}

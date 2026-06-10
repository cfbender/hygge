package ui

import (
	"testing"
	"time"
)

// baseTime is a fixed instant used as a reference point throughout these tests.
var baseTime = time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)

// TestMessageCache_FreshPutIsHit verifies that a Get immediately after a Put
// returns the stored content with ok=true.
func TestMessageCache_FreshPutIsHit(t *testing.T) {
	t.Parallel()
	var c messageCache
	c.Put("hello", 80, 5, baseTime)

	got, ok := c.Get(80, 5, baseTime, false)
	if !ok {
		t.Fatal("expected cache hit after Put; got miss")
	}
	if got != "hello" {
		t.Fatalf("Get returned %q; want %q", got, "hello")
	}
}

// TestMessageCache_WidthChangeMiss verifies that a width change causes a miss.
func TestMessageCache_WidthChangeMiss(t *testing.T) {
	t.Parallel()
	var c messageCache
	c.Put("content", 80, 5, baseTime)

	_, ok := c.Get(100, 5, baseTime, false)
	if ok {
		t.Fatal("expected cache miss on width change; got hit")
	}
}

// TestMessageCache_CountChangeMiss verifies that a message-count change causes a miss.
func TestMessageCache_CountChangeMiss(t *testing.T) {
	t.Parallel()
	var c messageCache
	c.Put("content", 80, 5, baseTime)

	_, ok := c.Get(80, 6, baseTime, false)
	if ok {
		t.Fatal("expected cache miss on count change; got hit")
	}
}

// TestMessageCache_TTLExpiryMiss verifies that content older than 30 seconds is
// a miss even when width and count are unchanged.
func TestMessageCache_TTLExpiryMiss(t *testing.T) {
	t.Parallel()
	var c messageCache
	c.Put("content", 80, 5, baseTime)

	// Exactly at the boundary (30 s) should still be valid.
	now := baseTime.Add(30 * time.Second)
	_, ok := c.Get(80, 5, now, false)
	if !ok {
		t.Fatal("expected cache hit at exactly 30-second boundary; got miss")
	}

	// One nanosecond past the boundary is a miss.
	nowPlus := baseTime.Add(30*time.Second + time.Nanosecond)
	_, ok = c.Get(80, 5, nowPlus, false)
	if ok {
		t.Fatal("expected cache miss after TTL expiry; got hit")
	}
}

// TestMessageCache_StreamingDirtyScrolledAway verifies the deferred-rebuild
// path: when userScrolled=true and streamingDirty is set, Get returns a hit
// so the full re-render is deferred until the user returns to the bottom.
func TestMessageCache_StreamingDirtyScrolledAway(t *testing.T) {
	t.Parallel()
	var c messageCache
	c.Put("before", 80, 5, baseTime)

	// Simulate a streaming delta while scrolled away: InvalidateStreaming with
	// userScrolled=true should set streamingDirty and not fully invalidate.
	c.InvalidateStreaming(true)

	if !c.streamingDirty {
		t.Fatal("expected streamingDirty=true after InvalidateStreaming(scrolled=true)")
	}
	if !c.valid {
		t.Fatal("expected valid=true (cache not fully invalidated while scrolled)")
	}

	// Get with userScrolled=true → hit (defer the rebuild).
	got, ok := c.Get(80, 5, baseTime, true)
	if !ok {
		t.Fatal("expected cache hit while scrolled away with streamingDirty; got miss")
	}
	if got != "before" {
		t.Fatalf("Get returned %q; want %q", got, "before")
	}
}

// TestMessageCache_StreamingDirtyAtBottom verifies that when the user is at the
// bottom (userScrolled=false) and streamingDirty is set, Get returns a miss so
// the content is rebuilt immediately.
func TestMessageCache_StreamingDirtyAtBottom(t *testing.T) {
	t.Parallel()
	var c messageCache
	c.Put("before", 80, 5, baseTime)
	c.InvalidateStreaming(true) // set streamingDirty while scrolled away

	// Now the user scrolls back to the bottom.
	_, ok := c.Get(80, 5, baseTime, false)
	if ok {
		t.Fatal("expected cache miss at bottom with streamingDirty; got hit")
	}
}

// TestMessageCache_InvalidateClearsStreamingDirty verifies that a full
// Invalidate clears the streamingDirty flag (not just valid).
func TestMessageCache_InvalidateClearsStreamingDirty(t *testing.T) {
	t.Parallel()
	var c messageCache
	c.Put("x", 80, 3, baseTime)
	c.InvalidateStreaming(true) // sets streamingDirty

	c.Invalidate() // full invalidation

	if c.valid {
		t.Fatal("expected valid=false after Invalidate")
	}
	if c.streamingDirty {
		t.Fatal("expected streamingDirty=false after Invalidate")
	}
}

// TestMessageCache_PutClearsStreamingDirty verifies that a Put after a
// streaming invalidation clears the dirty flag so a subsequent hit is clean.
func TestMessageCache_PutClearsStreamingDirty(t *testing.T) {
	t.Parallel()
	var c messageCache
	c.Put("before", 80, 5, baseTime)
	c.InvalidateStreaming(true) // sets streamingDirty

	// Full rebuild by caller after returning to bottom.
	c.Put("after", 80, 5, baseTime)

	if c.streamingDirty {
		t.Fatal("expected streamingDirty=false after Put")
	}
	got, ok := c.Get(80, 5, baseTime, false)
	if !ok {
		t.Fatal("expected cache hit after rebuild Put; got miss")
	}
	if got != "after" {
		t.Fatalf("Get returned %q; want %q", got, "after")
	}
}

// TestMessageCache_InvalidateStreamingNotScrolledFullyInvalidates ensures that
// InvalidateStreaming with userScrolled=false does a full invalidation, not just
// setting the dirty flag.
func TestMessageCache_InvalidateStreamingNotScrolledFullyInvalidates(t *testing.T) {
	t.Parallel()
	var c messageCache
	c.Put("content", 80, 5, baseTime)

	c.InvalidateStreaming(false) // NOT scrolled away → full invalidate

	if c.valid {
		t.Fatal("expected valid=false when InvalidateStreaming called with userScrolled=false")
	}
	if c.streamingDirty {
		t.Fatal("expected streamingDirty=false (full invalidate clears it)")
	}
}

// TestMessageCache_FreshCacheIsAlwaysMiss verifies the zero-value cache always
// returns a miss (no panic, no spurious hit).
func TestMessageCache_FreshCacheIsAlwaysMiss(t *testing.T) {
	t.Parallel()
	var c messageCache

	_, ok := c.Get(80, 0, baseTime, false)
	if ok {
		t.Fatal("expected miss from zero-value cache; got hit")
	}
}

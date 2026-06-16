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

// TestMessageCache_MarkStreamingDirtyDoesNotForceMiss verifies that a streaming
// delta marks the cache dirty without forcing a Get miss. Rebuilds are paced by
// the coalescing tick, so an intervening render frame (keypress/scroll) keeps
// hitting the cache regardless of scroll position.
func TestMessageCache_MarkStreamingDirtyDoesNotForceMiss(t *testing.T) {
	t.Parallel()
	var c messageCache
	c.Put("before", 80, 5, baseTime)

	c.MarkStreamingDirty()

	if !c.streamingDirty {
		t.Fatal("expected streamingDirty=true after MarkStreamingDirty")
	}
	if !c.valid {
		t.Fatal("expected valid=true (MarkStreamingDirty must not invalidate)")
	}

	// Both at-bottom and scrolled-away must hit: rebuilds are tick-paced.
	for _, scrolled := range []bool{false, true} {
		got, ok := c.Get(80, 5, baseTime, scrolled)
		if !ok {
			t.Fatalf("expected cache hit while streamingDirty (scrolled=%v); got miss", scrolled)
		}
		if got != "before" {
			t.Fatalf("Get returned %q; want %q", got, "before")
		}
	}
}

// TestMessageCache_FlushStreamingDirtyAtBottom verifies the coalescing tick
// flush: at the bottom (userScrolled=false) a pending delta invalidates the
// cache (reports rebuilt=true) so the next frame rebuilds once.
func TestMessageCache_FlushStreamingDirtyAtBottom(t *testing.T) {
	t.Parallel()
	var c messageCache
	c.Put("before", 80, 5, baseTime)
	c.MarkStreamingDirty()

	rebuilt := c.FlushStreamingDirty(false)
	if !rebuilt {
		t.Fatal("expected FlushStreamingDirty to report rebuilt=true at bottom")
	}
	if c.valid {
		t.Fatal("expected valid=false after flush at bottom")
	}
	if c.streamingDirty {
		t.Fatal("expected streamingDirty cleared after flush at bottom")
	}
	if _, ok := c.Get(80, 5, baseTime, false); ok {
		t.Fatal("expected cache miss after flush; got hit")
	}
}

// TestMessageCache_FlushStreamingDirtyScrolledAway verifies that while scrolled
// away the flush defers the rebuild: it keeps the dirty flag and the cache stays
// valid (reports rebuilt=false), so off-screen streaming costs nothing.
func TestMessageCache_FlushStreamingDirtyScrolledAway(t *testing.T) {
	t.Parallel()
	var c messageCache
	c.Put("before", 80, 5, baseTime)
	c.MarkStreamingDirty()

	rebuilt := c.FlushStreamingDirty(true)
	if rebuilt {
		t.Fatal("expected FlushStreamingDirty to report rebuilt=false while scrolled away")
	}
	if !c.valid {
		t.Fatal("expected valid=true (rebuild deferred while scrolled away)")
	}
	if !c.streamingDirty {
		t.Fatal("expected streamingDirty retained while scrolled away")
	}
	// Returning to the bottom and flushing should now rebuild.
	if !c.FlushStreamingDirty(false) {
		t.Fatal("expected rebuilt=true after returning to bottom")
	}
}

// TestMessageCache_FlushStreamingDirtyNoop verifies that flushing with no
// pending delta is a no-op that reports rebuilt=false and leaves the cache
// valid.
func TestMessageCache_FlushStreamingDirtyNoop(t *testing.T) {
	t.Parallel()
	var c messageCache
	c.Put("before", 80, 5, baseTime)

	if c.FlushStreamingDirty(false) {
		t.Fatal("expected rebuilt=false when nothing is dirty")
	}
	if !c.valid {
		t.Fatal("expected valid=true (no-op flush must not invalidate)")
	}
}

// TestMessageCache_InvalidateClearsStreamingDirty verifies that a full
// Invalidate clears the streamingDirty flag (not just valid).
func TestMessageCache_InvalidateClearsStreamingDirty(t *testing.T) {
	t.Parallel()
	var c messageCache
	c.Put("x", 80, 3, baseTime)
	c.MarkStreamingDirty()

	c.Invalidate() // full invalidation

	if c.valid {
		t.Fatal("expected valid=false after Invalidate")
	}
	if c.streamingDirty {
		t.Fatal("expected streamingDirty=false after Invalidate")
	}
}

// TestMessageCache_PutClearsStreamingDirty verifies that a Put after a
// streaming-dirty mark clears the dirty flag so a subsequent hit is clean.
func TestMessageCache_PutClearsStreamingDirty(t *testing.T) {
	t.Parallel()
	var c messageCache
	c.Put("before", 80, 5, baseTime)
	c.MarkStreamingDirty()

	// Full rebuild by caller after the coalescing tick invalidated.
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

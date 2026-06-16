package ui

import "time"

// messageCache holds the pre-rendered message list string and the metadata
// used to decide whether a rebuild is needed.  All fields are unexported;
// callers use Invalidate, MarkStreamingDirty, FlushStreamingDirty, Get, and
// Put.
type messageCache struct {
	// content is the last rendered message-list string.
	content string
	// valid is true when content is up-to-date.
	valid bool
	// streamingDirty is set by MarkStreamingDirty when a streaming delta
	// arrives. The rebuild is deferred to the coalescing tick
	// (FlushStreamingDirty), which paces full re-renders so neither the
	// per-delta path nor an intervening keypress/scroll frame pays for one.
	streamingDirty bool
	// width is the left-column width at which content was rendered.
	width int
	// msgLen is the message count at which content was rendered.
	msgLen int
	// cachedAt is the wall-clock time at which content was rendered; used for
	// the 30-second relative-timestamp TTL check.
	cachedAt time.Time
}

// Invalidate marks the cache unconditionally stale and clears the streaming
// dirty flag.  Call after any change that must be reflected on the next frame
// regardless of scroll position (resize, theme switch, message append, etc.).
func (c *messageCache) Invalidate() {
	c.valid = false
	c.streamingDirty = false
}

// MarkStreamingDirty records that a streaming-tail delta arrived without
// rebuilding the transcript. The actual rebuild is deferred to the coalescing
// tick (see App.handleStreamCoalesceTick), which calls FlushStreamingDirty at
// a bounded rate. This keeps per-delta cost off the render path entirely: a
// keypress or scroll frame that lands between ticks still hits the valid cache
// and renders instantly instead of paying for a full re-style.
func (c *messageCache) MarkStreamingDirty() {
	c.streamingDirty = true
}

// FlushStreamingDirty is called by the coalescing tick. When a streaming delta
// is pending and the user is at the bottom (not scrolled away), it invalidates
// the cache so the next frame rebuilds once, and reports true. When the user
// is scrolled away the rebuild stays deferred (the flag is kept) so streaming
// updates off-screen do not trigger costly rebuilds; it reports false. When
// nothing is pending it reports false.
func (c *messageCache) FlushStreamingDirty(userScrolled bool) (rebuilt bool) {
	if !c.streamingDirty {
		return false
	}
	if userScrolled {
		// Keep the flag; rebuild when the user returns to the bottom.
		return false
	}
	c.Invalidate()
	return true
}

// StreamingDirty reports whether a streaming delta is pending an eventual
// rebuild.
func (c *messageCache) StreamingDirty() bool {
	return c.streamingDirty
}

// Get returns the cached content and ok=true when the cache is valid for the
// given parameters.  It returns "", false when a rebuild is required.
//
// A rebuild is required when any of the following is true:
//   - the cache is not valid
//   - width has changed
//   - msgCount has changed
//   - more than 30 seconds have elapsed since the last Put
//
// Note: the streamingDirty flag deliberately does NOT force a miss here.
// Streaming-delta rebuilds are paced by the coalescing tick, which calls
// FlushStreamingDirty to invalidate the cache at a bounded rate. This keeps an
// arbitrary render frame (e.g. one triggered by a keypress or scroll) from
// rebuilding the whole transcript just because a delta arrived since the last
// tick — input frames stay cheap.
//
// The userScrolled parameter is retained for signature stability and possible
// future scroll-aware behavior; it currently does not affect the result.
func (c *messageCache) Get(width, msgCount int, now time.Time, userScrolled bool) (content string, ok bool) {
	_ = userScrolled
	if !c.valid ||
		c.width != width ||
		c.msgLen != msgCount ||
		now.Sub(c.cachedAt) > 30*time.Second {
		return "", false
	}
	return c.content, true
}

// Put stores a freshly built content string together with the metadata that
// was used to produce it, and clears the streamingDirty flag.
func (c *messageCache) Put(content string, width, msgCount int, now time.Time) {
	c.content = content
	c.valid = true
	c.streamingDirty = false
	c.width = width
	c.msgLen = msgCount
	c.cachedAt = now
}

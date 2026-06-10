package ui

import "time"

// messageCache holds the pre-rendered message list string and the metadata
// used to decide whether a rebuild is needed.  All fields are unexported;
// callers use Invalidate, InvalidateStreaming, Get, and Put.
type messageCache struct {
	// content is the last rendered message-list string.
	content string
	// valid is true when content is up-to-date.
	valid bool
	// streamingDirty is set by InvalidateStreaming when the user is scrolled
	// away from the bottom.  The rebuild is deferred until the user returns to
	// the bottom so live streaming updates don't cause costly full re-renders.
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

// InvalidateStreaming handles a streaming-tail delta.  When userScrolled is
// true and the cache is currently valid, the rebuild is deferred: only the
// streamingDirty flag is set.  Otherwise the cache is fully invalidated.
func (c *messageCache) InvalidateStreaming(userScrolled bool) {
	if userScrolled && c.valid {
		c.streamingDirty = true
		return
	}
	c.Invalidate()
}

// Get returns the cached content and ok=true when the cache is valid for the
// given parameters.  It returns "", false when a rebuild is required.
//
// A rebuild is required when any of the following is true:
//   - the cache is not valid
//   - width has changed
//   - msgCount has changed
//   - the streamingDirty flag is set and the user is NOT scrolled away
//     (i.e. !userScrolled — encoded as userScrolled==false in the
//     (!userScrolled && streamingDirty) check)
//   - more than 30 seconds have elapsed since the last Put
//
// The userScrolled parameter is the App's current scroll-away flag; this
// matches the original render check: (!a.userScrolled && a.msgCacheStreamingDirty).
func (c *messageCache) Get(width, msgCount int, now time.Time, userScrolled bool) (content string, ok bool) {
	if !c.valid ||
		c.width != width ||
		c.msgLen != msgCount ||
		(!userScrolled && c.streamingDirty) ||
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

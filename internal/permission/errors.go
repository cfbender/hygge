package permission

import "errors"

// ErrBusClosed is returned by Engine.Ask when the underlying bus is closed
// (e.g. the subscription channel was closed) before a reply could be received.
// Callers should treat this as a transient infrastructure error, not a deny.
var ErrBusClosed = errors.New("permission: bus closed before reply received")

// ErrEngineClosed is returned by Engine.Ask when the Engine itself has been
// closed via Engine.Close.  Subsequent Asks after Close always return this.
var ErrEngineClosed = errors.New("permission: engine closed")

// ErrInvalidPattern is wrapped when a Rule's Pattern is not a valid
// doublestar glob.  Returned by NewMatcher.
var ErrInvalidPattern = errors.New("permission: invalid rule pattern")

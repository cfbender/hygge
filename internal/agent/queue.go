package agent

import "github.com/cfbender/hygge/internal/session"

// QueuedSend holds the payload for a user message that arrived while
// the session was already busy.  The send is held in the per-session
// queue and dispatched automatically when the current run completes.
type QueuedSend struct {
	// Parts is the user message content to send once the session is free.
	Parts []session.Part
}

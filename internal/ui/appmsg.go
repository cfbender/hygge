package ui

import (
	"time"

	"github.com/cfbender/hygge/internal/session"
)

// busDelivery is the bubbletea Msg that wraps a single event read off the
// bus-to-channel bridge.  See app.go listenBus for the producer loop.
type busDelivery struct {
	// Event is the concrete bus.* event struct.  Update inspects the dynamic
	// type with a type switch.
	Event any
}

// sendStarted is published locally (not on the bus) when the user submits an
// input and the agent.Send goroutine is launched.  Used to flip the busy state.
type sendStarted struct {
	UserInput string
	StartedAt time.Time
}

// sendCompleted fires when the agent.Send goroutine returns.  The Err field is
// nil on success, non-nil for any error including context cancellation.
type sendCompleted struct {
	Result *session.Message
	Err    error
}

// clearToastMsg fires after the modal toast's display duration elapses.
type clearToastMsg struct{}

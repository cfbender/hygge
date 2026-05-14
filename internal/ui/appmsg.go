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

// clearNoticeMsg fires after the slash-command ephemeral notice
// display duration elapses.  Carries the original notice text so a
// later notice that landed in the meantime is not overwritten.
type clearNoticeMsg struct {
	notice string
}

// subagentTickMsg fires every second for an active sub-agent to drive
// the elapsed-time counter in its rendered block.  The handler in
// app.go re-issues the tick while the sub-agent is still running and
// drops it once SubagentCompleted has arrived (the State.EndedAt
// becomes non-zero).  This keeps redraws bounded to one tick per
// active sub-agent per second instead of the whole-app spinner
// cadence -- matches the "don't spam redraws" constraint.
type subagentTickMsg struct {
	SubSessionID string
}

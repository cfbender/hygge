package ui

import (
	"time"

	"github.com/cfbender/hygge/internal/session"
	"github.com/cfbender/hygge/internal/ui/styles"
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

type steerCompleted struct {
	text string
	err  error
}

type queuedPromptDraft struct {
	Text        string
	Attachments []promptAttachment
}

// workingVerbTickMsg fires periodically while the app is busy to rotate the
// footer's working label without changing it on every spinner frame.
type workingVerbTickMsg struct{}

// clearNoticeMsg fires after the slash-command ephemeral notice
// display duration elapses.  Carries the original notice text so a
// later notice that landed in the meantime is not overwritten.
type clearNoticeMsg struct {
	notice string
}

type rememberSessionMemoryMsg struct {
	content string
	warning string
	err     error
}

type forgetMemoryMsg struct {
	memoryID string
	err      error
}

type memoriesLoadedMsg struct {
	memories []*session.Memory
	err      error
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

// --- Session modal messages -------------------------------------------------
// These are emitted by the sessions modal component and handled in app.go.
// App applies store + agent side effects and updates foreground state.

// switchSessionMsg asks the App to change the foreground session.
type switchSessionMsg struct {
	ID            string
	ToastTitle    string
	ToastSubtitle string
}

// --- Compaction messages ----------------------------------------------------
// Internal messages for the compaction modal, banner, and in-flight block.

// compactionRunMsg fires when the user accepts the compaction modal.
// The App handles it by calling Agent.Compact asynchronously.
type compactionRunMsg struct {
	SessionID string
}

// compactionCompleteMsg fires when Agent.Compact returns (success or fail).
// Used to clear the in-flight compaction block and show the toast.
type compactionCompleteMsg struct {
	// Err is non-nil on failure.
	Err error
	// MarkerID is the id of the persisted marker on success.
	MarkerID string
	// MessagesCompacted is the number of messages that were summarised.
	MessagesCompacted int
	// SummaryTokens is the token count of the generated summary.
	SummaryTokens int64
}

// clearCompactionToastMsg fires after the post-compaction toast display
// duration (5 seconds) to hide it.
type clearCompactionToastMsg struct{}

// dismissBannerMsg asks the App to hide the threshold suggestion banner for
// the current crossing.  Fired when the user presses Ctrl+X.
type dismissBannerMsg struct{}

// splashFogTickMsg fires at ~30fps while the splash is on screen to drive the
// fog animation. The tick self-terminates when the splash is no longer
// active; the spinner tick handler re-arms it when the splash returns.
type splashFogTickMsg struct{}

// busyReconcileTickMsg fires once per second while the App may be in a
// busy-desync state.  The handler compares UI busy state against the agent's
// canonical activeRuns and corrects any drift caused by dropped bus events
// (e.g. a TurnCompleted or TurnStarted lost when the bus drops events during
// a system lock with active streaming).  The tick re-arms itself only when
// there is still something to watch; it self-terminates otherwise.
type busyReconcileTickMsg struct{}

// markdownBatchMsg carries the result of a background glamour render pass
// over a set of hydrated messages, keyed by MessageID.
//
//   - rendered maps MessageID → rendered glamour string.  Only messages whose
//     current MessageID matches a key here receive FinalMarkdown updates.
//   - fallback holds per-index (startIdx+i → rawSnap,rendered) data for messages
//     that had no MessageID at snapshot time.  These are applied only when the
//     message at that index still has the same Raw as when snapshotted.
//   - width and theme are the msgColW and Theme that were current when the
//     render started; used to detect stale results when a resize or theme
//     switch arrived in the meantime.
type markdownBatchMsg struct {
	// MessageID-keyed results (safe against index shifts).
	rendered map[string]string // messageID → glamour output
	// Index-keyed fallback for messages without a MessageID.
	// Only applied when the message at the index still has the same Raw.
	fallback []markdownBatchFallback
	width    int
	theme    *styles.Styles
}

// markdownBatchFallback holds the index-keyed render result for a message
// that had no stable MessageID when the batch was issued.
type markdownBatchFallback struct {
	idx     int
	rawSnap string
	out     string
}

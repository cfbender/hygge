// Package command implements the slash-command framework for hygge.
//
// A slash command is anything the user types into the TUI input that
// begins with `/`.  The TUI strips the leading slash, splits the head
// token off as the command name, hands the remainder to the matching
// [Command].Execute, and applies the returned [Outcome] to its own
// state.
//
// # Layering
//
// internal/command sits below internal/ui and above internal/session.
// It may import internal/provider and internal/session for the [App]
// adapter shapes the built-in commands need to read; it must NOT
// import internal/ui.  cmd/hygge/cli wires the [Registry] into the
// TUI.
//
// # Two kinds of commands
//
//   - Built-in: a small Go struct with imperative semantics.  Lives
//     in builtins.go.  Examples: /clear, /help, /cost.
//   - TOML prompt template: declared in a discovered commands.toml,
//     parsed and registered at [Load] time.  Renders user input
//     against a template and returns it as a [Outcome.Message] that
//     the TUI sends as a normal user turn.
//
// Both kinds share a single [Registry].  TOML entries can override
// built-ins of the same name — that is a feature, so users can
// customise the prompts hygge sends for things like /compact.
//
// # Outcomes, not mutation
//
// Commands do not mutate TUI state directly.  Execute returns an
// [Outcome] that names what the TUI should do next: send a message,
// show an ephemeral notice, clear the history, trigger compaction,
// open a named modal, or apply config-style updates (model change,
// reasoning level, etc.).  The TUI is the only thing that may touch
// its own state.  This keeps commands trivially testable in
// isolation and keeps the TUI free to evolve its internals without
// breaking command implementations.
//
// # Name rules
//
// Command names are case-sensitive and must match [a-z][a-z0-9_-]*.
// They never carry the leading slash; the TUI strips it before the
// lookup.  Duplicate registration is an error; layered overrides go
// through the loader's normal "later layer wins" pathway.
package command

import (
	"context"

	"github.com/cfbender/hygge/internal/provider"
	"github.com/cfbender/hygge/internal/session"
)

// Well-known [Outcome.Updates] keys recognised by the TUI dispatcher.
// Commands set entries on the Updates map; the TUI applies each by
// name.  Unknown keys are logged and ignored.
const (
	// UpdateModel asks the TUI to switch the active model.
	// Value shape: "<provider>/<model-id>".
	UpdateModel = "model"

	// UpdateReasoning asks the TUI to switch the reasoning effort.
	// Value: "off" | "low" | "medium" | "high".
	UpdateReasoning = "reasoning"

	// UpdateForkAt asks the TUI to fork the session at the named
	// message id.  Until session-management UI lands (T1.2) the TUI
	// renders this as an explanatory notice and otherwise no-ops.
	UpdateForkAt = "fork_at"

	// UpdateAttachFile asks the TUI to attach a local file path to the next
	// prompt. Value shape: local filesystem path typed by the user.
	UpdateAttachFile = "attach_file"

	// UpdateAttachments asks the TUI to mutate pending prompt attachments.
	// Currently recognised value: "clear".
	UpdateAttachments = "attachments"

	// UpdateTheme asks the TUI to load and persist a theme by name.
	UpdateTheme = "theme"

	// UpdateYolo asks the TUI to toggle reduced-confirmation mode.
	// Value: "on" | "off" | "toggle".
	UpdateYolo = "yolo"

	// UpdateRememberSessionMemory asks the TUI to persist memory.
	// Value shape: "<scope>\n<content>".
	UpdateRememberSessionMemory = "remember_session_memory"

	// UpdateRememberMemoryDraft passes no-scope /remember text into the scope picker.
	UpdateRememberMemoryDraft = "remember_memory_draft"

	// UpdateForgetMemory asks the TUI to forget memory by ID.
	// Value shape: "<scope>\n<memory_id>".
	UpdateForgetMemory = "forget_memory"

	// UpdateQueueMessage asks the TUI to add text to the explicit queued-message list.
	UpdateQueueMessage = "queue_message"

	// UpdateSteerMessage asks the TUI to send active-turn steering to the agent.
	UpdateSteerMessage = "steer_message"

	// UpdateLayout asks the TUI to switch the in-session layout mode without
	// persisting to config.  Value: "default" | "compact" | "toggle".
	UpdateLayout = "layout"
)

// Well-known modal names a command may request via [Outcome.OpenModal].
// The TUI is free to ignore unknown names; commands should stick to
// this set.
const (
	ModalHelp           = "help"
	ModalSessions       = "sessions"
	ModalMemory         = "memory"
	ModalRememberMemory = "memory-remember"
	ModalForgetMemory   = "memory-forget"
	ModalCompactConfirm = "compact-confirm"
	ModalModel          = "model"
	ModalAPIKey         = "apikey"
	ModalTheme          = "theme"
)

// App is the read-only handle commands use to inspect live TUI state.
// It is intentionally narrow: commands must NOT need anything that
// is not on this interface, and they must not mutate the TUI.  The
// TUI implements [App] on a tiny adapter wrapper so commands never
// see its private fields.
type App interface {
	// SessionID returns the active foreground session id.  Empty
	// when no session has been bound yet (first input in a fresh
	// TUI before the lazy create has fired).
	SessionID() string

	// Model returns the current "<provider>/<model-id>" pair.
	Model() string

	// Reasoning returns the current reasoning configuration.
	Reasoning() provider.Reasoning

	// Cost returns the running session cost in USD.
	Cost() float64

	// Sessions returns the most recent sessions, newest first, up to
	// limit.  Used by /sessions and similar to populate listings
	// without forcing every command to learn the store API.
	Sessions(ctx context.Context, limit int) ([]*session.Session, error)
}

// ArgSpec describes one declared argument of a [Command].  Built-in
// commands use this primarily for /help rendering; TOML
// prompt-template commands use it to drive the template renderer's
// positional/named lookup.
type ArgSpec struct {
	// Name is the canonical token used in template substitutions
	// (`{{name}}`).  Must match [a-z][a-z0-9_]*.
	Name string

	// Description is the one-line user-facing hint shown by /help.
	Description string

	// Required controls whether [renderTemplate] errors out when no
	// value is supplied at invocation time.
	Required bool
}

// Outcome is the closed set of effects a command may request.  The
// TUI interprets each field; commands never call into TUI state
// directly.  An [Outcome] with all zero fields is a valid "do
// nothing" reply (rare but useful for /fork during the T1.2
// transition).
type Outcome struct {
	// Message, if non-empty, is appended to the session as a new
	// user message and triggers a normal agent turn.  Used by TOML
	// prompt-template commands.
	Message string

	// Notice, if non-empty, is shown as an ephemeral status line.
	// Not persisted to the session.  Used by /help, /cost, /model
	// (no-arg), and any error reply.
	Notice string

	// ClearHistory asks the TUI to drop the rendered session history
	// from view.  /clear sets this.
	ClearHistory bool

	// NewSession asks the TUI to clear the foreground and create a fresh
	// session on the next user input. /new sets this; /clear aliases it.
	NewSession bool

	// Compact asks the TUI to trigger a compaction pass on the
	// current session.  /compact sets this.
	Compact bool

	// OpenModal names a modal the TUI should open.  The set of
	// recognised names is in this package's Modal* constants.
	// Unknown names are logged and ignored.
	OpenModal string

	// Updates carries well-known config diffs to apply.  Keys are
	// the Update* constants; values are typed strings.  Unknown
	// keys are logged and ignored.
	Updates map[string]string
}

// Command is a single slash command.  Implementations are stateless
// or guard their own state — Execute may be invoked concurrently
// across sessions in a multi-pane TUI of the future.
type Command interface {
	// Name returns the canonical name without the leading slash.
	Name() string

	// Description is the one-line summary surfaced in /help and the
	// command palette.
	Description() string

	// Source identifies where the command came from: "builtin",
	// "user", or "project".  Surfaced by `hygge commands list`.
	Source() string

	// Args returns the named arguments this command accepts in
	// declaration order.  An empty slice means "no named args"; the
	// command palette will still show the command but free-form
	// trailing text is captured as a single string and surfaced
	// through the template renderer's reserved `{{tail}}` slot.
	Args() []ArgSpec

	// Execute runs the command.  input is everything after the
	// command name (i.e. the user typed `/name <input>`).  ctx is
	// the TUI's request context.
	Execute(ctx context.Context, app App, input string) (Outcome, error)
}

// LoadOptions configures [Load].  Empty fields fall back to the
// runtime defaults: $HOME, $XDG_CONFIG_HOME, and getwd.  Tests pass
// every field explicitly to redirect every disk access into a
// t.TempDir.
type LoadOptions struct {
	// HomeDir overrides $HOME.
	HomeDir string

	// XDGConfigHome overrides $XDG_CONFIG_HOME.  Empty falls back to
	// $HOME/.config.
	XDGConfigHome string

	// Pwd is the starting directory for the project walk-up.  Empty
	// skips project-layer discovery.
	Pwd string
}

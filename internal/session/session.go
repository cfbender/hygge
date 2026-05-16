// Package session defines the domain types for a hygge conversation and the
// Store interface that persists them.
//
// # Layering
//
// This package is the pure-Go domain layer.  It has no dependency on SQL or
// any storage backend.  internal/store provides the SQLite implementation
// and imports this package; the reverse is forbidden.
//
// # Forks are references, not copies
//
// A child session points at its parent via ParentID and ForkMessageID; the
// parent's messages are never duplicated.  Reading the conversation for a
// forked session means walking the ancestry chain and selecting the slice of
// messages each ancestor contributes up to its fork point.  Truncating a
// branch never affects the parent.
//
// # Soft delete
//
// Sessions and messages carry a DeletedAt timestamp.  A zero-valued DeletedAt
// means "not deleted".  ListSessions excludes deleted rows by default; an
// explicit IncludeDeleted opt-in returns them.  Hard deletes are not part of
// the public API in v0.1 — they only happen via raw SQL during maintenance
// or via the FK CASCADE that fires when a session row is hard-deleted by an
// administrator.
//
// # Compaction is a marker row, never a deletion
//
// When a session is compacted, hygge writes a compaction_markers row recording
// the cut-off message and a summary.  The original messages are left in
// place; only the "live" context window seen by the agent is shortened.  The
// latest marker on a session defines the current context window.
//
// # Bus events are NOT this layer's job
//
// Persistence is decoupled from event publication.  The agent (Task 11)
// publishes bus events after Store operations succeed.  The Store itself is
// silent.
//
// # Session kinds
//
// Every session row carries a [Kind].  The two recognised kinds are
// [KindPrimary] (the default; a normal user-facing conversation) and
// [KindSubagent] (a sub-session spawned by the `task` tool).  Subagent
// sessions have a non-empty ParentID pointing at the primary session that
// dispatched them, but -- unlike a forked session -- they do NOT need a
// ForkMessageID: a subagent has its own fresh history rather than
// inheriting the parent's prefix.
//
// We chose an explicit Kind column over reusing fork_message_id (Approach
// B in the Stage A design) because `hygge sessions list` will grow filters
// next ("show me just the primary sessions") and a column-level
// distinction is cheaper to query than a row-by-row inspection of message
// parts.
package session

import (
	"context"
	"encoding/json"
	"time"
)

// ModelRef identifies a provider and model name.
type ModelRef struct {
	Provider string
	Name     string
}

// Kind identifies how a session was created.  See the package doc for
// the rationale behind the explicit column.
type Kind string

// Recognised session kinds.  The DB CHECK constraint enforces this exact
// set.
const (
	// KindPrimary is the default kind: a regular user-facing
	// conversation, possibly forked from another primary session.
	KindPrimary Kind = "primary"
	// KindSubagent is the kind tagged on sub-sessions spawned by the
	// `task` tool.  These sessions carry a ParentID pointing at the
	// dispatching session but do not require ForkMessageID.
	KindSubagent Kind = "subagent"
)

// Totals is a bundle of cumulative token and cost counters used both on
// Session (running totals) and as deltas passed to UpdateSessionTotals.
type Totals struct {
	InputTokens      int64
	OutputTokens     int64
	CacheReadTokens  int64
	CacheWriteTokens int64
	CostUSD          float64
}

// Session is a single conversation, optionally rooted at a parent fork point.
type Session struct {
	ID                  string
	ParentID            string // "" = root session
	ForkMessageID       string // "" if no parent
	ParentToolUseID     string // subagent sessions: parent task tool_use_id
	Slug                string
	ProjectDir          string
	Model               ModelRef
	Kind                Kind   // "primary" or "subagent"; empty on read means "primary"
	Totals              Totals // rolled-up totals: includes all descendant subagents
	OwnTotals           Totals // own totals: direct messages of this session only
	CreatedAt           time.Time
	UpdatedAt           time.Time
	DeletedAt           time.Time // zero value if not deleted
	Metadata            json.RawMessage
	FirstMessagePreview string // "" when no user message exists yet
}

// Role identifies who produced a message.
type Role string

// Recognised roles.  The DB CHECK constraint enforces this exact set.
const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
	RoleSystem    Role = "system"
)

// Message is a single turn in a session.  See parts.go for the Part union
// stored in Parts.
type Message struct {
	ID               string
	SessionID        string
	Role             Role
	Parts            []Part
	InputTokens      int64 // 0 = unset (no provider usage data attached)
	OutputTokens     int64
	CacheReadTokens  int64
	CacheWriteTokens int64
	CostUSD          float64
	DurationMs       int64
	CreatedAt        time.Time
	DeletedAt        time.Time
}

// Marker is a compaction marker.  The latest marker on a session bounds the
// live context window: only messages newer than BeforeMessageID are sent to
// the model.
type Marker struct {
	ID               string
	SessionID        string
	BeforeMessageID  string
	Summary          string
	InputTokensSaved int64
	CreatedAt        time.Time
}

// TodoStatus is the persisted status for a lightweight session todo item.
type TodoStatus string

const (
	// TodoPending means the todo has not started yet.
	TodoPending TodoStatus = "pending"
	// TodoInProgress means the todo is currently being worked.
	TodoInProgress TodoStatus = "in_progress"
	// TodoCompleted means the todo finished successfully.
	TodoCompleted TodoStatus = "completed"
	// TodoCancelled means the todo is no longer planned.
	TodoCancelled TodoStatus = "cancelled"
)

// TodoItem is one lightweight agent todo item scoped to a session.
type TodoItem struct {
	Content  string     `json:"content"`
	Status   TodoStatus `json:"status"`
	Priority string     `json:"priority,omitempty"`
}

// TodoSummary is the aggregate state the UI needs for the status pill.
type TodoSummary struct {
	Total      int `json:"total"`
	Incomplete int `json:"incomplete"`
	InProgress int `json:"in_progress"`
	Completed  int `json:"completed"`
	Cancelled  int `json:"cancelled"`
}

// NewSession is the input to Store.CreateSession.  ID, CreatedAt, UpdatedAt
// are assigned by the Store.
type NewSession struct {
	ProjectDir      string
	Model           ModelRef
	Slug            string // optional
	ParentID        string // optional; "" = root session
	ForkMessageID   string // required when ParentID is set AND Kind != KindSubagent
	ParentToolUseID string // optional; subagent parent task tool_use_id
	Kind            Kind   // empty defaults to KindPrimary
}

// NewMessage is the input to Store.AppendMessage.  ID and CreatedAt are
// assigned by the Store.
type NewMessage struct {
	Role             Role
	Parts            []Part
	InputTokens      int64 // 0 = unset
	OutputTokens     int64
	CacheReadTokens  int64
	CacheWriteTokens int64
	CostUSD          float64
	DurationMs       int64
}

// ListOpts controls ListSessions.
type ListOpts struct {
	ProjectDir     string // filter; "" = all directories
	IncludeDeleted bool
	Limit          int // 0 = use default (50)

	// Kind filters by session kind.  Empty matches all kinds (existing
	// behaviour).  Pass KindPrimary to hide subagent sessions from a
	// top-level listing.
	Kind Kind

	// ParentID filters to children of the given session.  "" matches
	// sessions regardless of parent.  Combine with Kind=KindSubagent
	// to list a parent's subagent invocations.
	ParentID string

	// Query is a case-insensitive substring filter matched against
	// slug, project_dir, and the first user-message preview text.
	// Empty matches all sessions.  Applied in Go after the SQL scan.
	Query string
}

// DefaultListLimit is the cap applied by ListSessions when ListOpts.Limit is 0.
const DefaultListLimit = 50

// Store persists sessions, messages, and compaction markers.  Concrete
// implementations live in other packages (currently only internal/store).
//
// Implementations are expected to be safe for concurrent use by multiple
// goroutines.
type Store interface {
	// CreateSession creates a new session.  When in.ParentID is non-empty
	// and in.Kind is not KindSubagent, in.ForkMessageID must also be
	// non-empty and reference a message owned by the parent session.
	// Subagent sessions (Kind == KindSubagent) may have a ParentID
	// without a ForkMessageID.
	CreateSession(ctx context.Context, in NewSession) (*Session, error)

	// GetSession returns the session with the given id.  Deleted sessions
	// are still returned (with a non-zero DeletedAt).
	GetSession(ctx context.Context, id string) (*Session, error)

	// ListSessions returns sessions matching opts, newest first.
	ListSessions(ctx context.Context, opts ListOpts) ([]*Session, error)

	// UpdateSessionTotals adds delta to the session's running totals.  The
	// update is atomic; callers do not need to read-modify-write.  Also
	// bumps UpdatedAt.
	UpdateSessionTotals(ctx context.Context, id string, delta Totals) error

	// PropagateTotals adds delta to every session in the parent chain
	// starting at id (inclusive) and walking up via parent_id.  The
	// walk is capped at 32 hops to guard against accidental cycles.
	// All updates happen in a single SQL transaction so the chain is
	// updated atomically.
	//
	// Returns the slice of session ids that were updated, ordered
	// leaf-first (id is index 0; the root ancestor is last).  The
	// caller uses this list to publish CostUpdated events for each
	// ancestor so the TUI footer — which subscribes to the root id —
	// sees the rolled-up total.
	//
	// Sessions that existed before T2.1 keep their prior (un-rolled-up)
	// totals; only new deltas go through the chain walk.
	PropagateTotals(ctx context.Context, id string, delta Totals) ([]string, error)

	// SoftDeleteSession marks the session and bumps UpdatedAt.  Already
	// deleted sessions are left untouched (no error).
	SoftDeleteSession(ctx context.Context, id string) error

	// RenameSession sets a new slug on an existing session and bumps
	// UpdatedAt.  An empty slug clears the slug.  Returns ErrNotFound
	// when id is unknown.  A no-op when the new slug is identical to
	// the current one (UpdatedAt is not bumped in that case).
	RenameSession(ctx context.Context, id, slug string) error

	// LatestUserMessageID returns the id of the most recent non-deleted
	// user-role message in sessionID, or ("", nil) when none exist.
	// Used by the fork-at-latest path to resolve the fork point without
	// requiring the caller to walk the full message list.
	LatestUserMessageID(ctx context.Context, sessionID string) (string, error)

	// ForkSession creates a new child session that inherits history from
	// fromSessionID up to and including fromMessageID.
	ForkSession(ctx context.Context, fromSessionID, fromMessageID string, model ModelRef, slug string) (*Session, error)

	// AppendMessage adds a message to the session and returns the persisted row.
	AppendMessage(ctx context.Context, sessionID string, in NewMessage) (*Message, error)

	// GetMessage returns the message with the given id (deleted or not).
	GetMessage(ctx context.Context, id string) (*Message, error)

	// MessagesForSession returns the full conversation history for the
	// session, walking the fork chain up to its root.  Excludes
	// soft-deleted messages.  Returns messages in chronological order.
	MessagesForSession(ctx context.Context, sessionID string) ([]*Message, error)

	// MessagesDirectForSession returns only the messages directly owned by
	// sessionID (no fork-chain walking).  Excludes soft-deleted messages.
	// Returns messages in chronological order.  Used for session kinds
	// (e.g. KindSubagent) that start with a fresh history rather than
	// inheriting a parent's prefix.
	MessagesDirectForSession(ctx context.Context, sessionID string) ([]*Message, error)

	// MessagesSinceLatestMarker returns the messages newer than the
	// session's latest compaction marker, plus the marker itself.  When the
	// session has no marker, returns the full MessagesForSession output and
	// a nil marker.
	MessagesSinceLatestMarker(ctx context.Context, sessionID string) ([]*Message, *Marker, error)

	// AddCompactionMarker records a new compaction cut-off for the session.
	AddCompactionMarker(ctx context.Context, sessionID string, beforeMessageID, summary string, tokensSaved int64) (*Marker, error)

	// LatestMarker returns the most recent compaction marker for the
	// session, or (nil, nil) when there are none.
	LatestMarker(ctx context.Context, sessionID string) (*Marker, error)

	// ListMarkersForSession returns all compaction markers for the session
	// in chronological order (oldest first).  Returns an empty slice (not
	// an error) when no markers exist.
	ListMarkersForSession(ctx context.Context, sessionID string) ([]*Marker, error)

	// ReplaceSessionTodos stores the full current todo list for a session.
	ReplaceSessionTodos(ctx context.Context, sessionID string, items []TodoItem) (TodoSummary, error)

	// GetSessionTodos returns the persisted todo list and summary for a session.
	GetSessionTodos(ctx context.Context, sessionID string) ([]TodoItem, TodoSummary, error)

	// Close releases backing resources.  Safe to call multiple times.
	Close() error
}

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
	ID            string
	ParentID      string // "" = root session
	ForkMessageID string // "" if no parent
	Slug          string
	ProjectDir    string
	Model         ModelRef
	Kind          Kind // "primary" or "subagent"; empty on read means "primary"
	Totals        Totals
	CreatedAt     time.Time
	UpdatedAt     time.Time
	DeletedAt     time.Time // zero value if not deleted
	Metadata      json.RawMessage
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

// NewSession is the input to Store.CreateSession.  ID, CreatedAt, UpdatedAt
// are assigned by the Store.
type NewSession struct {
	ProjectDir    string
	Model         ModelRef
	Slug          string // optional
	ParentID      string // optional; "" = root session
	ForkMessageID string // required when ParentID is set AND Kind != KindSubagent
	Kind          Kind   // empty defaults to KindPrimary
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

	// SoftDeleteSession marks the session and bumps UpdatedAt.  Already
	// deleted sessions are left untouched (no error).
	SoftDeleteSession(ctx context.Context, id string) error

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

	// Close releases backing resources.  Safe to call multiple times.
	Close() error
}

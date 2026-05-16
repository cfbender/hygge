// Package ui contains the bubbletea TUI shell for Hygge.
//
// # Layering
//
// internal/ui may import: internal/agent, internal/bus, internal/cost,
// internal/session, internal/ui/theme, and stdlib.  It must NOT import
// anything in cmd/.  Task 13's main.go wraps the App in a tea.Program.
//
// # The App is a Model, not a Program
//
// New(opts) returns an *App that implements tea.Model (Init/Update/View).
// The CLI is responsible for constructing a *tea.Program around it.  This
// keeps internal/ui drivable from tests without touching a TTY.
//
// # Bus bridge
//
// The App owns a single goroutine that fans every event we care about from
// the typed bus subscriptions into one `chan any`.  Init returns a tea.Cmd
// that reads one event off that channel and wraps it in a [busDelivery]
// Msg; Update re-issues the same Cmd on every delivery, creating an
// infinite read-loop entirely inside the bubbletea Cmd machinery.  No
// program-side Send is needed.
//
// Close cancels the bridge context so the goroutine exits and tests can
// avoid leaks.
package ui

import (
	"context"
	"errors"
	"fmt"
	"image/color"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/glamour"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/cfbender/hygge/internal/agent"
	"github.com/cfbender/hygge/internal/bus"
	"github.com/cfbender/hygge/internal/command"
	"github.com/cfbender/hygge/internal/config"
	"github.com/cfbender/hygge/internal/cost"
	"github.com/cfbender/hygge/internal/notify"
	"github.com/cfbender/hygge/internal/provider"
	"github.com/cfbender/hygge/internal/session"
	appstate "github.com/cfbender/hygge/internal/state"
	"github.com/cfbender/hygge/internal/ui/components"
	"github.com/cfbender/hygge/internal/ui/components/anim"
	"github.com/cfbender/hygge/internal/ui/styles"
	"github.com/cfbender/hygge/internal/ui/theme"
)

// AppOptions configures the App.
type AppOptions struct {
	Bus     *bus.Bus
	Agent   *agent.Agent
	Store   session.Store
	Catalog *cost.Catalog
	Theme   *theme.Theme
	// StyleTheme selects the built-in color theme for the new styles system.
	StyleTheme string

	// Modes is the ordered list of agent modes the user can cycle through
	// with Tab. Each mode specifies a provider, model, and optional
	// reasoning/prompt. Guaranteed non-empty after config loading.
	Modes         []config.ModeConfig
	SessionID     string // existing session to resume, or "" to create on first input
	ProjectDir    string
	ModelProvider string // "anthropic" etc, for status bar display
	ModelName     string
	ProfileName   string
	Reasoning     provider.Reasoning
	Commands      *command.Registry // slash-command registry; nil disables slash routing
	Subagents     []MentionSubagent // sub-agent types selectable from @ mentions
	Now           func() time.Time
	// ContextWindow is the model's maximum context size in tokens.  Used by
	// the compaction modal to display usage info.  0 means unknown.
	ContextWindow int64

	// Version is the application version string for the header bar (e.g. "v0.4").
	// Empty string hides the version.
	Version string

	// NerdFonts controls whether nerd-font glyphs are used in the header bar.
	// When true, the git-branch glyph (U+EAFC) is used; otherwise ":branch".
	// Default false; callers should set this from config.UI.NerdFonts.
	NerdFonts bool

	// HomeDir is the user's home directory, used for tilde-collapsing the
	// project path in the header bar.  Empty → no collapse.
	HomeDir string

	// OnSessionCreated, if non-nil, is invoked after the App lazily
	// creates a new session on first Send.  The CLI uses this to record
	// the new id in state (RecentSessions).  Best-effort; errors are
	// swallowed internally.
	OnSessionCreated func(id string)

	// MCPStatuses is the list of MCP server statuses populated at bootstrap.
	// Displayed in the sidebar.  The UI-side type is SidebarMCPStatus (defined
	// in components/sidebar.go) so the UI package has no dependency on cmd/.
	MCPStatuses []components.SidebarMCPStatus

	// OpenSessionsModalOnStart, when true, causes the sessions picker to
	// open immediately after the first render.  Used by `hygge resume`
	// (multiple sessions in cwd) and resume_default="ask".  When the
	// picker is opened this way and the user presses Esc without selecting
	// a session — and no foreground session is bound — the App exits.
	OpenSessionsModalOnStart bool

	// Config is the resolved application configuration.  When non-nil,
	// the notifications subsystem reads Config.Notifications to decide
	// whether and when to send notifications.  A nil Config disables
	// notifications (equivalent to Config.Notifications.Enabled == false).
	Config *config.Config

	// SwitchModel applies a provider/model selection to the running backend.
	// When nil, /model remains a session-only UI selection.
	SwitchModel func(ctx context.Context, providerName, modelName string) error
	// SaveModel persists a successful provider/model runtime switch.  Save
	// failures are surfaced to the UI without rolling back the runtime switch.
	SaveModel  func(ctx context.Context, providerName, modelName string) error
	SaveAPIKey func(ctx context.Context, providerName, apiKey string) error
	ThemeNames []string
	LoadTheme  func(ctx context.Context, name string) (*theme.Theme, error)
	SaveTheme  func(ctx context.Context, name string) error
	// EditPrompt opens the current prompt in an external editor and returns the
	// edited prompt. Tests may inject this seam; production falls back to
	// $VISUAL, then $EDITOR, then vi.
	EditPrompt func(ctx context.Context, initial string) (string, error)
	// Yolo bypasses configurable permission prompts/default denies while keeping
	// the hard-coded secrets denylist active.
	Yolo    bool
	SetYolo func(ctx context.Context, enabled bool) error
}

// uiMessage is the App's internal alias for the components.UIMessage view
// model.  Kept here so appmsg.go and tests can refer to it without importing
// the components package.
type uiMessage = components.UIMessage

// SidebarMCPStatus is a re-export of components.SidebarMCPStatus so that
// cmd/hygge/cli/run.go can reference the type without importing the
// internal/ui/components package directly.  See AppOptions.MCPStatuses.
type SidebarMCPStatus = components.SidebarMCPStatus

const queuedDraftHitRows = 3

// New constructs the App.  Validates required fields and starts the bus
// bridge goroutine.
func New(opts AppOptions) (*App, error) {
	if opts.Bus == nil {
		return nil, errors.New("ui: New: Bus is required")
	}
	if opts.Theme == nil {
		opts.Theme = theme.ShellTheme()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}

	// Select the notification backend based on config.
	var nb notify.Backend = notify.NoopBackend{}
	if opts.Config != nil && opts.Config.Notifications.Enabled {
		nb = notify.NativeBackend{}
	}

	// Initialize the styles system from the selected theme.
	themeStyles := styles.ThemeByName(opts.StyleTheme)

	ctx, cancel := context.WithCancel(context.Background())
	inp := components.NewInput(opts.Theme)
	inp.SetStyles(&themeStyles)

	a := &App{
		opts:          opts,
		styles:        &themeStyles,
		ctx:           ctx,
		cancel:        cancel,
		busCh:         make(chan any, 256),
		input:         inp,
		spinner:       spinner.New(),
		width:         80,
		height:        24,
		msgColW:       61, // default: bubble content at 80 cols (int(80*0.80)-3)
		subagents:     make(map[string]*components.SubagentState),
		subagentAnims: make(map[string]*anim.Anim),
		expandedTools: make(map[string]bool),
		msgViewport:   viewport.New(viewport.WithWidth(80), viewport.WithHeight(20)),
		touched:       appstate.NewTouchedFiles(),
		focused:       true,
		notifyBackend: nb,
	}
	a.msgViewport.MouseWheelEnabled = true
	a.spinner.Spinner = spinner.Meter
	if themeStyles.WorkingGradFromColor != nil {
		a.spinner.Style = lipgloss.NewStyle().Foreground(themeStyles.WorkingGradFromColor)
	}
	a.history = newInputHistory(xdgStateHome(opts.HomeDir))
	a.initModes()
	if opts.SessionID != "" || !opts.OpenSessionsModalOnStart {
		a.bridge()
	}
	return a, nil
}

// App is the root bubbletea model.
type App struct {
	opts AppOptions

	ctx    context.Context
	cancel context.CancelFunc

	// busCh receives every event the bridge goroutine has multiplexed.
	busCh chan any

	// width and height come from tea.WindowSizeMsg.
	width  int
	height int

	// styles is the resolved theme style system.
	styles *styles.Styles

	// modeIndex is the index into opts.Modes for the currently active mode.
	// Always >= 0; Modes is guaranteed non-empty after config loading.
	modeIndex int

	// history tracks previously sent inputs for up-arrow recall.
	history *inputHistory

	// toast is the active notification shown in the top-left corner.
	toast        *toast
	toastCounter int

	// lastEscAt records when Esc was last pressed for double-Esc detection.
	lastEscAt time.Time
	// quitSelectedNo tracks which button is selected in the quit overlay.
	quitSelectedNo bool

	// expandedTools tracks which tool results are fully expanded (not truncated).
	expandedTools map[string]bool

	// sel tracks mouse-driven text selection.
	sel selection
	// lastCanvas is the most recently rendered screen buffer, kept for
	// extracting selected text on mouse release.
	lastCanvas uv.ScreenBuffer

	// layout holds the pre-computed screen regions for the current frame.
	// Recalculated on resize and when dynamic element heights change.
	layout uiLayout

	// msgColW is the glamour word-wrap width: the inner content width of
	// assistant bubbles. Bubbles are 80% of the left column width and lose
	// 1 column to their side accent bar plus 2 columns to horizontal padding, so:
	//   msgColW = int(float64(leftW) * 0.80) - 3
	// Updated alongside a.width in the WindowSizeMsg handler and kept in
	// sync in View().  Glamour is rendered at this width so markdown lines
	// never overflow the bubble's inner area.
	msgColW int

	// renderer is the glamour TermRenderer; rebuilt on resize.
	renderer  *glamour.TermRenderer
	rendererW int

	// messages is the conversation buffer.
	messages []uiMessage

	// msgCache holds the pre-rendered message list content. Invalidated when
	// messages change (append, stream delta, resize). This avoids re-rendering
	// all messages on every frame — only the viewport scroll position changes.
	msgCache         string
	msgCacheValid    bool
	msgCacheW        int       // width at which cache was rendered
	msgCacheLen      int       // message count at which cache was rendered
	msgCacheTime     time.Time // time at which cache was rendered (for relative timestamps)
	subagentHitZones []components.SubagentHitZone
	toolHitZones     []components.ToolHitZone

	// hoverSubagentID is the subagent ID under the mouse cursor, or "".
	hoverSubagentID string

	// msgViewport is the fixed-height scrollable container for the message list.
	// Its Height is recomputed on every WindowSizeMsg and View() call so it
	// adapts as chrome elements (banner, notice, etc.) appear and disappear.
	msgViewport viewport.Model

	// userScrolled tracks whether the user has manually scrolled up from the
	// bottom of the message list.  When true, new incoming messages do NOT
	// auto-scroll to the bottom; the user's position is preserved.  It is
	// reset to false when the user presses Enter (sends a message) or when
	// the viewport is programmatically scrolled to the bottom.
	userScrolled bool

	// permission state
	pendingPerms []components.PermissionRequest // FIFO queue
	modalToast   string                         // transient message inside the modal
	overlays     overlayStack                   // typed topmost-first dialog routing foundation

	// status state
	busy        bool
	spinner     spinner.Model
	spinnerTick int
	workingVerb string

	// cost / context state
	costDollars float64
	usedTok     int64
	maxTok      int64
	pctUsed     float64

	// Terminal colours reported by Bubble Tea. For the shell theme, Hygge uses
	// these to derive a subtle surface fill close to the user's real terminal bg.
	terminalBg color.Color
	terminalFg color.Color

	// input + send state
	input *components.Input
	// inflightCancel cancels the current Send.
	inflightCancel context.CancelFunc

	// notice is the ephemeral status line raised by slash commands
	// and surfaced briefly under the input.  Cleared on a timer or
	// the next slash invocation.
	notice string

	// pendingAttachments are one-shot local files included with the next user
	// message. They clear after Agent.Send accepts the turn/enqueue.
	pendingAttachments []promptAttachment
	// pastedInputBlocks keeps multi-line paste contents out of the editor while
	// preserving the full pasted text for the next send.
	pastedInputBlocks []pastedInputBlock

	// paletteHighlight is the current row index into the active
	// command palette matches.  -1 means "no row highlighted".
	// Reset on every buffer change.
	paletteHighlight int
	// slashPaletteDismissed tracks an Esc-dismissed slash palette until
	// the next input edit. The typed slash buffer is preserved.
	slashPaletteDismissed bool
	// mentionHighlight is the current row index into active @ mention matches.
	mentionHighlight int
	// mentionDismissed tracks an Esc-dismissed @ mention palette until the next
	// input edit. The typed @ token is preserved.
	mentionDismissed bool
	// mentionFileCache caches project-relative file paths for @ file mentions.
	mentionFileRoot  string
	mentionFileCache []string

	// activeModal mirrors the top non-permission overlay for existing tests and
	// compatibility; overlay routing/rendering uses overlays.
	activeModal string

	// sessionsModal holds the live state of the sessions picker
	// when activeModal == "sessions".
	sessionsModal components.SessionsModal
	modelModal    components.ModelModal
	apiKeyModal   components.APIKeyModal
	themeModal    components.ThemeModal

	// forkPendingID and forkPendingMsgID are set by applyUpdate when a
	// /fork outcome is received.  applyOutcome drains them after all
	// Outcome fields have been processed and generates the fork tea.Cmd.
	forkPendingID    string
	forkPendingMsgID string

	// subagents tracks every in-flight or completed sub-agent
	// invocation whose root ancestor is opts.SessionID.  Keyed by
	// sub-session id.  Populated on bus.SubagentStarted, updated by
	// the sub-session's normal streaming events, finalised on
	// bus.SubagentCompleted.  Stage A blocks recursion at the
	// runtime layer so depth is currently at most 1 -- but
	// isDescendant() below walks the chain so future relaxation
	// does not break the filter.
	subagents map[string]*components.SubagentState

	// subagentAnims holds one Anim per running sub-agent, keyed by
	// SubSessionID.  Created on SubagentStarted (live events only —
	// resumed sessions always have EndedAt set and never create an Anim).
	// Deleted on SubagentCompleted to stop unnecessary ticking.
	subagentAnims map[string]*anim.Anim

	// foregroundStack tracks the navigation history for the TUI.
	// The bottom entry (index 0) is always the root session.
	// The top entry (last index) is the currently-foregrounded session.
	//
	// Operations:
	//   pushForeground(id): appends id to the top.
	//   popForeground():    removes the top; no-op when len == 1 (never
	//                       pops the root).
	//   resetForeground(id): replaces the entire stack with [id]; used by
	//                        the sessions modal "switch" action so the
	//                        chosen session becomes the new root and
	//                        breadcrumb is cleared.
	//
	// When the stack is empty (App not yet bound to a session), foregroundID()
	// returns opts.SessionID to preserve the pre-T2.2 lazy-create behaviour.
	foregroundStack []string

	// --- Compaction state (T2.3) ---

	// compactionModal holds the live state of the compaction confirmation
	// modal when activeModal == command.ModalCompactConfirm.
	compactionModal components.CompactionModal

	// compactionInFlight is true while Agent.Compact is running (between
	// CompactionStarted and Completed/Failed events).
	compactionInFlight bool

	// compactionInFlightCount is the number of messages being compacted,
	// carried from CompactionStarted so the Completed toast can display it.
	compactionInFlightCount int

	// compactionToast is the post-compaction result message shown for 5s.
	// Empty means no toast is showing.
	compactionToast string

	// bannerVisible is true when the threshold-suggestion banner should be
	// shown above the input.
	bannerVisible bool

	// bannerPct is the context-usage percentage carried into the banner.
	bannerPct float64

	// bannerDismissed is true when the user pressed Ctrl+X to dismiss the
	// banner for the current crossing.  Cleared when compaction completes or
	// usage crosses below the hysteresis level (reset by a new
	// CompactionRequested{Source: "threshold"} event).
	bannerDismissed bool

	// program is the bubbletea Program that owns this App.  Set by
	// SetProgram after tea.NewProgram returns.  Used by sendOutOfBand to
	// inject messages from goroutines that run outside the bubbletea event
	// loop (e.g. the Agent.Send goroutine launched in startSend).  Nil in
	// unit tests — sendOutOfBand is a no-op when program is nil.
	program *tea.Program

	// testAgentSendFn, when non-nil, is called by startSend's goroutine
	// instead of opts.Agent.Send.  Used exclusively by unit tests to inject
	// a controllable stub without requiring a concrete *agent.Agent.  Must
	// not be set in production code.
	testAgentSendFn func(ctx context.Context, sessionID string, parts []session.Part) (*session.Message, error)

	// testAgentClearQueueFn, when non-nil, is called by the Esc handler
	// instead of opts.Agent.ClearQueue.  Used exclusively by unit tests.
	// Must not be set in production code.
	testAgentClearQueueFn func(sessionID string) int

	// testSendFn, when non-nil, is called by sendOutOfBand instead of
	// program.Send.  Used exclusively by unit tests that cannot wire a
	// *tea.Program.  Must not be set in production code.
	testSendFn func(tea.Msg)

	// closed protects against double Close.
	closeOnce sync.Once

	// touched tracks absolute paths of files written or edited during the
	// session.  Populated on bus.ToolCallCompleted for write/edit tools.
	touched *appstate.TouchedFiles

	// todosCache is the most-recently-loaded sidebar todo list for the
	// foreground session. Refreshed on TodoChanged for the foreground
	// session and on session switches via hydrateTodoSummary's caller.
	todosCache []components.SidebarTodo

	// sessionTitle is a cached copy of the sidebar session display title
	// (FirstMessagePreview > Slug > first-12-chars of ID).  Populated at
	// Init (resume path), ensureSession (new-session path), and on
	// bus.MessageAppended for the root session so View() never calls
	// Store.GetSession synchronously on the render goroutine.
	sessionTitle string

	// focused tracks terminal focus state.  true means the terminal window
	// is focused (user is looking at it); false means it is blurred.
	// Defaults to true (assume focused until told otherwise — we'd rather
	// suppress a notification the user sees than miss one while they're away).
	focused bool

	// notifyBackend is the active notification backend.  Selected at New
	// time based on config.Notifications.Enabled: NativeBackend when
	// enabled, NoopBackend otherwise.
	notifyBackend notify.Backend

	// queueCount is the number of pending sends in the agent queue for the
	// foreground session.  Updated on bus.QueueChanged.
	queueCount int

	// queuedPrompts holds the prompt texts for queued sends (for display).
	// Updated on bus.QueueChanged.
	queuedPrompts []string
	queuedDrafts  []queuedPromptDraft
	// queuedDraftEditing remembers the original queue index for a draft being
	// edited in the input. Submitting while busy reinserts at this index.
	queuedDraftEditing bool
	queuedDraftEditIdx int

	// optimisticUserPending is true after startSend renders the active turn's
	// user prompt before the store confirms it. Queued prompts do not set this;
	// they stay in queuedPrompts until their persisted MessageAppended event.
	optimisticUserPending bool

	// todoIncomplete/todoInProgress summarize the foreground session's agent
	// todo list. Updated on bus.TodoChanged.
	todoIncomplete int
	todoInProgress int

	// activeTurns is the number of agent turns currently executing for the
	// foreground session.  Incremented on bus.TurnStarted; decremented on
	// bus.TurnCompleted.  The UI flips busy=false only when activeTurns
	// reaches zero AND queueCount is also zero, so the "Thinking…"
	// placeholder stays on through the brief gap between one turn completing
	// and the next queued turn's TurnStarted arriving.
	activeTurns int
}

// Init is the bubbletea Model entry point.  Starts the input focus, the
// spinner tick, and the bus listener.
func (a *App) Init() tea.Cmd {
	cmds := []tea.Cmd{
		a.input.Textarea.Focus(),
		a.spinner.Tick,
		tea.RequestBackgroundColor,
		tea.RequestForegroundColor,
	}
	// Only start the bus listener when the bridge is running (i.e. a
	// foreground session is already bound or OpenSessionsModalOnStart
	// is false).
	if a.opts.SessionID != "" || !a.opts.OpenSessionsModalOnStart {
		cmds = append(cmds, a.listenBus())
	}
	if a.opts.OpenSessionsModalOnStart {
		// Initialise the modal and schedule a load.
		a.openOverlay(overlaySessions)
		a.sessionsModal = components.SessionsModal{
			Theme:        a.opts.Theme,
			ForegroundID: a.opts.SessionID,
			AllowNew:     true,
		}
		a.updateInputFocus()
		cmds = append(cmds, a.openSessionsModal())
	}
	// When resuming an existing session, pre-populate the message list from
	// the persisted store so the user sees history before typing anything.
	// Also seed the session title cache so the sidebar never blocks on
	// Store.GetSession during View().
	if a.opts.SessionID != "" {
		a.foregroundStack = []string{a.opts.SessionID}
		a.hydrateMessagesFromStore(a.opts.SessionID)
		a.sessionTitle = a.loadSessionTitle(a.opts.SessionID)
		a.hydrateTodoSummary(a.opts.SessionID)
	}
	return tea.Batch(cmds...)
}

// Close releases the bridge goroutine and any in-flight Send.  Idempotent.
// Tests call this in t.Cleanup.
func (a *App) Close() error {
	a.closeOnce.Do(func() {
		if a.inflightCancel != nil {
			a.inflightCancel()
		}
		a.cancel()
	})
	return nil
}

// SetProgram stores the tea.Program so that goroutines started by startSend
// can inject messages back into the bubbletea event loop via program.Send.
// Must be called before the first Update that triggers a send.  The CLI calls
// it immediately after tea.NewProgram.  Tests leave it unset; sendOutOfBand is
// a no-op when program is nil, so tests drive sendCompleted manually via
// app.Update(sendCompleted{...}).
func (a *App) SetProgram(p *tea.Program) {
	a.program = p
}

// sendOutOfBand injects msg into the bubbletea event loop from a goroutine
// running outside the normal Update path.  Safe to call from any goroutine.
// Uses testSendFn when set (unit tests); falls back to program.Send in
// production; no-op when both are nil.
func (a *App) sendOutOfBand(msg tea.Msg) {
	if a.testSendFn != nil {
		a.testSendFn(msg)
		return
	}
	if a.program != nil {
		a.program.Send(msg)
	}
}

// bridge subscribes to every bus event type the App cares about and starts a
// goroutine per type that forwards them into a.busCh.
//
// Subscriptions are created synchronously, BEFORE bridge returns.  This
// closes the obvious race where a publish issued immediately after New()
// would otherwise land before any of the subscribers existed.  Each
// goroutine exits when either the subscription channel is closed
// (Bus.Close / Unsubscribe) or the App's context is cancelled.
//
// Subagent filtering strategy (approach A):
//
//	The App subscribes once per event type globally and filters per
//	delivery against the active set of foreground + descendant
//	session ids.  This is simpler than spawning per-sub-session
//	subscribers on the fly (approach B in the design doc) and the
//	per-message branch is O(1) for the depth bound we currently
//	enforce in the runtime (≤1 level).  isDescendant walks the
//	chain so the filter still works if recursion is ever
//	relaxed.
func (a *App) bridge() {
	// Subscribe synchronously so that any Publish issued after New()
	// returns is guaranteed to find a live subscriber.
	subDelta := bus.Subscribe[bus.AssistantTextDelta](a.opts.Bus, bus.SubscribeOptions{BufferSize: 256})
	subThink := bus.Subscribe[bus.AssistantThinkingDelta](a.opts.Bus, bus.SubscribeOptions{BufferSize: 256})
	subAppended := bus.Subscribe[bus.MessageAppended](a.opts.Bus, bus.SubscribeOptions{BufferSize: 128})
	subToolReq := bus.Subscribe[bus.ToolCallRequested](a.opts.Bus, bus.SubscribeOptions{BufferSize: 64})
	subToolDone := bus.Subscribe[bus.ToolCallCompleted](a.opts.Bus, bus.SubscribeOptions{BufferSize: 64})
	subCost := bus.Subscribe[bus.CostUpdated](a.opts.Bus, bus.SubscribeOptions{BufferSize: 64})
	subCtx := bus.Subscribe[bus.ContextUsageUpdated](a.opts.Bus, bus.SubscribeOptions{BufferSize: 64})
	subPerm := bus.Subscribe[bus.PermissionAsked](a.opts.Bus, bus.SubscribeOptions{BufferSize: 32})
	subPermReplied := bus.Subscribe[bus.PermissionReplied](a.opts.Bus, bus.SubscribeOptions{BufferSize: 32})
	subMCPStatus := bus.Subscribe[bus.MCPStatusUpdated](a.opts.Bus, bus.SubscribeOptions{BufferSize: 32})
	subIter := bus.Subscribe[bus.IterationLimitReached](a.opts.Bus, bus.SubscribeOptions{BufferSize: 8})
	subSubStart := bus.Subscribe[bus.SubagentStarted](a.opts.Bus, bus.SubscribeOptions{BufferSize: 16})
	subSubDone := bus.Subscribe[bus.SubagentCompleted](a.opts.Bus, bus.SubscribeOptions{BufferSize: 16})
	subCmpReq := bus.Subscribe[bus.CompactionRequested](a.opts.Bus, bus.SubscribeOptions{BufferSize: 8})
	subCmpStart := bus.Subscribe[bus.CompactionStarted](a.opts.Bus, bus.SubscribeOptions{BufferSize: 8})
	subCmpDone := bus.Subscribe[bus.CompactionCompleted](a.opts.Bus, bus.SubscribeOptions{BufferSize: 8})
	subCmpFail := bus.Subscribe[bus.CompactionFailed](a.opts.Bus, bus.SubscribeOptions{BufferSize: 8})
	subQueueChanged := bus.Subscribe[bus.QueueChanged](a.opts.Bus, bus.SubscribeOptions{BufferSize: 32})
	subTodoChanged := bus.Subscribe[bus.TodoChanged](a.opts.Bus, bus.SubscribeOptions{BufferSize: 32})
	subTurnStarted := bus.Subscribe[bus.TurnStarted](a.opts.Bus, bus.SubscribeOptions{BufferSize: 16})
	subTurnDone := bus.Subscribe[bus.TurnCompleted](a.opts.Bus, bus.SubscribeOptions{BufferSize: 16})

	stop := a.ctx.Done()

	// One forwarder goroutine per type.  The body is identical in shape;
	// generics over the channel element type would be cleaner but Go
	// closures cannot capture generic type parameters, so each call is
	// type-instantiated explicitly.
	go forward(subDelta.C(), a.busCh, stop, subDelta.Unsubscribe)
	go forward(subThink.C(), a.busCh, stop, subThink.Unsubscribe)
	go forward(subAppended.C(), a.busCh, stop, subAppended.Unsubscribe)
	go forward(subToolReq.C(), a.busCh, stop, subToolReq.Unsubscribe)
	go forward(subToolDone.C(), a.busCh, stop, subToolDone.Unsubscribe)
	go forward(subCost.C(), a.busCh, stop, subCost.Unsubscribe)
	go forward(subCtx.C(), a.busCh, stop, subCtx.Unsubscribe)
	go forward(subPerm.C(), a.busCh, stop, subPerm.Unsubscribe)
	go forward(subPermReplied.C(), a.busCh, stop, subPermReplied.Unsubscribe)
	go forward(subMCPStatus.C(), a.busCh, stop, subMCPStatus.Unsubscribe)
	go forward(subIter.C(), a.busCh, stop, subIter.Unsubscribe)
	go forward(subSubStart.C(), a.busCh, stop, subSubStart.Unsubscribe)
	go forward(subSubDone.C(), a.busCh, stop, subSubDone.Unsubscribe)
	go forward(subCmpReq.C(), a.busCh, stop, subCmpReq.Unsubscribe)
	go forward(subCmpStart.C(), a.busCh, stop, subCmpStart.Unsubscribe)
	go forward(subCmpDone.C(), a.busCh, stop, subCmpDone.Unsubscribe)
	go forward(subCmpFail.C(), a.busCh, stop, subCmpFail.Unsubscribe)
	go forward(subQueueChanged.C(), a.busCh, stop, subQueueChanged.Unsubscribe)
	go forward(subTodoChanged.C(), a.busCh, stop, subTodoChanged.Unsubscribe)
	go forward(subTurnStarted.C(), a.busCh, stop, subTurnStarted.Unsubscribe)
	go forward(subTurnDone.C(), a.busCh, stop, subTurnDone.Unsubscribe)
}

// forward pumps a single typed subscription channel into the shared any
// channel until either source is exhausted or the App context is cancelled.
func forward[T any](in <-chan T, out chan<- any, stop <-chan struct{}, unsubscribe func()) {
	defer unsubscribe()
	for {
		select {
		case ev, ok := <-in:
			if !ok {
				return
			}
			select {
			case out <- ev:
			case <-stop:
				return
			}
		case <-stop:
			return
		}
	}
}

// listenBus is the bubbletea Cmd that reads ONE event off the bridge channel
// and wraps it in a busDelivery.  Update re-issues this Cmd on every
// delivery, creating an infinite read-loop inside the bubbletea machinery.
func (a *App) listenBus() tea.Cmd {
	return func() tea.Msg {
		select {
		case ev, ok := <-a.busCh:
			if !ok {
				return nil
			}
			return busDelivery{Event: ev}
		case <-a.ctx.Done():
			return nil
		}
	}
}

// Handle delivers a single bus event synchronously, exactly as if it had
// arrived via the listener.  Used by tests to drive the App without
// goroutines.  Returns the same tea.Cmd Update would.
func (a *App) Handle(ev any) tea.Cmd {
	model, cmd := a.Update(busDelivery{Event: ev})
	_ = model
	return cmd
}

// View renders the App.
func (a *App) View() tea.View {
	w := a.width
	if w <= 0 {
		w = 80
	}
	h := a.height
	if h <= 0 {
		h = 24
	}

	// Keep the editor width in sync before layout so wrapped input content can
	// contribute the correct dynamic height.
	if !a.viewingSubagent() {
		leftW := w - sidebarWidthForTerminal(w)
		a.input.SetWidth(inputWidthForLeft(leftW))
	}

	// Recompute layout regions for the current dimensions.
	a.layout = a.generateLayout(w, h)
	a.msgColW = a.layout.msgContentW

	// Render into a UV screen buffer.
	canvas := uv.NewScreenBuffer(w, h)
	cursor := a.Draw(canvas, canvas.Bounds())
	a.lastCanvas = canvas

	v := tea.NewView(canvas.Render())
	v.AltScreen = true
	v.MouseMode = tea.MouseModeAllMotion
	if a.styles != nil {
		v.BackgroundColor = a.styles.Background
	}
	if cursor != nil {
		v.Cursor = cursor
	}
	return v
}

// Update is the bubbletea Update method.
func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m := msg.(type) {

	case tea.FocusMsg:
		a.focused = true
		return a, nil

	case tea.BlurMsg:
		a.focused = false
		return a, nil

	case tea.WindowSizeMsg:
		a.width = m.Width
		a.height = m.Height
		a.invalidateMsgCache()
		// Compute the left column width accounting for the sidebar.
		sidebarW := sidebarWidthForTerminal(m.Width)
		leftW := m.Width - sidebarW
		// glamour word-wrap = bubble content width minus bubble chrome.
		a.msgColW = msgContentWidthForLeft(leftW)
		a.input.SetWidth(inputWidthForLeft(leftW))
		a.msgViewport.SetWidth(leftW)
		// Height is recomputed per-frame in View(); set a sane default here
		// so the viewport is usable before the first full render.
		if m.Height > 6 {
			a.msgViewport.SetHeight(m.Height - 6)
		}
		// Glamour renderer is sized to the body width; rebuild lazily.
		a.renderer = nil
		a.rendererW = 0
		a.rerenderFinalMarkdownMessages()
		return a, nil

	case tea.BackgroundColorMsg:
		a.terminalBg = m.Color
		return a, nil

	case tea.ForegroundColorMsg:
		a.terminalFg = m.Color
		return a, nil

	case tea.EnvMsg, tea.ColorProfileMsg, tea.TerminalVersionMsg, tea.ModeReportMsg, tea.KeyboardEnhancementsMsg:
		// Bubble Tea emits terminal capability/report messages during startup and
		// around mode changes. Consume them at the root so they never fall through
		// to the textarea component.
		return a, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		a.spinner, cmd = a.spinner.Update(m)
		a.spinnerTick++
		return a, cmd

	case workingVerbTickMsg:
		if !a.busy {
			return a, nil
		}
		a.workingVerb = components.RandomWorkingVerb()
		return a, a.workingVerbTick()

	case anim.StepMsg:
		// Route to the matching sub-agent anim.  The anims are keyed by
		// SubSessionID, but StepMsg.ID is the anim's own internal id.
		// Search the map to find the right anim.  If the sub-agent has
		// completed, the anim is already deleted; the StepMsg is dropped.
		for subID, an := range a.subagentAnims {
			updated, cmd := an.Update(m)
			if cmd != nil {
				// This anim consumed the message.
				a.subagentAnims[subID] = updated
				return a, cmd
			}
		}
		return a, nil

	case subagentTickMsg:
		// Re-issue the tick if the sub-agent is still running;
		// otherwise drop it.  No view-state change is needed -- the
		// next View() reads opts.Now() to recompute the elapsed
		// label.
		if st, ok := a.subagents[m.SubSessionID]; ok && st.IsRunning() {
			return a, a.subagentTick(m.SubSessionID)
		}
		return a, nil

	case clearToastMsg:
		a.modalToast = ""
		return a, nil

	case clearToastByID:
		a.handleToastClear(m.id)
		return a, nil

	case clearCompactionToastMsg:
		a.compactionToast = ""
		return a, nil

	case dismissBannerMsg:
		a.bannerDismissed = true
		return a, nil

	case compactionRunMsg:
		return a, a.startCompaction(m.SessionID)

	case compactionCompleteMsg:
		a.compactionInFlight = false
		a.compactionInFlightCount = 0
		if m.Err != nil {
			a.compactionToast = fmt.Sprintf("✕  Compaction failed: %s", m.Err.Error())
		} else {
			a.compactionToast = fmt.Sprintf("✓  Compacted %d messages → %d tokens summary.  Marker %s",
				m.MessagesCompacted, m.SummaryTokens, shortID(m.MarkerID))
		}
		return a, tea.Tick(5*time.Second, func(time.Time) tea.Msg { return clearCompactionToastMsg{} })

	case clearNoticeMsg:
		// Only clear when we are still showing the same notice the
		// timer was scheduled for — otherwise a fresher notice that
		// landed in the meantime would be wiped.
		if a.notice == m.notice {
			a.notice = ""
		}
		return a, nil

	case sessionsLoadedMsg:
		// Sessions loaded (or reloaded after rename/delete).
		a.sessionsModal.Sessions = m.sessions
		// Clamp cursor to avoid out-of-bounds after a delete.
		filtered := a.sessionsModal.FilteredCount()
		if a.sessionsModal.Cursor >= filtered && filtered > 0 {
			a.sessionsModal.Cursor = filtered - 1
		}
		return a, nil

	case switchSessionMsg:
		return a, a.applySwitchSession(m.ID)

	case modelSwitchResult:
		if m.err != nil {
			return a, a.setNotice("model switch failed: " + m.err.Error())
		}
		a.opts.ModelProvider = m.provider
		a.opts.ModelName = m.model
		if m.saveErr != nil {
			return a, a.setNotice("model switched for this session but save failed: " + m.saveErr.Error())
		}
		return a, a.showToast("Model switched", "Using "+m.provider+"/"+m.model)

	case apiKeySaveResult:
		if m.err != nil {
			a.openOverlay(overlayAPIKey)
			return a, a.setNotice("API key save failed for " + m.provider + ": " + m.err.Error())
		}
		return a, a.showToast("API key saved", "Provider: "+m.provider)

	case modeSwitchResult:
		if m.err != nil {
			return a, a.showToast("Mode Switch Failed", m.err.Error())
		}
		return a, nil // toast already shown by cycleMode

	case themeSwitchResult:
		if m.err != nil {
			return a, a.setNotice("theme switch failed: " + m.err.Error())
		}
		if m.theme != nil {
			a.opts.Theme = m.theme
			a.input.Theme = m.theme
			a.renderer = nil
			a.rendererW = 0
		}
		if m.saveErr != nil {
			return a, a.setNotice("theme applied for this session but save failed: " + m.saveErr.Error())
		}
		return a, a.showToast("Theme switched", "Using "+m.name)

	case promptEditorFinishedMsg:
		if m.err != nil {
			return a, a.setNotice("editor: " + m.err.Error())
		}
		a.setEditedPrompt(m.text)
		return a, nil

	case yoloSwitchResult:
		if m.err != nil {
			return a, a.setNotice("yolo mode failed: " + m.err.Error())
		}
		a.opts.Yolo = m.enabled
		if m.enabled {
			return a, a.showToast("Yolo mode", "Enabled — secrets still protected")
		}
		return a, a.showToast("Yolo mode", "Disabled")

	case sendStarted:
		wasBusy := a.busy
		a.busy = true
		a.workingVerb = components.RandomWorkingVerb()
		suffix := ""
		if a.queueCount > 0 {
			suffix = fmt.Sprintf(" (%d queued)", a.queueCount)
		}
		a.input.SetBusy(true, suffix)
		if !wasBusy {
			return a, a.workingVerbTick()
		}
		return a, nil

	case sendCompleted:
		// The goroutine is done; no more cancellable work on this context.
		a.inflightCancel = nil
		if m.Err != nil {
			a.optimisticUserPending = false
			// An error means no TurnCompleted will fire (the turn failed or was
			// cancelled), so we must flip busy=false here.  Also reset the
			// activeTurns counter since no matching TurnCompleted is coming.
			a.activeTurns = 0
			a.busy = false
			a.workingVerb = ""
			a.input.SetBusy(false, "")
			if !errors.Is(m.Err, context.Canceled) {
				// Surface the failure so the user has something to react to;
				// silently dropping errors leaves the UI looking dead.
				a.messages = append(a.messages, uiMessage{
					Role:    components.RoleSystem,
					Raw:     "error: " + m.Err.Error(),
					IsError: true,
				})
			}
		}
		// When Err == nil the agent either completed normally (TurnCompleted
		// will handle busy) or returned nil,nil (queued — TurnStarted /
		// TurnCompleted for the actual turn drive the busy state).
		return a, nil

	case tea.KeyPressMsg:
		a.clearSelection()
		return a.handleKey(m)

	case tea.PasteMsg:
		a.clearSelection()
		return a.handlePaste(m)

	case tea.MouseClickMsg:
		if !a.anyOverlayOpen() && m.Button == tea.MouseLeft {
			if idx := a.queuedDraftAtScreen(m.X, m.Y); idx >= 0 {
				a.clearSelection()
				a.editQueuedDraft(idx)
				return a, nil
			}
			// Check for subagent bubble click.
			if id := a.subagentAtScreen(m.X, m.Y); id != "" {
				a.clearSelection()
				a.pushForeground(id)
				return a, nil
			}
			// Check for tool block click (expand/collapse bash output).
			if id := a.toolAtScreen(m.X, m.Y); id != "" {
				a.clearSelection()
				a.expandedTools[id] = !a.expandedTools[id]
				a.invalidateMsgCache()
				return a, nil
			}
			a.handleMouseDown(m.X, m.Y)
		}
		return a, nil

	case tea.MouseMotionMsg:
		if !a.anyOverlayOpen() {
			// Track hover over subagent bubbles.
			prev := a.hoverSubagentID
			a.hoverSubagentID = a.subagentAtScreen(m.X, m.Y)
			if a.hoverSubagentID != prev {
				a.invalidateMsgCache()
			}
			// Skip text selection when hovering a subagent bubble.
			if a.hoverSubagentID == "" {
				a.handleMouseMotion(m.X, m.Y)
			}
		}
		return a, nil

	case tea.MouseReleaseMsg:
		if !a.anyOverlayOpen() {
			cmd := a.handleMouseUp(m.X, m.Y)
			return a, cmd
		}
		return a, nil

	case tea.MouseWheelMsg:
		// Clear selection on scroll.
		a.clearSelection()
		if !a.anyOverlayOpen() {
			prevOffset := a.msgViewport.YOffset()
			a.msgViewport, _ = a.msgViewport.Update(m)
			if a.msgViewport.YOffset() < prevOffset {
				a.userScrolled = true
			} else if a.msgViewport.AtBottom() {
				a.userScrolled = false
			}
		}
		return a, nil

	case busDelivery:
		cmd := a.handleBusEvent(m.Event)
		// Re-issue the listener so the next event is read.
		return a, tea.Batch(cmd, a.listenBus())
	}

	// Forward other messages (e.g. blinking caret) to the textarea so it can
	// animate.  This is the bubbletea standard pattern.
	var cmd tea.Cmd
	a.input.Textarea, cmd = a.input.Textarea.Update(msg)
	return a, cmd
}

func (a *App) workingVerbTick() tea.Cmd {
	return tea.Tick(15*time.Second, func(time.Time) tea.Msg {
		return workingVerbTickMsg{}
	})
}

// handleKey dispatches a key.  When the modal is open, only the modal
// keybinds work; everything else is dropped.
func (a *App) handleKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if top, ok := a.overlays.Top(); ok {
		switch top {
		case overlayPermission:
			return a.handleModalKey(k)
		case overlaySessions:
			return a.handleSessionsModalKey(k)
		case overlayCompactConfirm:
			return a.handleCompactionModalKey(k)
		case overlayHelp:
			if k.String() == "esc" || k.String() == "q" {
				a.closeOverlay(overlayHelp)
			}
			return a, nil
		case overlayModel:
			return a.handleModelModalKey(k)
		case overlayAPIKey:
			return a.handleAPIKeyModalKey(k)
		case overlayTheme:
			return a.handleThemeModalKey(k)
		case overlayQuit:
			switch k.String() {
			case "y", "Y", "ctrl+c":
				return a, tea.Quit
			case "n", "N", "esc":
				a.closeOverlay(overlayQuit)
				return a, nil
			case "left", "right", "tab", "h", "l":
				a.quitSelectedNo = !a.quitSelectedNo
				return a, nil
			case "enter", " ":
				if !a.quitSelectedNo {
					return a, tea.Quit
				}
				a.closeOverlay(overlayQuit)
				return a, nil
			default:
				return a, nil
			}
		}
	}
	if k.Code == tea.KeyEnter && (k.Mod.Contains(tea.ModShift) || k.Mod.Contains(tea.ModAlt)) {
		if a.viewingSubagent() {
			return a, nil
		}
		return a.insertInputNewline()
	}

	switch k.String() {
	case "ctrl+c":
		if a.busy && a.inflightCancel != nil {
			a.inflightCancel()
			return a, nil
		}
		if a.overlays.Has(overlayQuit) {
			return a, tea.Quit
		}
		a.quitSelectedNo = true // default to "No" (safe choice)
		a.openOverlay(overlayQuit)
		return a, nil
	case "ctrl+l":
		a.input.Reset()
		a.pastedInputBlocks = nil
		a.slashPaletteDismissed = false
		a.mentionDismissed = false
		a.cancelQueuedDraftEdit()
		return a, nil
	case "ctrl+x":
		// Dismiss the compaction threshold-suggestion banner for this crossing.
		if a.bannerVisible && !a.bannerDismissed {
			a.bannerDismissed = true
			return a, nil
		}
	case "ctrl+t":
		// Cycle reasoning level: off → low → medium → high → off.
		levels := []string{"", "low", "medium", "high"}
		cur := a.opts.Reasoning.Effort
		idx := 0
		for i, l := range levels {
			if l == cur {
				idx = i
				break
			}
		}
		next := levels[(idx+1)%len(levels)]
		a.opts.Reasoning.Effort = next
		label := next
		if label == "" {
			label = "off"
		}
		a.invalidateMsgCache()
		return a, a.showToast("Reasoning", label)
	case "ctrl+g":
		return a, a.followIntoLatestSubagent()
	case "ctrl+e":
		if a.viewingSubagent() {
			return a, nil
		}
		return a, a.openPromptEditorCmd()
	case "enter":
		if a.viewingSubagent() {
			return a, nil
		}
		if a.acceptMentionCompletion() {
			return a, nil
		}
		// Alt+Enter inserts a newline; route by key code/modifier so
		// terminal-specific string rendering cannot accidentally submit it.
		if k.Mod.Contains(tea.ModAlt) {
			return a.insertInputNewline()
		}
		displayText := strings.TrimSpace(a.input.Value())
		if displayText == "" {
			return a, nil
		}
		// Slash commands cannot be queued — block them while busy.
		if a.busy && strings.HasPrefix(displayText, "/") {
			return a, nil
		}
		if strings.HasPrefix(displayText, "/") {
			if slashPrefixOnly(displayText) {
				name, _ := splitSlash(displayText)
				exact := false
				if a.opts.Commands != nil {
					_, exact = a.opts.Commands.Get(name)
				}
				if !exact && a.acceptPaletteCompletion() {
					return a, nil
				}
			}
			a.input.Reset()
			a.pastedInputBlocks = nil
			a.slashPaletteDismissed = false
			return a, a.runSlashCommand(displayText)
		}
		mentionAttachments, err := a.promptAttachmentsForMentions(displayText)
		if err != nil {
			return a, a.setNotice("mention: " + err.Error())
		}
		text := strings.TrimSpace(a.expandPastedInputText(displayText))
		a.history.Add(text)
		attachments := a.drainPromptAttachments(mentionAttachments)
		a.input.Reset()
		a.pastedInputBlocks = nil
		a.slashPaletteDismissed = false
		a.mentionDismissed = false
		// Resume auto-scroll when the user sends a message.
		a.userScrolled = false
		if a.busy {
			a.enqueuePromptDraft(queuedPromptDraft{Text: text, Attachments: attachments})
			return a, nil
		}
		return a, a.startSendWithAttachments(text, attachments)
	case "pgup":
		// Scroll message list up one page; pause auto-scroll.
		if !a.msgViewport.AtTop() {
			a.msgViewport.PageUp()
			a.userScrolled = true
		}
		return a, nil
	case "pgdown":
		// Scroll message list down one page.
		a.msgViewport.PageDown()
		if a.msgViewport.AtBottom() {
			a.userScrolled = false
		}
		return a, nil
	case "tab":
		// Tab completes active palettes before falling back to mode cycling.
		if a.acceptMentionCompletion() {
			return a, nil
		}
		if a.acceptPaletteCompletion() {
			return a, nil
		}
		if cmd := a.cycleMode(); cmd != nil {
			return a, cmd
		}
	case "esc":
		// Subagent view: Esc pops the foreground stack.
		if len(a.foregroundStack) > 1 {
			a.popForeground()
			return a, nil
		}
		// Dismiss command palette first.
		if a.opts.Commands != nil && strings.HasPrefix(a.input.Value(), "/") && !a.slashPaletteDismissed {
			a.paletteHighlight = -1
			a.slashPaletteDismissed = true
			return a, nil
		}
		if _, _, ok := a.activeMentionQuery(); ok && !a.mentionDismissed {
			a.mentionHighlight = -1
			a.mentionDismissed = true
			return a, nil
		}
		// Double-Esc within 500ms: interrupt everything.
		now := a.opts.Now()
		if a.busy && now.Sub(a.lastEscAt) < 500*time.Millisecond {
			// Cancel the active run.
			if a.inflightCancel != nil {
				a.inflightCancel()
			}
			// Clear the queue.
			a.queuedDrafts = nil
			a.cancelQueuedDraftEdit()
			a.syncQueuedDraftDisplay()
			rootID := a.rootSessionID()
			if a.testAgentClearQueueFn != nil {
				a.testAgentClearQueueFn(rootID)
			} else if a.opts.Agent != nil {
				a.opts.Agent.ClearQueue(rootID)
			}
			a.lastEscAt = time.Time{}
			return a, a.showToast("Interrupted", "Stopped current turn")
		}
		a.lastEscAt = now
		if !a.busy {
			return a, nil
		}
		// First Esc while busy: clear the queue if any.
		if len(a.queuedDrafts) > 0 {
			dropped := len(a.queuedDrafts)
			a.queuedDrafts = nil
			a.cancelQueuedDraftEdit()
			a.syncQueuedDraftDisplay()
			return a, a.setNotice(fmt.Sprintf("cleared %d queued message(s) — press Esc again to interrupt", dropped))
		}
		if a.queueCount > 0 {
			rootID := a.rootSessionID()
			var dropped int
			if a.testAgentClearQueueFn != nil {
				dropped = a.testAgentClearQueueFn(rootID)
			} else if a.opts.Agent != nil {
				dropped = a.opts.Agent.ClearQueue(rootID)
			}
			if dropped > 0 {
				return a, a.setNotice(fmt.Sprintf("cleared %d queued message(s) — press Esc again to interrupt", dropped))
			}
		}
		return a, a.setNotice("press Esc again to interrupt")
	case "up":
		if a.viewingSubagent() {
			a.navigateSubagent(-1) // older subagent
			return a, nil
		}
		if a.opts.Commands != nil && strings.HasPrefix(a.input.Value(), "/") {
			a.movePaletteHighlight(-1)
			return a, nil
		}
		if a.moveMentionHighlight(-1) {
			return a, nil
		}
		if text, ok := a.history.Up(a.input.Value()); ok {
			a.input.Textarea.SetValue(text)
			a.input.Textarea.CursorEnd()
			return a, nil
		}
		return a, nil
	case "ctrl+p":
		if a.opts.Commands != nil && strings.HasPrefix(a.input.Value(), "/") {
			a.movePaletteHighlight(-1)
			return a, nil
		}
		if a.moveMentionHighlight(-1) {
			return a, nil
		}
	case "down":
		if a.viewingSubagent() {
			a.navigateSubagent(+1) // newer subagent
			return a, nil
		}
		if a.opts.Commands != nil && strings.HasPrefix(a.input.Value(), "/") {
			a.movePaletteHighlight(+1)
			return a, nil
		}
		if a.moveMentionHighlight(+1) {
			return a, nil
		}
		if a.history.Browsing() {
			if text, ok := a.history.Down(); ok {
				a.input.Textarea.SetValue(text)
				a.input.Textarea.CursorEnd()
				return a, nil
			}
			return a, nil
		}
	case "ctrl+n":
		if a.opts.Commands != nil && strings.HasPrefix(a.input.Value(), "/") {
			a.movePaletteHighlight(+1)
			return a, nil
		}
		if a.moveMentionHighlight(+1) {
			return a, nil
		}
	}

	return a.updateInputKey(k)
}

func (a *App) updateInputKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if a.handleAtomicPasteEdit(k) {
		return a, nil
	}
	var cmd tea.Cmd
	before := a.input.Value()
	a.input.Textarea, cmd = a.input.Textarea.Update(k)
	if a.input.Value() != before {
		a.history.Reset()
		a.paletteHighlight = -1
		a.slashPaletteDismissed = false
		a.mentionHighlight = -1
		a.mentionDismissed = false
	}
	a.keepCursorOutsidePastedMarkers(k)
	return a, cmd
}

func (a *App) insertInputNewline() (tea.Model, tea.Cmd) {
	before := a.input.Value()
	a.input.Textarea.InsertString("\n")
	if a.input.Value() != before {
		a.history.Reset()
		a.paletteHighlight = -1
		a.slashPaletteDismissed = false
		a.mentionHighlight = -1
		a.mentionDismissed = false
	}
	return a, nil
}

// handleModalKey routes keys to the permission modal.
func (a *App) handleModalKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if len(a.pendingPerms) == 0 {
		return a, nil
	}
	current := a.pendingPerms[0]
	reply := func(decision, scope string) tea.Cmd {
		return func() tea.Msg {
			bus.Publish(a.opts.Bus, bus.PermissionReplied{
				RequestID: current.RequestID,
				Decision:  decision,
				Scope:     scope,
				At:        a.opts.Now(),
			})
			return nil
		}
	}

	switch k.String() {
	case "esc":
		a.pendingPerms = a.pendingPerms[1:]
		a.modalToast = ""
		a.syncPermissionOverlay()
		a.updateInputFocus()
		return a, reply("deny", "once")
	default:
		if len(k.Text) != 1 {
			return a, nil
		}
		switch rune(k.Text[0]) {
		case 'y':
			a.pendingPerms = a.pendingPerms[1:]
			a.modalToast = ""
			a.syncPermissionOverlay()
			a.updateInputFocus()
			return a, reply("allow", "once")
		case 'Y':
			a.pendingPerms = a.pendingPerms[1:]
			a.modalToast = ""
			a.syncPermissionOverlay()
			a.updateInputFocus()
			return a, reply("allow", "session")
		case 'A':
			a.pendingPerms = a.pendingPerms[1:]
			a.modalToast = ""
			a.syncPermissionOverlay()
			a.updateInputFocus()
			return a, reply("allow", "always")
		case 'n', 'N':
			a.pendingPerms = a.pendingPerms[1:]
			a.modalToast = ""
			a.syncPermissionOverlay()
			a.updateInputFocus()
			return a, reply("deny", "once")
		case 'e', 'E':
			a.modalToast = "edit not yet implemented (v0.2)"
			return a, tea.Tick(2*time.Second, func(time.Time) tea.Msg { return clearToastMsg{} })
		}
	}
	return a, nil
}

// startSend launches a goroutine that calls Agent.Send and returns a tea.Cmd
// that immediately emits sendStarted.  sendCompleted (or sendFailed via
// sendOutOfBand) arrives later, once the goroutine finishes.
//
// The goroutine is the single concurrency boundary for a user turn: it runs
// ensureSession + Agent.Send outside the bubbletea event loop so the UI
// remains responsive while the agent is working.  sendOutOfBand(sendCompleted)
// re-enters the event loop when the turn finishes.
//
// In tests that do not wire a *tea.Program, sendOutOfBand is a no-op; tests
// drive sendCompleted manually via app.Update(sendCompleted{...}).
func (a *App) startSend(text string, mentionAttachments ...promptAttachment) tea.Cmd {
	return a.startSendWithAttachments(text, a.drainPromptAttachments(mentionAttachments))
}

func (a *App) drainPromptAttachments(mentionAttachments []promptAttachment) []promptAttachment {
	attachments := append([]promptAttachment(nil), a.pendingAttachments...)
	attachments = appendUniquePromptAttachments(attachments, mentionAttachments...)
	a.pendingAttachments = nil
	return attachments
}

func (a *App) startSendWithAttachments(text string, attachments []promptAttachment) tea.Cmd {
	if a.opts.Agent == nil && a.testAgentSendFn == nil {
		// No agent wired up — useful for tests that just want to verify
		// input handling.  Just emit sendStarted so the busy state flips.
		return func() tea.Msg {
			return sendStarted{UserInput: text, StartedAt: a.opts.Now()}
		}
	}
	// Optimistically render only the active turn. When the app is already busy,
	// Agent.Send will enqueue this prompt; queued prompts should remain in the
	// sticky chrome until their persisted MessageAppended event arrives.
	if !a.busy {
		userMsg := uiMessage{
			Role:      components.RoleUser,
			Raw:       text,
			Timestamp: a.opts.Now(),
		}
		if text != "" {
			userMsg.FinalMarkdown = renderMarkdown(a.ensureRenderer(), text)
		}
		a.messages = append(a.messages, userMsg)
		a.optimisticUserPending = true
	}

	ctx, cancel := context.WithCancel(a.ctx)
	a.inflightCancel = cancel

	// Resolve which send function to call: real agent or test stub.
	sendFn := func(ctx context.Context, sid string, parts []session.Part) (*session.Message, error) {
		return a.opts.Agent.Send(ctx, sid, parts)
	}
	if a.testAgentSendFn != nil {
		sendFn = a.testAgentSendFn
	}

	startedAt := a.opts.Now()
	go func() {
		defer cancel()
		sid, err := a.ensureSession(ctx)
		if err != nil {
			a.sendOutOfBand(sendCompleted{Err: err})
			return
		}
		msg, err := sendFn(ctx, sid, attachmentParts(text, attachments))
		a.sendOutOfBand(sendCompleted{Result: msg, Err: err})
	}()

	return func() tea.Msg {
		return sendStarted{UserInput: text, StartedAt: startedAt}
	}
}

func (a *App) enqueuePromptDraft(draft queuedPromptDraft) {
	if a.queuedDraftEditing {
		idx := min(max(a.queuedDraftEditIdx, 0), len(a.queuedDrafts))
		a.queuedDrafts = append(a.queuedDrafts, queuedPromptDraft{})
		copy(a.queuedDrafts[idx+1:], a.queuedDrafts[idx:])
		a.queuedDrafts[idx] = draft
		a.cancelQueuedDraftEdit()
		a.syncQueuedDraftDisplay()
		return
	}
	a.queuedDrafts = append(a.queuedDrafts, draft)
	a.syncQueuedDraftDisplay()
}

func (a *App) cancelQueuedDraftEdit() {
	a.queuedDraftEditing = false
	a.queuedDraftEditIdx = 0
}

func (a *App) syncQueuedDraftDisplay() {
	if len(a.queuedDrafts) == 0 {
		a.queueCount = 0
		a.queuedPrompts = nil
		return
	}
	a.queueCount = len(a.queuedDrafts)
	a.queuedPrompts = make([]string, 0, len(a.queuedDrafts))
	for _, draft := range a.queuedDrafts {
		a.queuedPrompts = append(a.queuedPrompts, draft.Text)
	}
	if a.busy {
		a.input.SetBusy(true, fmt.Sprintf(" (%d queued)", a.queueCount))
	}
}

func (a *App) flushQueuedDraftsCmd() tea.Cmd {
	if len(a.queuedDrafts) == 0 {
		return nil
	}
	draft := a.queuedDrafts[0]
	a.queuedDrafts = a.queuedDrafts[1:]
	a.cancelQueuedDraftEdit()
	a.syncQueuedDraftDisplay()
	if a.opts.Agent == nil && a.testAgentSendFn == nil {
		return nil
	}

	ctx, cancel := context.WithCancel(a.ctx)
	a.inflightCancel = cancel
	sendFn := func(ctx context.Context, sid string, parts []session.Part) (*session.Message, error) {
		return a.opts.Agent.Send(ctx, sid, parts)
	}
	if a.testAgentSendFn != nil {
		sendFn = a.testAgentSendFn
	}
	startedAt := a.opts.Now()
	go func() {
		defer cancel()
		sid, err := a.ensureSession(ctx)
		if err != nil {
			a.sendOutOfBand(sendCompleted{Err: err})
			return
		}
		msg, err := sendFn(ctx, sid, attachmentParts(draft.Text, draft.Attachments))
		if err != nil {
			a.sendOutOfBand(sendCompleted{Err: err})
			return
		}
		a.sendOutOfBand(sendCompleted{Result: msg})
	}()
	return func() tea.Msg {
		return sendStarted{UserInput: draft.Text, StartedAt: startedAt}
	}
}

func (a *App) appendPersistedUserMessage(messageID string) {
	if a.opts.Store == nil || messageID == "" {
		a.optimisticUserPending = false
		return
	}
	msg, err := a.opts.Store.GetMessage(a.ctx, messageID)
	if err != nil {
		slog.Warn("ui: appendPersistedUserMessage: failed to load message", "message_id", messageID, "err", err)
		a.optimisticUserPending = false
		return
	}
	entries := uiEntriesFromStoreMessage(msg, map[string]session.Part{}, map[string]struct{}{})
	if len(entries) == 0 {
		a.optimisticUserPending = false
		return
	}
	entry := entries[0]
	if a.optimisticUserPending && len(a.messages) > 0 && a.messages[len(a.messages)-1].Role == components.RoleUser {
		a.messages[len(a.messages)-1] = entry
	} else {
		a.messages = append(a.messages, entry)
	}
	a.optimisticUserPending = false
	a.invalidateMsgCache()
}

func (a *App) queuedDraftAtScreen(screenX, screenY int) int {
	if len(a.queuedDrafts) == 0 || screenX < 0 || screenX >= a.layout.leftW {
		return -1
	}
	chromeY := headerHeight + a.layout.chat.Dy() + chatBottomPadding
	firstPromptY := chromeY + 1 // row 0 is the queue/status pill itself.
	idx := screenY - firstPromptY
	if idx < 0 || idx >= len(a.queuedDrafts) || idx >= queuedDraftHitRows {
		return -1
	}
	return idx
}

func (a *App) editQueuedDraft(index int) {
	if index < 0 || index >= len(a.queuedDrafts) {
		return
	}
	draft := a.queuedDrafts[index]
	a.queuedDrafts = append(a.queuedDrafts[:index], a.queuedDrafts[index+1:]...)
	a.queuedDraftEditing = true
	a.queuedDraftEditIdx = index
	a.syncQueuedDraftDisplay()
	a.pendingAttachments = append([]promptAttachment(nil), draft.Attachments...)
	a.setInputValueAndCursor(draft.Text, len([]rune(draft.Text)))
	a.history.Reset()
	a.paletteHighlight = -1
	a.slashPaletteDismissed = false
	a.mentionHighlight = -1
	a.mentionDismissed = false
}

func (a *App) renderAttachmentChips(width int) string {
	if len(a.pendingAttachments) == 0 {
		return ""
	}
	style := lipgloss.NewStyle().Padding(0, 1)
	if a.opts.Theme != nil {
		style = a.opts.Theme.Style(theme.AtomMuted).Padding(0, 1)
	}
	chips := make([]string, 0, len(a.pendingAttachments)+1)
	for _, att := range a.pendingAttachments {
		label := fmt.Sprintf("📎 %s %s", att.Name, formatBytes(att.Size))
		chips = append(chips, style.Render(label))
	}
	chips = append(chips, style.Render("/attachments clear to remove"))
	out := lipgloss.JoinHorizontal(lipgloss.Left, chips...)
	if width > 0 && lipgloss.Width(out) > width {
		return lipgloss.NewStyle().MaxWidth(width).Render(out)
	}
	return out
}

// ensureSession returns a usable session id.  If opts.SessionID is empty,
// a fresh session is created via opts.Store, the id is stored back into
// opts.SessionID, bus.SessionStart is published, and any
// OnSessionCreated callback is invoked.  Subsequent calls return the
// stored id without touching the store.
//
// Concurrency: callers are the per-Send goroutine launched from startSend.
// At most one Send is in flight per App (the inflight cancel field
// enforces single-shot behaviour at the input layer), so we do not lock
// here.
func (a *App) ensureSession(ctx context.Context) (string, error) {
	if a.opts.SessionID != "" {
		return a.opts.SessionID, nil
	}
	if a.opts.Store == nil {
		return "", errors.New("ui: ensureSession: Store is required to lazily create sessions")
	}
	sess, err := a.opts.Store.CreateSession(ctx, session.NewSession{
		ProjectDir: a.opts.ProjectDir,
		Model: session.ModelRef{
			Provider: a.opts.ModelProvider,
			Name:     a.opts.ModelName,
		},
	})
	if err != nil {
		return "", fmt.Errorf("ui: ensureSession: create: %w", err)
	}
	a.opts.SessionID = sess.ID
	// Seed the title cache for new sessions.  At creation time
	// FirstMessagePreview and Slug are both empty, so this resolves to
	// the first-12-chars fallback — but it is populated synchronously
	// here (on the Cmd goroutine, NOT the render goroutine) so View()
	// never needs to call Store.GetSession.
	a.sessionTitle = a.loadSessionTitle(sess.ID)
	bus.Publish(a.opts.Bus, bus.SessionStart{
		SessionID: sess.ID,
		Resumed:   false,
		At:        a.opts.Now(),
	})
	if a.opts.OnSessionCreated != nil {
		a.opts.OnSessionCreated(sess.ID)
	}
	return sess.ID, nil
}

// handleBusEvent applies one event to the App state.
//
// Stage C routing: events tagged with a known descendant sub-session id
// flow into the matching SubagentState; events tagged with the
// foreground session id flow into the primary path; everything else
// is dropped.  Events with no SessionID (e.g. IterationLimitReached
// when the limit was hit by a sub-agent) are always routed to the
// primary path on the assumption they describe the active focus.
func (a *App) handleBusEvent(ev any) tea.Cmd {
	// Invalidate the message render cache — any bus event may mutate messages.
	a.invalidateMsgCache()

	switch e := ev.(type) {

	case bus.SubagentStarted:
		return a.onSubagentStarted(e)

	case bus.SubagentCompleted:
		return a.onSubagentCompleted(e)

	case bus.AssistantTextDelta:
		if a.routeToSubagent(e.SessionID) {
			a.appendSubagentDelta(e.SessionID, e.Text)
			return nil
		}
		if !a.isForeground(e.SessionID) {
			return nil
		}
		// Finalize any trailing streaming thinking block before appending text.
		a.finalizeTrailingThinking()
		a.appendAssistantDelta(e.Text)

	case bus.AssistantThinkingDelta:
		if a.routeToSubagent(e.SessionID) {
			// Subagent thinking is not surfaced in the nested block view.
			return nil
		}
		if !a.isForeground(e.SessionID) {
			return nil
		}
		a.appendThinkingDelta(e.Text)

	case bus.MessageAppended:
		if a.routeToSubagent(e.SessionID) {
			a.flushSubagentStream(e.SessionID, e.Role)
			return nil
		}
		if !a.isForeground(e.SessionID) {
			return nil
		}
		// Refresh the sidebar title cache: the first user message sets
		// FirstMessagePreview in the store.  We call loadSessionTitle here
		// (on the Update goroutine, not the render goroutine) so
		// sidebarSessionTitle() stays cheap.
		if e.SessionID == a.rootSessionID() {
			a.sessionTitle = a.loadSessionTitle(e.SessionID)
		}
		// Finalize any trailing thinking block when the message is committed.
		a.finalizeTrailingThinking()
		if e.Role == string(session.RoleUser) {
			a.appendPersistedUserMessage(e.MessageID)
			return nil
		}
		a.flushAssistantStream(e.Role, e.MessageID)

	case bus.ToolCallRequested:
		if a.routeToSubagent(e.SessionID) {
			a.appendSubagentTool(e.SessionID, e.ToolName, extractTarget(e.Args))
			return nil
		}
		if !a.isForeground(e.SessionID) {
			return nil
		}
		// Finalize any trailing thinking block before a tool call.
		a.finalizeTrailingThinking()
		target := extractTarget(e.Args)
		// Track files that write/edit tools are about to modify.  We record the
		// path at request time (not completion) so the list updates as soon as
		// the tool is dispatched, giving the sidebar something to show even
		// while a long-running write is in progress.
		if e.ToolName == "write" || e.ToolName == "edit" {
			if p := extractPathFromArgs(e.Args); p != "" {
				a.touched.Add(p, a.opts.ProjectDir)
			}
		}
		a.messages = append(a.messages, uiMessage{
			Role:        components.RoleTool,
			ToolName:    e.ToolName,
			ToolUseID:   e.ToolUseID,
			Target:      target,
			ToolArgs:    e.Args,
			Raw:         "(running…)",
			IsStreaming: true,
			Status:      components.ToolStatusPending,
		})

	case bus.ToolCallCompleted:
		if a.routeToSubagent(e.SessionID) {
			a.finishSubagentTool(e.SessionID, e)
			return nil
		}
		if !a.isForeground(e.SessionID) {
			return nil
		}
		a.updateLastTool(e)

	case bus.CostUpdated:
		// T2.1 cost roll-up: always update subagent cost tracking so the
		// nested block header stays current for any sub-session event.
		if a.routeToSubagent(e.SessionID) {
			a.updateSubagentCost(e.SessionID, e)
			// Also fall through to check if this is the root, to keep
			// costDollars correct.
		}
		// The footer always shows the ROOT session's rolled-up total.
		// rootSessionID() returns opts.SessionID when the stack is empty
		// (pre-T2.2 path), preserving existing behaviour.
		rootID := a.rootSessionID()
		if rootID == "" || e.SessionID == rootID {
			a.costDollars = e.DollarsTotal
		}

	case bus.ContextUsageUpdated:
		// Context usage is a parent-level concern.  Sub-agents have
		// their own context windows that are not surfaced in the
		// primary footer.
		if !a.isForeground(e.SessionID) {
			return nil
		}
		a.usedTok = e.UsedTokens
		a.maxTok = e.MaxTokens
		a.pctUsed = e.PctUsed

	case bus.PermissionAsked:
		// Permission asks always pop the modal regardless of which
		// session originated them -- they block tool execution and
		// the user needs to decide either way.  The modal does not
		// (yet) badge which session asked; that's a v0.3 polish.
		a.pendingPerms = append(a.pendingPerms, components.PermissionRequest{
			RequestID: e.RequestID,
			ToolName:  e.ToolName,
			Category:  e.Category,
			Target:    e.Target,
		})
		a.syncPermissionOverlay()
		a.updateInputFocus()
		// Stamp the matching tool row as awaiting permission.  We correlate
		// by ToolName (most recent streaming row with that name) because
		// PermissionAsked does not carry a ToolUseID.
		a.setToolStatus(e.ToolName, components.ToolStatusAwaitingPermission)
		// Send a notification when the terminal is unfocused so the user
		// knows action is needed even when they've switched away.
		a.maybeNotify(notify.Notification{
			Title:   "Hygge is waiting…",
			Message: fmt.Sprintf("Permission required to execute %q", e.ToolName),
		}, "permission_ask")

	case bus.PermissionReplied:
		// Transition the awaiting-permission tool row based on the decision.
		// We find the most-recently-created row that is in AwaitingPermission
		// state — one reply resolves one request, so processing in FIFO order
		// is correct for the common single-permission case.
		if e.Decision == "allow" {
			a.setToolStatusByCurrentStatus(
				components.ToolStatusAwaitingPermission,
				components.ToolStatusRunning,
			)
		} else {
			a.setToolStatusByCurrentStatus(
				components.ToolStatusAwaitingPermission,
				components.ToolStatusCancelled,
			)
		}

	case bus.MCPStatusUpdated:
		a.upsertMCPStatus(components.SidebarMCPStatus{
			Name:      e.Name,
			Ready:     e.Ready,
			Error:     e.Error,
			ToolCount: e.ToolCount,
		})

	case bus.IterationLimitReached:
		// Route iter-limit notices to a sub-agent when the session
		// matches; otherwise it's a parent-loop event.  The matching
		// SubagentCompleted will arrive right after with
		// HitIterLimit=true, so this is mainly a UX nicety in case
		// the order ever inverts.
		if a.routeToSubagent(e.SessionID) {
			if st := a.subagents[e.SessionID]; st != nil {
				st.HitIterLimit = true
			}
			return nil
		}
		if !a.isForeground(e.SessionID) {
			return nil
		}
		a.messages = append(a.messages, uiMessage{
			Role: components.RoleSystem,
			Raw:  fmt.Sprintf("iteration limit reached (%d)", e.Limit),
		})

	// --- Compaction events (T2.3) ---

	case bus.CompactionRequested:
		if !a.isForeground(e.SessionID) {
			return nil
		}
		if e.Source == "threshold" {
			// Advisory suggestion: show the banner (or reset dismiss for a new
			// crossing — the agent only fires this once per crossing, so
			// receiving it again means usage dropped and came back).
			a.bannerVisible = true
			a.bannerPct = e.UsagePct
			a.bannerDismissed = false
		}
		// Source "user" is handled by applyOutcome via the modal outcome path;
		// the bus event is not used to open the modal (that's the slash command's
		// job).

	case bus.CompactionStarted:
		if !a.isForeground(e.SessionID) {
			return nil
		}
		a.compactionInFlight = true
		a.compactionInFlightCount = e.MessagesToCompact

	case bus.CompactionCompleted:
		if !a.isForeground(e.SessionID) {
			return nil
		}
		// The compactionCompleteMsg path handles toast rendering.
		// Here we also clear the banner since compaction has finished,
		// and append a persistent marker row to the message list.
		a.bannerVisible = false
		a.bannerDismissed = false
		// Fetch the marker summary from store so the banner row carries
		// the full text.  Best-effort: if the store is unavailable or the
		// fetch fails we skip the marker row (the toast still fires).
		if a.opts.Store != nil && e.MarkerID != "" {
			marker, err := a.opts.Store.LatestMarker(a.ctx, e.SessionID)
			if err == nil && marker != nil {
				a.messages = append(a.messages, uiMessage{
					Role:              components.RoleMarker,
					MarkerSummary:     marker.Summary,
					MarkerTokensSaved: marker.InputTokensSaved,
				})
			}
		}

	case bus.CompactionFailed:
		if !a.isForeground(e.SessionID) {
			return nil
		}
		// compactionCompleteMsg will carry the error for toast display.
		// Nothing extra to do here — the in-flight notice is cleared by
		// compactionCompleteMsg handling.

	case bus.QueueChanged:
		if len(a.queuedDrafts) > 0 {
			return nil
		}
		// Only update the queue state for the root (active send) session.
		rootID := a.rootSessionID()
		if rootID != "" && e.SessionID != rootID {
			return nil
		}
		a.queueCount = e.Count
		a.queuedPrompts = e.Prompts
		// Update the busy placeholder to reflect the queued count.
		if a.busy {
			suffix := ""
			if a.queueCount > 0 {
				suffix = fmt.Sprintf(" (%d queued)", a.queueCount)
			}
			a.input.SetBusy(true, suffix)
		}

	case bus.TodoChanged:
		rootID := a.rootSessionID()
		if rootID != "" && e.SessionID != rootID {
			return nil
		}
		a.todoIncomplete = e.Incomplete
		a.todoInProgress = e.InProgress
		a.refreshTodosCache()

	case bus.TurnStarted:
		// Gate on foreground session.  Increment the in-flight turn counter
		// and make sure the UI shows busy.  This fires from the agent's own
		// context goroutine, so it always arrives even when the caller's ctx
		// was cancelled (queue-drain path).
		if !a.isForeground(e.SessionID) {
			return nil
		}
		a.activeTurns++
		wasBusy := a.busy
		a.busy = true
		if a.workingVerb == "" {
			a.workingVerb = components.RandomWorkingVerb()
		}
		suffix := ""
		if a.queueCount > 0 {
			suffix = fmt.Sprintf(" (%d queued)", a.queueCount)
		}
		a.input.SetBusy(true, suffix)
		if !wasBusy {
			return a.workingVerbTick()
		}

	case bus.TurnCompleted:
		// Gate on foreground session and send turn-complete notification.
		if !a.isForeground(e.SessionID) {
			return nil
		}
		a.maybeNotify(notify.Notification{
			Title:   "Hygge is waiting…",
			Message: fmt.Sprintf("Turn completed in %q", a.sessionTitle),
		}, "turn_complete")
		// Decrement the in-flight turn counter.  Flip busy=false only when no
		// more turns are running AND the queue is empty.  If a queued send is
		// about to start, its TurnStarted will arrive shortly and re-set busy;
		// keeping it set here avoids a visible flicker.
		if a.activeTurns > 0 {
			a.activeTurns--
		}
		if a.activeTurns == 0 && len(a.queuedDrafts) > 0 {
			a.busy = false
			a.workingVerb = ""
			a.input.SetBusy(false, "")
			return a.flushQueuedDraftsCmd()
		}
		if a.activeTurns == 0 && a.queueCount == 0 {
			a.busy = false
			a.workingVerb = ""
			a.input.SetBusy(false, "")
		}
	}
	return nil
}

func (a *App) upsertMCPStatus(status components.SidebarMCPStatus) {
	for i := range a.opts.MCPStatuses {
		if a.opts.MCPStatuses[i].Name == status.Name {
			a.opts.MCPStatuses[i] = status
			return
		}
	}
	a.opts.MCPStatuses = append(a.opts.MCPStatuses, status)
}

// foregroundID returns the current foreground session id (top of the
// navigation stack).  Falls back to opts.SessionID when the stack is
// empty so the pre-T2.2 lazy-create path still works.
func (a *App) foregroundID() string {
	if n := len(a.foregroundStack); n > 0 {
		return a.foregroundStack[n-1]
	}
	return a.opts.SessionID
}

// subagentAtScreen returns the subagent ID at the given screen coordinates,
// accounting for the viewport offset, chat region position, and bubble width.
// Returns "" if no subagent bubble is at that position.
func (a *App) subagentAtScreen(screenX, screenY int) string {
	if len(a.subagentHitZones) == 0 {
		return ""
	}

	// Only register clicks within the left column's bubble area.
	bubbleW := int(float64(a.layout.leftW) * 0.80)
	if screenX > bubbleW {
		return ""
	}

	// The left column content flow is:
	//   header (headerHeight=1 row)
	//   [breadcrumb + "\n" if present]  <- outside viewport
	//   viewport content (chatH rows)
	//   ...rest...
	//
	// The viewport content starts at screen row = headerHeight + breadcrumb rows.
	// The breadcrumb is rendered above the viewport View() output in renderChatContent.
	viewportTop := headerHeight
	breadcrumbLines := 0
	if !a.viewingSubagent() {
		if segs := a.breadcrumbSegments(); len(segs) > 0 {
			// Breadcrumb is one rendered line + "\n" separator = 2 screen rows.
			breadcrumbLines = 2
			viewportTop += breadcrumbLines
		}
	}

	chatH := a.layout.chat.Dy()
	viewportBottom := viewportTop + chatH - breadcrumbLines
	if screenY < viewportTop || screenY >= viewportBottom {
		return ""
	}

	// Content line = position within viewport + scroll offset.
	contentLine := (screenY - viewportTop) + a.msgViewport.YOffset()
	if contentLine < 0 {
		return ""
	}

	for _, zone := range a.subagentHitZones {
		if contentLine >= zone.StartLine && contentLine < zone.EndLine {
			return zone.SubSessionID
		}
	}
	return ""
}

// toolAtScreen returns the ToolUseID of a bash tool block at the given screen
// coordinates, or "" if none. Uses the same coordinate translation as subagentAtScreen.
func (a *App) toolAtScreen(screenX, screenY int) string {
	if len(a.toolHitZones) == 0 {
		return ""
	}
	bubbleW := int(float64(a.layout.leftW) * 0.80)
	if screenX > bubbleW {
		return ""
	}
	viewportTop := headerHeight
	chatH := a.layout.chat.Dy()
	viewportBottom := viewportTop + chatH
	if screenY < viewportTop || screenY >= viewportBottom {
		return ""
	}
	contentLine := (screenY - viewportTop) + a.msgViewport.YOffset()
	if contentLine < 0 {
		return ""
	}
	for _, zone := range a.toolHitZones {
		if contentLine >= zone.StartLine && contentLine < zone.EndLine {
			return zone.ToolUseID
		}
	}
	return ""
}

// viewingSubagent reports whether the user is viewing a subagent's
// transcript (foreground stack depth > 1).
func (a *App) viewingSubagent() bool {
	return len(a.foregroundStack) > 1
}

// navigateSubagent switches to the next (+1) or previous (-1) subagent
// in the current session.
func (a *App) navigateSubagent(delta int) {
	ids := a.sortedSubagentIDs()
	if len(ids) < 2 {
		return
	}
	cur := a.foregroundID()
	idx := -1
	for i, id := range ids {
		if id == cur {
			idx = i
			break
		}
	}
	if idx < 0 {
		return
	}
	next := (idx + delta + len(ids)) % len(ids)
	if next == idx {
		return
	}
	// Replace the top of the foreground stack.
	a.foregroundStack[len(a.foregroundStack)-1] = ids[next]
	a.refreshMessagesForForeground()
}

// sortedSubagentIDs returns subagent IDs belonging to the parent session
// of the current foreground, sorted by start time.
func (a *App) sortedSubagentIDs() []string {
	// Find the parent session (one level below top of stack).
	parentID := a.rootSessionID()
	if len(a.foregroundStack) >= 2 {
		parentID = a.foregroundStack[len(a.foregroundStack)-2]
	}

	type entry struct {
		id string
		at time.Time
	}
	var entries []entry
	for id, st := range a.subagents {
		if st != nil && st.ParentSessionID == parentID {
			entries = append(entries, entry{id, st.StartedAt})
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].at.Before(entries[j].at)
	})
	ids := make([]string, len(entries))
	for i, e := range entries {
		ids[i] = e.id
	}
	return ids
}

// foregroundSubagent returns the SubagentState for the currently viewed
// subagent, or nil if viewing the root session.
func (a *App) foregroundSubagent() *components.SubagentState {
	if !a.viewingSubagent() {
		return nil
	}
	return a.subagents[a.foregroundID()]
}

// rootSessionID returns the session id at the bottom of the foreground
// stack — the original primary session.  Used by the TUI footer and the
// cost event handler so the rolled-up total is always visible regardless
// of which level the user is viewing.
//
// Falls back to opts.SessionID when the stack is empty.
func (a *App) rootSessionID() string {
	if len(a.foregroundStack) > 0 {
		return a.foregroundStack[0]
	}
	return a.opts.SessionID
}

// pushForeground appends id to the top of the foreground stack.
// If the stack is currently empty, the current foreground (opts.SessionID)
// is used as the implicit root and pushed first, so the stack always has
// the root at index 0 before the new entry.
// Refreshes the message list from the in-memory subagent buffer (for
// now; a future version may reload from the store for the full history).
func (a *App) pushForeground(id string) {
	// Seed the root entry if the stack hasn't been explicitly initialised.
	if len(a.foregroundStack) == 0 && a.opts.SessionID != "" {
		a.foregroundStack = []string{a.opts.SessionID}
	}
	// Guard: do not double-push the same id.
	if a.foregroundID() == id {
		return
	}
	a.foregroundStack = append(a.foregroundStack, id)
	a.refreshMessagesForForeground()
}

// popForeground removes the top of the foreground stack.  No-op when the
// stack would otherwise lose its root entry (depth == 1).
func (a *App) popForeground() {
	if len(a.foregroundStack) <= 1 {
		return
	}
	a.foregroundStack = a.foregroundStack[:len(a.foregroundStack)-1]
	a.refreshMessagesForForeground()
}

// resetForeground replaces the entire stack with [id].  Used by the
// sessions modal "switch" action: the chosen session becomes the new root
// and the breadcrumb is cleared (stack depth == 1).
func (a *App) resetForeground(id string) {
	a.foregroundStack = []string{id}
	a.refreshMessagesForForeground()
}

// refreshMessagesForForeground updates the messages buffer to show the
// foregrounded session.  If the foregrounded session is a known subagent,
// the subagent's transcript is loaded.  Otherwise the primary message
// list is kept as-is (the in-memory buffer is already the primary view).
//
// NOTE: A future version will reload from the store so previously-stored
// messages are visible when following into a completed subagent.
func (a *App) refreshMessagesForForeground() {
	a.invalidateMsgCache()
	a.userScrolled = false

	id := a.foregroundID()
	if id == a.rootSessionID() {
		return
	}
	st, ok := a.subagents[id]
	if !ok {
		return
	}
	// Replace the primary message buffer with the sub-session's messages
	// so the MessageList renders the sub-session's conversation.
	// On pop we restore the primary buffer — but since we only swap
	// a.messages we need to stash it.  Use the foregrounded session
	// approach: when depth > 1 we source from the subagent state.
	// To keep it simple: messages are NOT swapped here; the View()
	// method checks foregroundStack depth and renders accordingly.
	_ = st // used by View() directly
}

// breadcrumbSegments builds the label slice for the Breadcrumb component.
// When viewing a subagent, shows the subagent type/description and nav hints.
func (a *App) breadcrumbSegments() []string {
	if len(a.foregroundStack) <= 1 {
		return nil
	}
	st := a.foregroundSubagent()
	if st == nil {
		return []string{"subagent", "esc to go back"}
	}

	label := st.Type
	if st.Description != "" {
		label += " — " + st.Description
	}

	ids := a.sortedSubagentIDs()
	nav := "esc to go back"
	if len(ids) > 1 {
		nav = "↑ ↓ navigate · esc to go back"
	}
	return []string{label, nav}
}

// latestSubagentID returns the sub-session id of the most recently started
// sub-agent, or "" when no sub-agents have been tracked.  This is the
// "most recent" heuristic shared with toggleLatestSubagent / Ctrl+T.
func (a *App) latestSubagentID() string {
	var latest *components.SubagentState
	for _, st := range a.subagents {
		if st == nil {
			continue
		}
		if latest == nil || st.StartedAt.After(latest.StartedAt) {
			latest = st
		}
	}
	if latest == nil {
		return ""
	}
	return latest.SubSessionID
}

// followIntoLatestSubagent pushes the most-recently-started sub-agent's
// session onto the foreground stack.  If no sub-agents are tracked, a
// notice is set and the call is a no-op.  Returns the notice tea.Cmd.
func (a *App) followIntoLatestSubagent() tea.Cmd {
	id := a.latestSubagentID()
	if id == "" {
		return a.setNotice("no subagent to follow (Ctrl+G)")
	}
	a.pushForeground(id)
	return nil
}

// isForeground reports whether sessionID is the App's active foreground
// session.  An empty foreground id matches anything: this preserves the
// pre-Stage-C behaviour where the App accepted all events because the
// session was lazily created on first user input.
func (a *App) isForeground(sessionID string) bool {
	fg := a.foregroundID()
	if fg == "" {
		return true
	}
	return sessionID == fg
}

// routeToSubagent reports whether sessionID matches a tracked sub-agent
// state AND the user has NOT followed into that session (i.e. it is not
// the current foreground).  When the user has pressed Ctrl+G to follow
// into a sub-session, that sub-session's events flow into the primary
// message path rather than the nested block path.
func (a *App) routeToSubagent(sessionID string) bool {
	if sessionID == "" {
		return false
	}
	// If this session is the current foreground (the user has followed
	// into it), treat it as the primary path.
	if a.isForeground(sessionID) {
		return false
	}
	_, ok := a.subagents[sessionID]
	return ok
}

// onSubagentStarted reacts to bus.SubagentStarted.  Filtering: only
// track sub-agents whose parent chain roots at the foreground session.
// The state is bound to the task tool message via exact ToolUseID match
// (see attachSubagentToSubagentMessage).
func (a *App) onSubagentStarted(e bus.SubagentStarted) tea.Cmd {
	if !a.isInForegroundChain(e.ParentSessionID) {
		return nil
	}
	state := &components.SubagentState{
		SubSessionID:    e.SubSessionID,
		ParentSessionID: e.ParentSessionID,
		ParentMessageID: e.ParentMessageID,
		Type:            e.Type,
		Description:     e.Description,
		Model:           e.Model,
		StartedAt:       e.At,
	}
	a.subagents[e.SubSessionID] = state

	a.attachSubagentToSubagentMessage(state)

	// Create an Anim for the running sub-agent.  Resumed sessions are
	// never live-started (they arrive via hydrateMessagesFromStore with
	// EndedAt already set), so we only create Anims here.
	an := anim.New(anim.Settings{
		Width: 8,
		Theme: a.opts.Theme,
	})
	a.subagentAnims[e.SubSessionID] = an

	// Drive the elapsed-time tick while running.  Coalesces with
	// the spinner Tick that's already in flight; bubbletea handles
	// multiple Tick'ers fine.
	return tea.Batch(a.subagentTick(e.SubSessionID), an.Start())
}

// attachSubagentToSubagentMessage walks the message buffer for the
// matching `task` tool message and stamps SubagentID on it.
//
// Primary path: exact ToolUseID match — the task tool UIMessage whose
// ToolUseID equals SubagentStarted.ParentMessageID is the canonical
// anchor and is always unambiguous when ToolUseID is populated.
//
// Defensive fallback: when no exact match is found (e.g. the event
// predates the ToolUseID field being populated), the most recent
// unclaimed streaming task message is used.  An slog.Warn is emitted
// so the condition is observable in logs.
func (a *App) attachSubagentToSubagentMessage(state *components.SubagentState) {
	if state.ParentMessageID != "" {
		// Primary path: exact ToolUseID match.
		for i := len(a.messages) - 1; i >= 0; i-- {
			msg := &a.messages[i]
			if msg.Role != components.RoleTool || msg.ToolName != "subagent" {
				continue
			}
			if msg.ToolUseID != state.ParentMessageID {
				continue
			}
			if msg.SubagentID != "" && msg.SubagentID != state.SubSessionID {
				continue
			}
			msg.SubagentID = state.SubSessionID
			return
		}
	}

	// Defensive fallback: most recent unclaimed streaming task message.
	for i := len(a.messages) - 1; i >= 0; i-- {
		msg := &a.messages[i]
		if msg.Role != components.RoleTool || msg.ToolName != "subagent" {
			continue
		}
		if msg.SubagentID != "" && msg.SubagentID != state.SubSessionID {
			continue
		}
		slog.Warn("ui: subagent anchor fell back to recency heuristic; ToolUseID missing on subagent message",
			"sub_session_id", state.SubSessionID,
			"parent_message_id", state.ParentMessageID,
		)
		msg.SubagentID = state.SubSessionID
		return
	}
}

// onSubagentCompleted reacts to bus.SubagentCompleted.  Marks EndedAt,
// freezes the running cost/usage with the event's authoritative
// totals, and surfaces HitIterLimit on the state so the header
// switches to the failed style.
func (a *App) onSubagentCompleted(e bus.SubagentCompleted) tea.Cmd {
	state, ok := a.subagents[e.SubSessionID]
	if !ok {
		return nil
	}
	end := e.At
	if end.IsZero() {
		end = a.opts.Now()
	}
	state.EndedAt = end
	state.HitIterLimit = e.HitIterLimit
	// CostUSD is the final authoritative cost.  Override the
	// running counter even if it drifted (the design doc calls
	// this out explicitly).
	state.Cost = e.CostUSD
	// Stop the anim ticking for this sub-agent: delete from the map so
	// future anim.StepMsg arrivals are silently dropped.
	delete(a.subagentAnims, e.SubSessionID)
	return nil
}

// isInForegroundChain reports whether parentSessionID is the foreground
// session or any descendant of it.  Used to filter incoming
// SubagentStarted events so a sub-agent dispatched by a non-foreground
// session does not leak into the current view.
func (a *App) isInForegroundChain(parentSessionID string) bool {
	if parentSessionID == "" {
		return false
	}
	fg := a.foregroundID()
	if fg == "" {
		// No foreground bound yet -- accept the dispatcher's
		// session as the implicit root.  This preserves
		// pre-Stage-C "no filtering" behaviour for the empty-id
		// edge case.
		return true
	}
	if parentSessionID == fg {
		return true
	}
	// Walk known sub-agents.  Bounded by the size of the map; the
	// runtime currently caps recursion at depth 1.
	cur := parentSessionID
	for i := 0; i < len(a.subagents)+1; i++ {
		st, ok := a.subagents[cur]
		if !ok {
			return false
		}
		if st.ParentSessionID == fg {
			return true
		}
		cur = st.ParentSessionID
	}
	return false
}

// subagentTick returns a tea.Cmd that fires a subagentTickMsg one
// second from now if the named sub-agent is still running.  Update
// re-issues the tick on every fire until the sub-agent completes.
// The single global spinner Tick already drives spinner frames, but
// the spinner tick is locked to the spinner.Model's own cadence;
// dedicating a sub-agent tick lets the elapsed-time counter update
// independently and stop when the sub-agent finishes.
func (a *App) subagentTick(subSessionID string) tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg {
		return subagentTickMsg{SubSessionID: subSessionID}
	})
}

// appendSubagentDelta streams text into the matching sub-agent's
// transcript.  Mirrors appendAssistantDelta but scoped to a
// SubagentState.
func (a *App) appendSubagentDelta(subSessionID, text string) {
	st, ok := a.subagents[subSessionID]
	if !ok {
		return
	}
	if n := len(st.Messages); n > 0 {
		last := &st.Messages[n-1]
		if last.Role == components.RoleAssistant && last.IsStreaming {
			last.Raw += text
			return
		}
	}
	st.Messages = append(st.Messages, uiMessage{
		Role:          components.RoleAssistant,
		Raw:           text,
		IsStreaming:   true,
		AgentType:     st.Type,
		SubagentColor: components.ColorForSubagentType(st.Type),
	})
}

// flushSubagentStream marks the matching sub-agent's most recent
// assistant message as final.  Mirrors flushAssistantStream; no
// markdown rendering -- the nested view is plain text by design.
func (a *App) flushSubagentStream(subSessionID, role string) {
	if role != "assistant" {
		return
	}
	st, ok := a.subagents[subSessionID]
	if !ok {
		return
	}
	n := len(st.Messages)
	if n == 0 {
		return
	}
	last := &st.Messages[n-1]
	if last.Role != components.RoleAssistant {
		return
	}
	last.IsStreaming = false
}

// appendSubagentTool appends a streaming tool entry to the matching
// sub-agent's transcript.
func (a *App) appendSubagentTool(subSessionID, toolName, target string) {
	st, ok := a.subagents[subSessionID]
	if !ok {
		return
	}
	st.Messages = append(st.Messages, uiMessage{
		Role:        components.RoleTool,
		ToolName:    toolName,
		Target:      target,
		Raw:         "(running…)",
		IsStreaming: true,
	})
}

// finishSubagentTool finalises the most recent streaming tool entry
// for a sub-agent, mirroring updateLastTool but scoped.
func (a *App) finishSubagentTool(subSessionID string, e bus.ToolCallCompleted) {
	st, ok := a.subagents[subSessionID]
	if !ok {
		return
	}
	for i := len(st.Messages) - 1; i >= 0; i-- {
		msg := &st.Messages[i]
		if msg.Role != components.RoleTool || !msg.IsStreaming {
			continue
		}
		if msg.ToolName != e.ToolName {
			continue
		}
		msg.IsStreaming = false
		if e.Err != "" {
			msg.IsError = true
			msg.Raw = e.Err
		} else {
			msg.Raw = string(e.Result)
		}
		return
	}
	out := string(e.Result)
	if e.Err != "" {
		out = e.Err
	}
	st.Messages = append(st.Messages, uiMessage{
		Role:     components.RoleTool,
		ToolName: e.ToolName,
		Raw:      out,
		IsError:  e.Err != "",
	})
}

// updateSubagentCost updates a sub-agent's running cost & token totals
// from a bus.CostUpdated event.
func (a *App) updateSubagentCost(subSessionID string, e bus.CostUpdated) {
	st, ok := a.subagents[subSessionID]
	if !ok {
		return
	}
	st.Cost = e.DollarsTotal
	st.InputTokens = e.InputTokens
	st.OutputTokens = e.OutputTokens
}

// gitBranch returns the current git branch for the project directory.
// Delegates to the state package which caches the result per-session.
func (a *App) gitBranch() string {
	if a.opts.ProjectDir == "" {
		return ""
	}
	return appstate.GitBranch(a.opts.ProjectDir)
}

// refreshTodosCache loads the foreground session's todo list from the store
// into todosCache.  Called when the foreground session changes or when a
// bus.TodoChanged event arrives for the foreground session.  Runs from the
// Update loop in the bubbletea goroutine so no lock is needed on todosCache.
func (a *App) refreshTodosCache() {
	a.todosCache = nil
	if a.opts.Store == nil {
		return
	}
	sid := a.rootSessionID()
	if sid == "" {
		return
	}
	items, _, err := a.opts.Store.GetSessionTodos(a.ctx, sid)
	if err != nil {
		slog.Warn("ui: refreshTodosCache: failed to load todos",
			"session_id", sid, "err", err)
		return
	}
	if len(items) == 0 {
		return
	}
	out := make([]components.SidebarTodo, 0, len(items))
	for _, it := range items {
		out = append(out, components.SidebarTodo{
			Title:  it.Content,
			Status: components.SidebarTodoStatus(it.Status),
		})
	}
	a.todosCache = out
}

// collapsedProjectPath returns opts.ProjectDir with the home directory
// prefix replaced by "~", mirroring the logic from the old HeaderBar.
func (a *App) collapsedProjectPath() string {
	p := a.opts.ProjectDir
	h := a.opts.HomeDir
	if h != "" && strings.HasPrefix(p, h) {
		rest := strings.TrimPrefix(p, h)
		if rest == "" {
			return "~"
		}
		return "~" + rest
	}
	return p
}

// sidebarSessionTitle returns the cached display title for the current root
// session.  The cache is populated synchronously in ensureSession, Init, and
// handleBusEvent (bus.MessageAppended) so this method never calls
// Store.GetSession on the render goroutine.
//
// Preference order: FirstMessagePreview → Slug → first 12 chars of session id.
func (a *App) sidebarSessionTitle() string {
	if a.sessionTitle != "" {
		return a.sessionTitle
	}
	// Fallback for the brief window before the cache is seeded (e.g.
	// immediately after Init before the first render with a known session).
	rootID := a.rootSessionID()
	if rootID == "" {
		return ""
	}
	if len(rootID) > 12 {
		return rootID[:12]
	}
	return rootID
}

// loadSessionTitle reads the session title from the store and returns the
// display string.  Preference order: FirstMessagePreview → Slug → first 12
// chars of session id.  Used to populate a.sessionTitle synchronously on
// the Cmd goroutine so View() never blocks on store I/O.
func (a *App) loadSessionTitle(id string) string {
	if id == "" {
		return ""
	}
	if a.opts.Store != nil {
		sess, err := a.opts.Store.GetSession(a.ctx, id)
		if err == nil && sess != nil {
			if sess.FirstMessagePreview != "" {
				return sess.FirstMessagePreview
			}
			if sess.Slug != "" {
				return sess.Slug
			}
		}
	}
	// Fallback: first 12 chars of the session id.
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// appendThinkingDelta appends thinking text to the trailing streaming
// RoleAssistant message's Thinking field.  If the trailing message is not a
// streaming assistant, a new one is created with Thinking populated and Raw
// empty.  Thinking and text both live on one message; the message finalizes
// when the assistant turn completes.
func (a *App) appendThinkingDelta(text string) {
	if n := len(a.messages); n > 0 {
		last := &a.messages[n-1]
		if last.Role == components.RoleAssistant && last.IsStreaming {
			last.Thinking += text
			return
		}
	}
	a.messages = append(a.messages, uiMessage{
		Role:        components.RoleAssistant,
		Thinking:    text,
		Raw:         "",
		IsStreaming: true,
		AgentType:   a.ActiveModeName(),
		ModelName:   a.opts.ModelName,
		ModeColor:   a.activeModeColor(),
	})
}

// finalizeTrailingThinking is a no-op after Phase 2: thinking and text both
// live on the same RoleAssistant message, so there is nothing to "finalize"
// separately.  The function is preserved as a call-site placeholder so the
// existing handleBusEvent call graph compiles without changes in Phase 3.
func (a *App) finalizeTrailingThinking() {
	// Thinking lives inline on the assistant message — no separate row to
	// finalize.  This function is preserved as a call-site placeholder so the
	// existing handleBusEvent call graph compiles without changes.
}

// appendAssistantDelta appends text to the streaming assistant message, or
// starts a new one if the last message isn't a streaming assistant.
// Reuses the same streaming assistant uiMessage when thinking has already
// accumulated on it (thinking and text share one message in Phase 2).
func (a *App) appendAssistantDelta(text string) {
	if n := len(a.messages); n > 0 {
		last := &a.messages[n-1]
		if last.Role == components.RoleAssistant && last.IsStreaming {
			last.Raw += text
			return
		}
	}
	a.messages = append(a.messages, uiMessage{
		Role:        components.RoleAssistant,
		Raw:         text,
		IsStreaming: true,
		AgentType:   a.ActiveModeName(),
		ModelName:   a.opts.ModelName,
		ModeColor:   a.activeModeColor(),
	})
}

// flushAssistantStream marks the most recent assistant message as final and
// renders it through glamour.  The messageID parameter is used to look up
// token/cost/duration data from the store when available.
func (a *App) flushAssistantStream(role, messageID string) {
	if role != "assistant" {
		return
	}
	n := len(a.messages)
	if n == 0 {
		return
	}
	last := &a.messages[n-1]
	if last.Role != components.RoleAssistant {
		return
	}
	last.IsStreaming = false
	last.Timestamp = a.opts.Now()
	last.FinalMarkdown = renderMarkdown(a.ensureRenderer(), last.Raw)

	// Populate token/cost/duration from store if available.
	if a.opts.Store != nil && messageID != "" {
		if msg, err := a.opts.Store.GetMessage(a.ctx, messageID); err == nil && msg != nil {
			last.OutputTokens = msg.OutputTokens
			last.CostUSD = msg.CostUSD
			last.DurationMs = msg.DurationMs
			if !msg.CreatedAt.IsZero() {
				last.Timestamp = msg.CreatedAt
			}
		}
	}
}

// updateLastTool finds the most recent streaming tool entry with a matching
// name and finalises it with the result/error from the event.
func (a *App) updateLastTool(e bus.ToolCallCompleted) {
	for i := len(a.messages) - 1; i >= 0; i-- {
		msg := &a.messages[i]
		if msg.Role != components.RoleTool || !msg.IsStreaming {
			continue
		}
		if msg.ToolName != e.ToolName {
			continue
		}
		msg.IsStreaming = false
		if e.Err != "" {
			msg.IsError = true
			msg.Raw = e.Err
			msg.Status = components.ToolStatusError
		} else {
			msg.Status = components.ToolStatusCompleted
			msg.Raw = string(e.Result)
		}
		return
	}
	// No matching streaming tool entry — synthesise one.
	out := string(e.Result)
	status := components.ToolStatusCompleted
	if e.Err != "" {
		out = e.Err
		status = components.ToolStatusError
	}
	a.messages = append(a.messages, uiMessage{
		Role:     components.RoleTool,
		ToolName: e.ToolName,
		Raw:      out,
		IsError:  e.Err != "",
		Status:   status,
	})
}

// setToolStatus finds the most recent streaming RoleTool message with the
// given toolName and sets its Status field.  Used by the PermissionAsked
// handler to mark the row as awaiting permission.  No-op when no match
// is found (the tool row may not have arrived on the bus yet — rare race).
func (a *App) setToolStatus(toolName string, status components.ToolStatus) {
	for i := len(a.messages) - 1; i >= 0; i-- {
		msg := &a.messages[i]
		if msg.Role == components.RoleTool && msg.IsStreaming && msg.ToolName == toolName {
			msg.Status = status
			return
		}
	}
}

// setToolStatusByCurrentStatus finds the most recent RoleTool message whose
// Status equals fromStatus and transitions it to toStatus.  Used by the
// PermissionReplied handler to move the first awaiting-permission row to
// Running or Cancelled.  No-op when no match is found.
func (a *App) setToolStatusByCurrentStatus(fromStatus, toStatus components.ToolStatus) {
	for i := len(a.messages) - 1; i >= 0; i-- {
		msg := &a.messages[i]
		if msg.Role == components.RoleTool && msg.Status == fromStatus {
			msg.Status = toStatus
			return
		}
	}
}

// ensureRenderer constructs (or returns the cached) glamour renderer for the
// current bubble content width. msgColW is already the bubble content width
// (80% of the left column minus side bar + padding), so glamour word-wrap
// exactly matches the space available inside the bubble and content never
// overflows.
func (a *App) ensureRenderer() *glamour.TermRenderer {
	if a.renderer != nil && a.rendererW == a.msgColW {
		return a.renderer
	}
	w := a.msgColW
	if w <= 0 {
		w = 80
	}
	r, err := newRenderer(a.opts.Theme, w)
	if err != nil {
		return nil
	}
	a.renderer = r
	a.rendererW = w
	return a.renderer
}

func (a *App) rerenderFinalMarkdownMessages() {
	r := a.ensureRenderer()
	if r == nil {
		return
	}
	for i := range a.messages {
		msg := &a.messages[i]
		if msg.IsStreaming || msg.Raw == "" {
			continue
		}
		switch msg.Role {
		case components.RoleUser, components.RoleAssistant:
			msg.FinalMarkdown = renderMarkdown(r, msg.Raw)
		}
	}
	a.invalidateMsgCache()
}

// extractTarget makes a best-effort attempt to surface a useful target
// string (path, command) from a tool's raw JSON args.  Returns "" when the
// args don't decode or don't contain anything obvious.  We are intentionally
// duck-typed here — internal/ui must not depend on internal/tool schemas.
func extractTarget(args []byte) string {
	s := string(args)
	for _, key := range []string{`"path"`, `"file"`, `"command"`, `"url"`, `"target"`} {
		if _, after, ok := strings.Cut(s, key); ok {
			rest := after
			rest = strings.TrimLeft(rest, " \t:")
			if !strings.HasPrefix(rest, `"`) {
				continue
			}
			rest = rest[1:]
			before, _, ok := strings.Cut(rest, `"`)
			if !ok {
				continue
			}
			return before
		}
	}
	return ""
}

// extractPathFromArgs extracts the "filePath" or "path" field from a tool
// call's raw JSON args.  Used for write/edit tools to track modified files.
// Returns "" when the field is absent or the args cannot be decoded.
func extractPathFromArgs(args []byte) string {
	if len(args) == 0 {
		return ""
	}
	// Try both field names used by write/edit tools: "filePath" first
	// (write tool), then "path" (edit tool and some aliases).
	var fields struct {
		FilePath string `json:"filePath"`
		Path     string `json:"path"`
	}
	// Use the existing duck-typed extractor as a fast path before JSON decode.
	// Both "filePath" and "path" would be found by extractTarget("path"), but
	// we want the more specific JSON decode to avoid false positives from
	// values that contain the literal string "path".
	//
	// Note: encoding/json is not imported here because app.go already has it
	// through session/bus types.  We use a minimal inline parse via
	// extractTarget which is sufficient for this use case.
	if p := extractFieldString(args, "filePath"); p != "" {
		return p
	}
	if p := extractFieldString(args, "path"); p != "" {
		return p
	}
	_ = fields
	return ""
}

// extractFieldString extracts a top-level JSON string field by name from
// raw JSON bytes.  Returns "" when the field is absent or not a string.
// This is a lightweight alternative to a full json.Unmarshal when only one
// field is needed.
func extractFieldString(raw []byte, field string) string {
	key := `"` + field + `"`
	s := string(raw)
	_, after, ok := strings.Cut(s, key)
	if !ok {
		return ""
	}
	rest := after
	rest = strings.TrimLeft(rest, " \t\r\n:")
	if !strings.HasPrefix(rest, `"`) {
		return ""
	}
	rest = rest[1:]
	before, _, ok := strings.Cut(rest, `"`)
	if !ok {
		return ""
	}
	return before
}

// updateInputFocus sets input.Focused based on whether any modal is currently
// visible.  Call this after any change to activeModal or pendingPerms.
//
// Rule: the input border renders in the accent color only when no modal is
// covering the input area.  Any open modal → muted border; all modals
// dismissed → accent border.
func (a *App) updateInputFocus() {
	a.input.Focused = !a.anyOverlayOpen()
}

func (a *App) anyOverlayOpen() bool {
	a.syncPermissionOverlay()
	return a.overlays.Open()
}

func (a *App) openOverlay(kind overlayKind) {
	a.overlays.Push(kind)
	a.syncActiveModal()
}

func (a *App) closeOverlay(kind overlayKind) {
	a.overlays.Remove(kind)
	a.syncActiveModal()
	a.updateInputFocus()
}

func (a *App) syncPermissionOverlay() {
	if len(a.pendingPerms) > 0 {
		a.overlays.Push(overlayPermission)
		return
	}
	a.overlays.Remove(overlayPermission)
}

func (a *App) syncActiveModal() {
	a.activeModal = ""
	for i := len(a.overlays.entries) - 1; i >= 0; i-- {
		switch a.overlays.entries[i] {
		case overlayHelp, overlaySessions, overlayCompactConfirm, overlayModel, overlayAPIKey, overlayTheme:
			a.activeModal = string(a.overlays.entries[i])
			return
		}
	}
}

type apiKeySaveResult struct {
	provider string
	err      error
}

type themeSwitchResult struct {
	name    string
	theme   *theme.Theme
	err     error
	saveErr error
}

func (a *App) themeNames() []string {
	if len(a.opts.ThemeNames) > 0 {
		out := make([]string, len(a.opts.ThemeNames))
		copy(out, a.opts.ThemeNames)
		return out
	}
	return []string{"shell"}
}

func currentThemeName(t *theme.Theme) string {
	if t == nil || t.Name == "" {
		return "shell"
	}
	return t.Name
}

func (a *App) switchThemeCmd(name string) tea.Cmd {
	return func() tea.Msg {
		var th *theme.Theme
		if a.opts.LoadTheme != nil {
			loaded, err := a.opts.LoadTheme(a.ctx, name)
			if err != nil {
				return themeSwitchResult{name: name, err: err}
			}
			th = loaded
		} else if name == currentThemeName(a.opts.Theme) || name == "shell" {
			th = theme.ShellTheme()
		} else {
			return themeSwitchResult{name: name, err: fmt.Errorf("unknown theme %q", name)}
		}
		if a.opts.SaveTheme != nil {
			if err := a.opts.SaveTheme(a.ctx, name); err != nil {
				return themeSwitchResult{name: name, theme: th, saveErr: err}
			}
		}
		return themeSwitchResult{name: name, theme: th}
	}
}

func (a *App) handleThemeModalKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	sk := components.ThemeKey{Name: k.String(), Runes: []rune(k.Text)}
	switch k.String() {
	case "up", "down", "enter", "esc", "ctrl+n", "ctrl+p":
		sk.Name = k.String()
	case "backspace", "delete":
		sk.Name = "backspace"
	default:
		if len(k.Text) == 1 {
			sk.Name = k.Text
		}
	}
	updated, msg := a.themeModal.HandleKey(sk)
	a.themeModal = updated
	switch m := msg.(type) {
	case components.CloseThemeModal:
		a.closeOverlay(overlayTheme)
	case components.SelectThemeAction:
		a.closeOverlay(overlayTheme)
		return a, a.switchThemeCmd(m.Name)
	}
	return a, nil
}

func (a *App) handleAPIKeyModalKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	sk := components.APIKeyKey{Name: k.String(), Runes: []rune(k.Text)}
	if k.String() == "backspace" || k.String() == "delete" {
		sk.Name = "backspace"
	}
	updated, msg := a.apiKeyModal.HandleKey(sk)
	a.apiKeyModal = updated
	switch m := msg.(type) {
	case components.CloseAPIKeyModal:
		a.closeOverlay(overlayAPIKey)
	case components.SaveAPIKeyAction:
		a.closeOverlay(overlayAPIKey)
		return a, a.saveAPIKeyCmd(m.Provider, m.APIKey)
	}
	return a, nil
}

func (a *App) saveAPIKeyCmd(providerName, apiKey string) tea.Cmd {
	return func() tea.Msg {
		if a.opts.SaveAPIKey != nil {
			if err := a.opts.SaveAPIKey(a.ctx, providerName, apiKey); err != nil {
				return apiKeySaveResult{provider: providerName, err: err}
			}
		}
		if a.opts.SwitchModel != nil && providerName == a.opts.ModelProvider && a.opts.ModelName != "" {
			if err := a.opts.SwitchModel(a.ctx, a.opts.ModelProvider, a.opts.ModelName); err != nil {
				return apiKeySaveResult{provider: providerName, err: err}
			}
		}
		return apiKeySaveResult{provider: providerName}
	}
}

func (a *App) catalogModelOptions() []components.ModelOption {
	if a.opts.Catalog == nil || a.opts.Catalog.Source() == nil {
		return nil
	}
	src := a.opts.Catalog.Source()
	providers := src.Providers()
	configured := a.configuredModelProviders()
	out := make([]components.ModelOption, 0)
	for _, providerID := range providers {
		if len(configured) > 0 && !configured[providerID] {
			continue
		}
		for _, entry := range src.Models(providerID) {
			out = append(out, components.ModelOption{Provider: providerID, Entry: entry})
		}
	}
	return out
}

func (a *App) configuredModelProviders() map[string]bool {
	configured := make(map[string]bool)
	if provider := strings.TrimSpace(a.opts.ModelProvider); provider != "" {
		configured[provider] = true
	}
	for _, mode := range a.opts.Modes {
		if provider := strings.TrimSpace(mode.Provider); provider != "" {
			configured[provider] = true
		}
	}
	return configured
}

func (a *App) handleModelModalKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	sk := components.ModelKey{Name: k.String(), Runes: []rune(k.Text)}
	switch k.String() {
	case "up", "down", "enter", "esc", "ctrl+n", "ctrl+p":
		sk.Name = k.String()
	case "backspace", "delete":
		sk.Name = "backspace"
	default:
		if len(k.Text) == 1 {
			sk.Name = k.Text
		}
	}
	updated, msg := a.modelModal.HandleKey(sk)
	a.modelModal = updated
	if msg == nil {
		return a, nil
	}
	switch m := msg.(type) {
	case components.CloseModelModal:
		a.closeOverlay(overlayModel)
		return a, nil
	case components.SelectModelAction:
		a.closeOverlay(overlayModel)
		return a, a.switchModelCmd(m.Provider, m.Model)
	}
	return a, nil
}

func (a *App) renderHelpOverlay(width, height int) string {
	border := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(1, 2)
	if a.opts.Theme != nil {
		bs := a.opts.Theme.Style(theme.AtomModalBorder)
		border = border.BorderForeground(bs.GetForeground())
		modal := a.opts.Theme.Style(theme.AtomModalBg)
		if modal.GetBackground() != nil {
			border = border.Background(modal.GetBackground())
		}
	}
	primary := lipgloss.NewStyle().Bold(true)
	muted := lipgloss.NewStyle()
	if a.opts.Theme != nil {
		primary = a.opts.Theme.Style(theme.AtomPrimary).Bold(true)
		muted = a.opts.Theme.Style(theme.AtomMuted)
	}
	body := primary.Render("Help") + "\n\n" +
		"Type / to open command completions.\n" +
		"Use ↑/↓ to navigate completions and Enter to accept.\n\n" +
		"Common commands:\n" +
		"  /sessions  open the session picker\n" +
		"  /compact   compact recent context\n" +
		"  /clear     clear the visible transcript\n\n" +
		muted.Render("[esc] close   [q] close")
	box := border.Render(body)
	if width <= 0 {
		width = 80
	}
	if height <= 0 {
		height = 24
	}
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box)
}

// --- Compaction modal integration -----------------------------------------

// handleCompactionModalKey routes key presses when the compaction
// confirmation modal is open.
func (a *App) handleCompactionModalKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		a.closeOverlay(overlayCompactConfirm)
		return a, nil
	default:
		if len(k.Text) != 1 {
			return a, nil
		}
		switch rune(k.Text[0]) {
		case 'y', 'Y':
			if a.compactionModal.NothingToCompact() {
				// Disable y — nothing to compact.
				return a, nil
			}
			sid := a.foregroundID()
			a.closeOverlay(overlayCompactConfirm)
			return a, func() tea.Msg { return compactionRunMsg{SessionID: sid} }
		case 'n', 'N':
			a.closeOverlay(overlayCompactConfirm)
			return a, nil
		}
	}
	return a, nil
}

// startCompaction runs Agent.Compact asynchronously and returns a tea.Cmd
// that delivers compactionCompleteMsg when it finishes.
func (a *App) startCompaction(sessionID string) tea.Cmd {
	if a.opts.Agent == nil || sessionID == "" {
		return a.setNotice("compact: no agent or session")
	}
	msgCount := a.compactionModal.MessageCount
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(a.ctx, 120*time.Second)
		defer cancel()
		marker, err := a.opts.Agent.Compact(ctx, sessionID)
		if err != nil {
			if errors.Is(err, agent.ErrNothingToCompact) {
				return compactionCompleteMsg{Err: errors.New("nothing to compact (fewer than 4 messages since last marker)")}
			}
			return compactionCompleteMsg{Err: err}
		}
		return compactionCompleteMsg{
			MarkerID:          marker.ID,
			MessagesCompacted: msgCount,
			SummaryTokens:     marker.InputTokensSaved,
		}
	}
}

// shortID returns a short (12-char) prefix of a ULID for display in toasts.
func shortID(id string) string {
	if len(id) <= 12 {
		return id
	}
	return id[:12] + "…"
}

// maybeNotify sends a desktop notification if:
//   - notifications are enabled in config (Config != nil && Enabled == true),
//   - the terminal is not currently focused,
//   - the notification kind is enabled.
//
// kind must be "permission_ask" or "turn_complete".
// The send is best-effort: errors are logged at debug level only.
func (a *App) maybeNotify(n notify.Notification, kind string) {
	if a.opts.Config == nil {
		return
	}
	cfg := a.opts.Config.Notifications
	if !cfg.Enabled {
		return
	}
	// Only send when the terminal is not in focus.
	if a.focused {
		return
	}
	switch kind {
	case "permission_ask":
		if !cfg.PermissionAsk {
			return
		}
	case "turn_complete":
		if !cfg.TurnComplete {
			return
		}
	default:
		return
	}
	if err := a.notifyBackend.Send(n); err != nil {
		slog.Debug("ui: notification send failed", "kind", kind, "err", err)
	}
}

// --- Sessions modal integration --------------------------------------------

// openSessionsModal loads the current session list from the store and
// transitions activeModal to "sessions".
func (a *App) openSessionsModal() tea.Cmd {
	if a.opts.Store == nil {
		return a.setNotice("sessions: no store configured")
	}
	return func() tea.Msg {
		sessions, err := a.opts.Store.ListSessions(a.ctx, session.ListOpts{
			Limit:          200,
			IncludeDeleted: true, // load all; modal filters client-side
		})
		if err != nil {
			return clearNoticeMsg{notice: "sessions: " + err.Error()}
		}
		return sessionsLoadedMsg{sessions: sessions}
	}
}

// sessionsLoadedMsg carries a freshly-loaded session list into the App.
type sessionsLoadedMsg struct {
	sessions []*session.Session
}

// handleSessionsModalKey routes a key press into the sessions modal.
func (a *App) handleSessionsModalKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	sk := components.SessionsKey{
		Name:  k.String(),
		Runes: []rune(k.Text),
	}
	// Map bubbletea key strings to the strings our modal expects.
	switch k.String() {
	case "up":
		sk.Name = "up"
	case "down":
		sk.Name = "down"
	case "enter":
		sk.Name = "enter"
	case "esc":
		sk.Name = "esc"
	case "tab":
		sk.Name = "tab"
	case "backspace", "delete":
		sk.Name = "backspace"
	default:
		if len(k.Text) == 1 {
			sk.Name = k.Text
		}
	}

	updated, msg := a.sessionsModal.HandleKey(sk)
	a.sessionsModal = updated

	if msg == nil {
		return a, nil
	}

	return a, a.applySessionsModalMsg(msg)
}

// applySessionsModalMsg applies a sessions-modal action message.
func (a *App) applySessionsModalMsg(msg components.SessionsModalMsg) tea.Cmd {
	switch m := msg.(type) {
	case components.CloseSessionsModal:
		a.closeOverlay(overlaySessions)
		// When the picker was opened on start (OpenSessionsModalOnStart) and
		// there is no foreground session bound, the user chose to cancel
		// without picking — exit the App.
		if a.opts.OpenSessionsModalOnStart && a.opts.SessionID == "" {
			return tea.Quit
		}
		return nil

	case components.NewSessionAction:
		// User pressed 'n' in the picker with no sessions.  Close the picker
		// and start fresh (no session id → lazy create on first input).
		a.closeOverlay(overlaySessions)
		// Start the bus bridge now that we have a concrete "start fresh" intent.
		if a.opts.SessionID == "" && a.opts.OpenSessionsModalOnStart {
			a.bridge()
			return tea.Batch(a.listenBus(), a.showToast("New session", "Starting fresh"))
		}
		return a.showToast("New session", "Starting fresh")

	case components.SwitchSessionAction:
		a.closeOverlay(overlaySessions)
		return a.applySwitchSession(m.ID)

	case components.ForkSessionAction:
		a.closeOverlay(overlaySessions)
		return a.applyForkSession(m.ID, m.MessageID)

	case components.RenameSessionAction:
		return a.applyRenameSession(m.ID, m.Slug)

	case components.DeleteSessionAction:
		return a.applyDeleteSession(m.ID)
	}
	return nil
}

// applySwitchSession changes the foreground session and resets the UI state.
// The sessions modal "switch" action (Enter) resets the entire foreground
// stack to [id] via resetForeground so the breadcrumb is cleared.  Use
// Ctrl+G to follow into a sub-session without losing the current root.
func (a *App) applySwitchSession(id string) tea.Cmd {
	// When the picker was opened on start before any session was bound,
	// start the bus bridge now that we have a concrete session to track.
	bridgeNeeded := a.opts.SessionID == "" && a.opts.OpenSessionsModalOnStart && id != ""

	a.opts.SessionID = id
	a.messages = nil
	a.invalidateMsgCache()
	a.subagents = map[string]*components.SubagentState{}
	a.subagentAnims = map[string]*anim.Anim{}
	a.renderer = nil
	a.rendererW = 0
	if id != "" {
		a.resetForeground(id)
		a.hydrateMessagesFromStore(id)
		a.sessionTitle = a.loadSessionTitle(id)
		a.hydrateTodoSummary(id)
	} else {
		a.foregroundStack = nil
		a.sessionTitle = ""
		a.todoIncomplete = 0
		a.todoInProgress = 0
	}

	var cmds []tea.Cmd
	if bridgeNeeded {
		a.bridge()
		cmds = append(cmds, a.listenBus())
	}

	noticeID := id
	if len(id) > 8 {
		noticeID = id[:8]
	}
	if id == "" {
		cmds = append(cmds, a.showToast("Session cleared", "Next input creates a new session"))
	} else {
		cmds = append(cmds, a.showToast("Session switched", "Using "+noticeID))
	}
	return tea.Batch(cmds...)
}

// applyForkSession forks a session.  If messageID is "", it resolves the
// latest user message first.
func (a *App) applyForkSession(fromID, messageID string) tea.Cmd {
	if a.opts.Store == nil {
		return a.setNotice("fork: no store configured")
	}
	return func() tea.Msg {
		ctx := a.ctx
		msgID := messageID
		if msgID == "" || msgID == "latest" {
			var err error
			msgID, err = a.opts.Store.LatestUserMessageID(ctx, fromID)
			if err != nil {
				return sendCompleted{Err: fmt.Errorf("fork: lookup latest message: %w", err)}
			}
			if msgID == "" {
				return clearNoticeMsg{notice: "fork: no user messages in session — nothing to fork at"}
			}
		}

		// Validate that the message belongs to the source session.
		msg, err := a.opts.Store.GetMessage(ctx, msgID)
		if err != nil {
			return clearNoticeMsg{notice: "fork: message not found: " + err.Error()}
		}
		if msg.SessionID != fromID {
			return clearNoticeMsg{notice: "fork: message belongs to a different session"}
		}

		src, err := a.opts.Store.GetSession(ctx, fromID)
		if err != nil {
			return clearNoticeMsg{notice: "fork: source session not found: " + err.Error()}
		}

		forked, err := a.opts.Store.ForkSession(ctx, fromID, msgID, src.Model, "")
		if err != nil {
			return clearNoticeMsg{notice: "fork: " + err.Error()}
		}
		return switchSessionMsg{ID: forked.ID}
	}
}

// applyRenameSession renames a session and refreshes the modal.
func (a *App) applyRenameSession(id, slug string) tea.Cmd {
	if a.opts.Store == nil {
		return a.setNotice("rename: no store configured")
	}
	return func() tea.Msg {
		if err := a.opts.Store.RenameSession(a.ctx, id, slug); err != nil {
			return clearNoticeMsg{notice: "rename: " + err.Error()}
		}
		// Refresh the session list inside the modal.
		sessions, err := a.opts.Store.ListSessions(a.ctx, session.ListOpts{
			Limit: 200, IncludeDeleted: true,
		})
		if err != nil {
			return clearNoticeMsg{notice: "rename ok but list reload failed: " + err.Error()}
		}
		return sessionsLoadedMsg{sessions: sessions}
	}
}

// hydrateMessagesFromStore loads persisted history for sid and replaces
// a.messages.  Loads the full conversation history (all messages, including
// pre-compaction ones) and injects RoleMarker banner entries at the correct
// compaction boundaries.  Also reconstructs subagent state for any `task`
// tool messages that have corresponding child sessions.
//
// Idempotent: replaces the slice on every call; calling it twice for the
// same session id produces the same result.
//
// The caller is responsible for clearing a.messages before calling this
// when switching sessions; this function always writes from an empty base
// (a.messages[:0]) so any prior content is discarded.
func (a *App) hydrateMessagesFromStore(sid string) {
	if a.opts.Store == nil || sid == "" {
		return
	}
	visited := make(map[string]struct{})
	msgs := a.hydrateSessionMessages(sid, visited)
	a.messages = msgs
	a.invalidateMsgCache()
}

func (a *App) hydrateTodoSummary(sid string) {
	a.todoIncomplete = 0
	a.todoInProgress = 0
	a.todosCache = nil
	if a.opts.Store == nil || sid == "" {
		return
	}
	items, summary, err := a.opts.Store.GetSessionTodos(a.ctx, sid)
	if err != nil {
		slog.Warn("ui: hydrateTodoSummary: failed to load todos", "session_id", sid, "err", err)
		return
	}
	a.todoIncomplete = summary.Incomplete
	a.todoInProgress = summary.InProgress
	if len(items) == 0 {
		return
	}
	out := make([]components.SidebarTodo, 0, len(items))
	for _, it := range items {
		out = append(out, components.SidebarTodo{
			Title:  it.Content,
			Status: components.SidebarTodoStatus(it.Status),
		})
	}
	a.todosCache = out
}

// hydrateSessionMessages is the recursive implementation shared by
// hydrateMessagesFromStore (primary session) and subagent reconstruction.
// visited guards against cycles (impossible today but defensive).
// It returns a []uiMessage slice for the session's conversation.
//
// For KindSubagent sessions, only messages directly owned by the session
// are loaded (no fork-chain walking), because subagents start with a
// fresh history independent of their parent.
func (a *App) hydrateSessionMessages(sid string, visited map[string]struct{}) []uiMessage {
	if _, seen := visited[sid]; seen {
		slog.Warn("ui: hydrateSessionMessages: cycle detected, skipping",
			"session_id", sid)
		return nil
	}
	visited[sid] = struct{}{}

	// Look up session kind to decide which message query to use.
	// For subagent sessions: load messages directly (no fork-chain).
	// For primary/fork sessions: load via MessagesForSession (walks fork chain).
	var (
		storeMsgs []*session.Message
		err       error
	)
	if sess, lookupErr := a.opts.Store.GetSession(a.ctx, sid); lookupErr == nil &&
		sess.Kind == session.KindSubagent {
		storeMsgs, err = a.opts.Store.MessagesDirectForSession(a.ctx, sid)
	} else {
		storeMsgs, err = a.opts.Store.MessagesForSession(a.ctx, sid)
	}
	if err != nil {
		slog.Warn("ui: hydrateSessionMessages: failed to load history",
			"session_id", sid, "err", err)
		return nil
	}

	// Load all markers so we can inject them in order.
	var markers []*session.Marker
	if markList, err := a.opts.Store.ListMarkersForSession(a.ctx, sid); err == nil {
		markers = markList
	} else {
		slog.Warn("ui: hydrateSessionMessages: failed to load markers",
			"session_id", sid, "err", err)
	}

	// Build a set of message ids that are marker cut-off points.
	// key: beforeMessageID → marker.  Multiple markers may share no
	// common message ids since each compaction advances the cursor.
	// We walk markers in chronological order (oldest first) and inject
	// each one immediately before the message it cuts off at.
	markerByBefore := make(map[string]*session.Marker, len(markers))
	for _, mk := range markers {
		markerByBefore[mk.BeforeMessageID] = mk
	}

	// Pass 1: build a result index keyed by ToolUseID from all RoleTool rows.
	// This handles the common persistence shape where PartToolUse lives inside
	// an assistant message and PartToolResult lives in a separate tool message.
	toolResults, toolUseIDs := buildToolResultIndex(storeMsgs)

	// Pass 2: build the flat message list, passing the result index so that
	// assistant messages can inline results for their PartToolUse parts.
	var out []uiMessage
	for _, m := range storeMsgs {
		// Inject marker banner before this message if one targets it.
		if mk, ok := markerByBefore[m.ID]; ok {
			out = append(out, uiMessage{
				Role:              components.RoleMarker,
				MarkerSummary:     mk.Summary,
				MarkerTokensSaved: mk.InputTokensSaved,
			})
		}
		entries := uiEntriesFromStoreMessage(m, toolResults, toolUseIDs)
		// Wire AgentType and ModelName on assistant entries so bubbles render correctly.
		// Also glamour-render the body text so hydrated messages look the same as
		// finalized live messages.
		for i := range entries {
			switch entries[i].Role {
			case components.RoleAssistant:
				entries[i].AgentType = a.ActiveModeName()
				entries[i].ModelName = a.opts.ModelName
				entries[i].ModeColor = a.activeModeColor()
				if entries[i].Raw != "" {
					entries[i].FinalMarkdown = renderMarkdown(a.ensureRenderer(), entries[i].Raw)
				}
			case components.RoleUser:
				if entries[i].Raw != "" {
					entries[i].FinalMarkdown = renderMarkdown(a.ensureRenderer(), entries[i].Raw)
				}
			}
		}
		out = append(out, entries...)
	}

	// Handle markers whose BeforeMessageID no longer matches any message
	// (e.g. the message was deleted or the chain was rebased).  Fall back
	// to appending orphaned markers at the end.
	injectedIDs := make(map[string]struct{}, len(storeMsgs))
	for _, m := range storeMsgs {
		injectedIDs[m.ID] = struct{}{}
	}
	for _, mk := range markers {
		if _, found := injectedIDs[mk.BeforeMessageID]; !found {
			out = append(out, uiMessage{
				Role:              components.RoleMarker,
				MarkerSummary:     mk.Summary,
				MarkerTokensSaved: mk.InputTokensSaved,
			})
		}
	}

	// Reconstruct subagent state for `task` tool messages.
	// We list all KindSubagent sessions for this parent once, then match
	// them to task tool UIMessages by ToolUseID (extracted from the slug).
	a.reconstructSubagentState(sid, out, visited)

	return out
}

// reconstructSubagentState finds child subagent sessions for sid and
// populates a.subagents + stamps SubagentID on the parent task UIMessages.
// msgs is the already-built message list for sid (modified in place via
// the a.messages slice reference for the primary session).
func (a *App) reconstructSubagentState(parentSID string, msgs []uiMessage, visited map[string]struct{}) {
	if a.opts.Store == nil {
		return
	}

	// List all KindSubagent sessions for this parent.
	childSessions, err := a.opts.Store.ListSessions(a.ctx, session.ListOpts{
		ParentID: parentSID,
		Kind:     session.KindSubagent,
	})
	if err != nil {
		slog.Warn("ui: reconstructSubagentState: failed to list child sessions",
			"parent_id", parentSID, "err", err)
		return
	}
	if len(childSessions) == 0 {
		return
	}

	// Build a map from ToolUseID → child session by parsing the slug.
	// Slug format: "<type>: <description> [<toolUseID>]"
	// We also keep an ordered slice of children for fallback matching.
	toolUseToChild := make(map[string]*session.Session, len(childSessions))
	for _, cs := range childSessions {
		if toolUseID := extractToolUseIDFromSlug(cs.Slug); toolUseID != "" {
			toolUseToChild[toolUseID] = cs
		}
	}

	// Walk msgs (the already-built flat list for this parent) and match
	// task tool entries to child sessions.  We work on the caller's slice
	// by walking a.messages when parentSID is the primary session, or
	// directly on msgs otherwise.  Since hydrateSessionMessages returns
	// the slice and the caller either assigns it to a.messages or uses it
	// directly, we pass a pointer-slice approach: stamp SubagentID on the
	// returned slice, which the caller then stores.
	//
	// For the primary session path, msgs and a.messages will be the same
	// slice after hydrateMessagesFromStore assigns them — but we're still
	// building msgs here, so we work on msgs.
	matched := make(map[string]bool, len(childSessions))
	for i := range msgs {
		msg := &msgs[i]
		if msg.Role != components.RoleTool || msg.ToolName != "subagent" {
			continue
		}
		cs, ok := toolUseToChild[msg.ToolUseID]
		if !ok || msg.ToolUseID == "" {
			continue
		}
		matched[cs.ID] = true
		a.buildSubagentState(cs, msg, visited)
	}

	// Collect truly unmatched children (those not matched by ToolUseID).
	var unmatchedChildren []*session.Session
	for _, cs := range childSessions {
		if !matched[cs.ID] {
			unmatchedChildren = append(unmatchedChildren, cs)
		}
	}

	// Fallback: match remaining children to unclaimed task messages in
	// order of session creation time.  Walk msgs again for unclaimed task
	// entries.
	childIdx := 0
	for i := range msgs {
		if childIdx >= len(unmatchedChildren) {
			break
		}
		msg := &msgs[i]
		if msg.Role != components.RoleTool || msg.ToolName != "subagent" {
			continue
		}
		if msg.SubagentID != "" {
			continue // already claimed
		}
		cs := unmatchedChildren[childIdx]
		childIdx++
		slog.Warn("ui: reconstructSubagentState: falling back to order-based matching",
			"parent_id", parentSID, "child_id", cs.ID)
		a.buildSubagentState(cs, msg, visited)
	}

	// Any remaining unmatched children have no corresponding task message
	// (e.g. the message was deleted).  Register them without an anchor.
	for ; childIdx < len(unmatchedChildren); childIdx++ {
		cs := unmatchedChildren[childIdx]
		childMsgs := a.hydrateSessionMessages(cs.ID, visited)
		state := a.buildSubagentStateFromSession(cs, childMsgs)
		a.subagents[cs.ID] = state
	}
}

// buildSubagentState hydrates the child session cs, creates a SubagentState,
// registers it in a.subagents, and stamps SubagentID on msg.
func (a *App) buildSubagentState(cs *session.Session, msg *uiMessage, visited map[string]struct{}) {
	childMsgs := a.hydrateSessionMessages(cs.ID, visited)
	state := a.buildSubagentStateFromSession(cs, childMsgs)
	a.subagents[cs.ID] = state
	msg.SubagentID = cs.ID
}

// buildSubagentStateFromSession constructs a SubagentState from a session row
// and a pre-hydrated message list.
func (a *App) buildSubagentStateFromSession(cs *session.Session, msgs []uiMessage) *components.SubagentState {
	endedAt := cs.UpdatedAt
	if endedAt.IsZero() {
		endedAt = cs.CreatedAt
	}
	// Parse type/description from slug: "<type>: <description> [toolID]"
	agentType, description := parseTypeDescFromSlug(cs.Slug)

	// Patch assistant messages to use the subagent's type name and color
	// instead of the parent session's active mode (which the generic
	// hydration path stamps).
	subColor := components.ColorForSubagentType(agentType)
	for i := range msgs {
		if msgs[i].Role == components.RoleAssistant {
			msgs[i].AgentType = agentType
			msgs[i].ModeColor = nil
			msgs[i].SubagentColor = subColor
		}
	}

	state := &components.SubagentState{
		SubSessionID:    cs.ID,
		ParentSessionID: cs.ParentID,
		Type:            agentType,
		Description:     description,
		Model:           cs.Model.Provider + "/" + cs.Model.Name,
		StartedAt:       cs.CreatedAt,
		EndedAt:         endedAt, // completed on resume
		Cost:            cs.Totals.CostUSD,
		InputTokens:     cs.Totals.InputTokens,
		OutputTokens:    cs.Totals.OutputTokens,
		Messages:        msgs,
		Expanded:        false,
	}
	return state
}

// extractToolUseIDFromSlug parses the ToolUseID from a subagent session slug.
// The slug format produced by buildSlug is: "<type>: <description> [<toolUseID>]"
// or "<type> [<toolUseID>]" when no description.  Returns "" if not present.
func extractToolUseIDFromSlug(slug string) string {
	// Find the last "[" ... "]" bracketed segment.
	last := strings.LastIndex(slug, "[")
	if last < 0 {
		return ""
	}
	rest := slug[last+1:]
	before, _, ok := strings.Cut(rest, "]")
	if !ok {
		return ""
	}
	return before
}

// parseTypeDescFromSlug extracts the type and description from a subagent
// session slug.  Format: "<type>: <description> [<toolUseID>]".
// Returns ("", "") when the slug is empty or doesn't match.
func parseTypeDescFromSlug(slug string) (agentType, description string) {
	// Strip trailing " [toolUseID]" if present.
	if last := strings.LastIndex(slug, " ["); last >= 0 {
		slug = slug[:last]
	}
	// Split on ": " to separate type from description.
	parts := strings.SplitN(slug, ": ", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return slug, ""
}

// buildToolResultIndex scans all store messages and returns:
//   - results: PartToolResult parts keyed by ToolUseID (from any RoleTool row).
//   - toolUseIDs: set of ToolIDs that appeared in PartToolUse parts of
//     RoleAssistant messages.
//
// This supports the common persistence shape where PartToolUse lives inside
// an assistant message and PartToolResult lives in a separate tool message.
// If a ToolUseID appears in more than one result (should not happen in
// practice), last-write wins and a warning is logged.
func buildToolResultIndex(msgs []*session.Message) (results map[string]session.Part, toolUseIDs map[string]struct{}) {
	results = make(map[string]session.Part)
	toolUseIDs = make(map[string]struct{})
	for _, m := range msgs {
		if m == nil {
			continue
		}
		for _, p := range m.Parts {
			switch p.Kind {
			case session.PartToolResult:
				if _, exists := results[p.ToolUseID]; exists {
					slog.Warn("ui: buildToolResultIndex: duplicate tool_use_id; last writer wins",
						"tool_use_id", p.ToolUseID)
				}
				results[p.ToolUseID] = p
			case session.PartToolUse:
				if m.Role == session.RoleAssistant {
					toolUseIDs[p.ToolID] = struct{}{}
				}
			}
		}
	}
	return results, toolUseIDs
}

// uiEntriesFromStoreMessage converts a persisted *session.Message into zero
// or more uiMessages for the App's message buffer.  Multiple entries can be
// returned from a single store row when the message has text and tool-use
// parts (entries are emitted in part order within the turn).
//
// toolResults is the cross-message result index built by buildToolResultIndex.
// toolUseIDs is the set of ToolIDs that appeared in PartToolUse parts of
// assistant messages, also from buildToolResultIndex.
// Both must not be nil; pass empty maps for the legacy combined-row path.
//
// Phase 2 change: PartThinking parts are now collapsed into the assistant
// uiMessage's Thinking field instead of being emitted as separate rows.
// An assistant message that has only PartToolUse parts (no text, no
// thinking) emits no uiMessage so the bubble is not rendered empty.
func uiEntriesFromStoreMessage(m *session.Message, toolResults map[string]session.Part, toolUseIDs map[string]struct{}) []uiMessage {
	if m == nil {
		return nil
	}
	switch m.Role {
	case session.RoleUser:
		text := firstTextPart(m.Parts)
		if text == "" {
			return nil
		}
		ts := m.CreatedAt
		return []uiMessage{{Role: components.RoleUser, Raw: text, Timestamp: ts}}

	case session.RoleAssistant:
		// Collect all PartThinking texts (joined with "\n\n").
		var thinkingParts []string
		for _, p := range m.Parts {
			if p.Kind == session.PartThinking && p.Text != "" {
				thinkingParts = append(thinkingParts, p.Text)
			}
		}
		thinking := strings.Join(thinkingParts, "\n\n")

		// Accumulate consecutive PartText parts into a single assistant entry.
		var textBuf strings.Builder
		var toolEntries []uiMessage
		for _, p := range m.Parts {
			switch p.Kind {
			case session.PartThinking:
				// Already handled above; skip here.
			case session.PartText:
				textBuf.WriteString(p.Text)
			case session.PartToolUse:
				// Flush any accumulated text before emitting the tool row so
				// the ordering (text before tool) is preserved.
				// (text is captured in textBuf; tool entries are separate)
				target := extractTarget(p.ToolInput)
				raw := ""
				isError := false
				if res, ok := toolResults[p.ToolID]; ok {
					raw = res.Content
					isError = res.IsError
				}
				// Hydrated tool entries with a result are always completed or errored.
				// Entries without a matching result are orphaned (tool_use with no
				// tool_result — interrupted run); use ToolStatusUnknown so no status
				// text is rendered.  Well-formed sessions should not produce orphans.
				hydratedStatus := components.ToolStatusUnknown
				if _, hasResult := toolResults[p.ToolID]; hasResult {
					if isError {
						hydratedStatus = components.ToolStatusError
					} else {
						hydratedStatus = components.ToolStatusCompleted
					}
				}
				toolEntries = append(toolEntries, uiMessage{
					Role:      components.RoleTool,
					ToolName:  p.ToolName,
					ToolUseID: p.ToolID,
					Target:    target,
					ToolArgs:  p.ToolInput,
					Raw:       raw,
					IsError:   isError,
					Status:    hydratedStatus,
				})
			}
		}
		rawText := textBuf.String()

		// Skip entirely if no text and no thinking (tool-only assistant turn).
		if thinking == "" && rawText == "" {
			return toolEntries
		}

		// Emit one assistant uiMessage with thinking + text, then tool entries.
		assistantMsg := uiMessage{
			Role:         components.RoleAssistant,
			Raw:          rawText,
			Thinking:     thinking,
			Timestamp:    m.CreatedAt,
			OutputTokens: m.OutputTokens,
			CostUSD:      m.CostUSD,
			DurationMs:   m.DurationMs,
		}
		return append([]uiMessage{assistantMsg}, toolEntries...)

	case session.RoleTool:
		// Check whether this row contains any PartToolUse (legacy combined-row
		// shape).  If so, pair each PartToolUse with its inline PartToolResult.
		// If not (the common result-only shape produced by the current
		// persistence model), emit nothing — results were already inlined by
		// the assistant turn handling above.
		hasUse := false
		for _, p := range m.Parts {
			if p.Kind == session.PartToolUse {
				hasUse = true
				break
			}
		}
		if !hasUse {
			// Result-only row: warn on truly orphaned results whose ToolUseID
			// did not appear in any assistant message's PartToolUse part.
			for _, p := range m.Parts {
				if p.Kind == session.PartToolResult {
					if _, found := toolUseIDs[p.ToolUseID]; !found {
						slog.Warn("ui: uiEntriesFromStoreMessage: orphaned tool_result (no matching tool_use in any assistant message)",
							"tool_use_id", p.ToolUseID)
					}
				}
			}
			return nil
		}
		// Legacy combined-row: pair each PartToolUse with its inline result.
		inlineResults := make(map[string]session.Part, len(m.Parts))
		for _, p := range m.Parts {
			if p.Kind == session.PartToolResult {
				inlineResults[p.ToolUseID] = p
			}
		}
		var entries []uiMessage
		for _, p := range m.Parts {
			if p.Kind != session.PartToolUse {
				continue
			}
			target := extractTarget(p.ToolInput)
			raw := ""
			isError := false
			if res, ok := inlineResults[p.ToolID]; ok {
				raw = res.Content
				isError = res.IsError
			}
			// Legacy combined-row: always completed or errored (no in-flight state).
			legacyStatus := components.ToolStatusCompleted
			if isError {
				legacyStatus = components.ToolStatusError
			}
			entries = append(entries, uiMessage{
				Role:      components.RoleTool,
				ToolName:  p.ToolName,
				ToolUseID: p.ToolID,
				Target:    target,
				Raw:       raw,
				IsError:   isError,
				Status:    legacyStatus,
			})
		}
		return entries

	case session.RoleSystem:
		text := firstTextPart(m.Parts)
		if text == "" {
			return nil
		}
		return []uiMessage{{Role: components.RoleSystem, Raw: text}}

	default:
		return nil
	}
}

// uiEntryFromStoreMessage is a compatibility shim retained for test
// coverage of the single-entry path.  It delegates to
// uiEntriesFromStoreMessage with an empty result index (covers the legacy
// combined-row shape where both PartToolUse and PartToolResult appear in
// the same message row) and returns the first entry, or (zero, false) when
// empty.
func uiEntryFromStoreMessage(m *session.Message) (uiMessage, bool) {
	empty := map[string]session.Part{}
	emptyIDs := map[string]struct{}{}
	entries := uiEntriesFromStoreMessage(m, empty, emptyIDs)
	if len(entries) == 0 {
		return uiMessage{}, false
	}
	return entries[0], true
}

// firstTextPart returns the Text of the first PartText part in parts, or "".
func firstTextPart(parts []session.Part) string {
	for _, p := range parts {
		if p.Kind == session.PartText {
			return p.Text
		}
	}
	return ""
}

// applyDeleteSession soft-deletes a session.  If it was the foreground,
// the App switches to the most recent other primary session (creating a
// fresh one if none exist).
func (a *App) applyDeleteSession(id string) tea.Cmd {
	if a.opts.Store == nil {
		return a.setNotice("delete: no store configured")
	}
	wasForeground := id == a.opts.SessionID
	return func() tea.Msg {
		ctx := a.ctx
		if err := a.opts.Store.SoftDeleteSession(ctx, id); err != nil {
			return clearNoticeMsg{notice: "delete: " + err.Error()}
		}
		if wasForeground {
			// Find another primary session to switch to.
			others, err := a.opts.Store.ListSessions(ctx, session.ListOpts{
				Kind: session.KindPrimary, Limit: 10,
			})
			if err == nil && len(others) > 0 {
				return switchSessionMsg{ID: others[0].ID}
			}
			// No other sessions: clear the foreground so the next input
			// creates a fresh one.
			return switchSessionMsg{ID: ""}
		}
		// Refresh modal list.
		sessions, err := a.opts.Store.ListSessions(ctx, session.ListOpts{
			Limit: 200, IncludeDeleted: true,
		})
		if err != nil {
			return clearNoticeMsg{notice: "delete ok but list reload failed: " + err.Error()}
		}
		return sessionsLoadedMsg{sessions: sessions}
	}
}

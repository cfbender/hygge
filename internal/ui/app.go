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
	Modes []config.ModeConfig
	// AuthConfiguredProviders lists providers with global auth configured
	// (env var or auth store), independent of the active profile.
	AuthConfiguredProviders []string
	SessionID               string // existing session to resume, or "" to create on first input
	ProjectDir              string
	ModelProvider           string // "anthropic" etc, for status bar display
	ModelName               string
	ProfileName             string
	Reasoning               provider.Reasoning
	Commands                *command.Registry // slash-command registry; nil disables slash routing
	Subagents               []MentionSubagent // sub-agent types selectable from @ mentions
	Now                     func() time.Time
	// ContextWindow is the model's maximum context size in tokens.  Used by
	// the compaction modal to display usage info.  0 means unknown.
	ContextWindow int64

	// Version is the application version string shown in persistent chrome (e.g. "v0.4").
	// Empty string hides the version.
	Version string

	// NerdFonts controls whether nerd-font glyphs are used in persistent chrome.
	// When true, the git-branch glyph (U+EAFC) is used; otherwise ":branch".
	// Default false; callers should set this from config.UI.NerdFonts.
	NerdFonts bool

	// HomeDir is the user's home directory, used for tilde-collapsing the
	// project path in persistent chrome. Empty → no collapse.
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
	// modeName is non-empty when the switch came from a mode change; it is empty
	// for direct /model selections. When nil, /model remains a session-only UI
	// selection.
	SwitchModel func(ctx context.Context, providerName, modelName, modeName string) error
	// SaveModel persists a successful provider/model runtime switch.  Save
	// failures are surfaced to the UI without rolling back the runtime switch.
	SaveModel  func(ctx context.Context, providerName, modelName string) error
	SaveAPIKey func(ctx context.Context, providerName, apiKey string) error
	// RememberMemory persists project/global memory. Session memory uses Store.
	RememberMemory func(ctx context.Context, scope session.MemoryScope, content string) (*session.Memory, error)
	// ForgetMemory deletes project/global memory. Session memory uses Store.
	ForgetMemory func(ctx context.Context, scope session.MemoryScope, memoryID string) (*session.Memory, error)
	// ListMemories loads file-backed global/project memories. Session memory uses Store.
	ListMemories func(ctx context.Context) ([]*session.Memory, error)
	// ProjectMemoryGitignoreWarning returns a warning before the first project memory
	// write when .hygge/ may become untracked.
	ProjectMemoryGitignoreWarning func(ctx context.Context) (string, error)
	ThemeNames                    []string
	LoadTheme                     func(ctx context.Context, name string) (*theme.Theme, error)
	SaveTheme                     func(ctx context.Context, name string) error
	// EditPrompt opens the current prompt in an external editor and returns the
	// edited prompt. Tests may inject this seam; production falls back to
	// $VISUAL, then $EDITOR, then vi.
	EditPrompt func(ctx context.Context, initial string) (string, error)
	// NeedsOnboarding opens the first-run onboarding wizard before chat.
	NeedsOnboarding bool
	// KnownProviders lists provider IDs selectable in onboarding.
	KnownProviders []string
	// SaveOnboardingResult persists the completed onboarding configuration.
	SaveOnboardingResult func(ctx context.Context, result OnboardingResult) error
	// GeneratePrompt generates a system prompt during onboarding. It should return
	// an error without blocking manual editing when generation fails.
	GeneratePrompt func(ctx context.Context, providerName, modelName, apiKey, idea string) (string, error)

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
	if opts.NeedsOnboarding {
		providers := append([]string(nil), opts.KnownProviders...)
		if len(providers) == 0 {
			providers = []string{"anthropic", "openai", "openrouter"}
		}
		a.onboardingWizard = components.OnboardingWizard{
			Theme:               opts.Theme,
			Providers:           providers,
			ConfiguredProviders: configuredProviderSet(opts.AuthConfiguredProviders),
			Models:              a.onboardingModelIDs(opts.ModelProvider),
		}
	}
	if opts.SessionID != "" || !opts.OpenSessionsModalOnStart {
		a.bridge()
	}
	return a, nil
}

func configuredProviderSet(providers []string) map[string]bool {
	if len(providers) == 0 {
		return nil
	}
	set := make(map[string]bool, len(providers))
	for _, provider := range providers {
		provider = strings.TrimSpace(provider)
		if provider != "" {
			set[provider] = true
		}
	}
	return set
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
	msgCache               string
	msgCacheValid          bool
	msgCacheStreamingDirty bool      // streaming tail changed while user was scrolled away
	msgCacheW              int       // width at which cache was rendered
	msgCacheLen            int       // message count at which cache was rendered
	msgCacheTime           time.Time // time at which cache was rendered (for relative timestamps)
	subagentHitZones       []components.SubagentHitZone
	toolHitZones           []components.ToolHitZone

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
	// scrollDrag tracks an in-progress drag on the chat scrollbar thumb.
	scrollDragActive     bool
	scrollDragThumbDelta int

	// modal prompt state
	pendingPerms          []components.PermissionRequest // FIFO queue
	pendingQuestions      []components.QuestionRequest   // FIFO queue
	questionSelectedIndex int                            // selected answer in the active question modal
	modalToast            string                         // transient message inside the modal
	overlays              overlayStack                   // typed topmost-first dialog routing foundation

	// status state
	busy        bool
	spinner     spinner.Model
	spinnerTick int
	workingVerb string

	// cost / context state
	costDollars float64
	billedTok   int64
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
	sessionsModal      components.SessionsModal
	memoryModal        components.MemoryModal
	rememberScopeModal components.RememberScopeModal
	modelModal         components.ModelModal
	apiKeyModal        components.APIKeyModal
	themeModal         components.ThemeModal
	onboardingWizard   components.OnboardingWizard

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
	// compactionAnim is the transient chat-block animation shown while a
	// compaction run is building its summary.
	compactionAnim *anim.Anim

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
	// testAgentSteerFn, when non-nil, is called by steerCmd instead of
	// opts.Agent.Steer. Used exclusively by unit tests.
	testAgentSteerFn func(sessionID string, parts []session.Part) error

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
	// (Slug > first-12-chars of ID). Populated at
	// Init (resume path), ensureSession (new-session path), and on
	// bus.MessageAppended for the root session so View() never calls
	// Store.GetSession synchronously on the render goroutine.
	sessionTitle string
	// titleGeneration tracks sessions with an in-flight model-generated title so
	// repeated MessageAppended events do not schedule duplicate rename attempts.
	titleGeneration map[string]bool

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
	if a.opts.NeedsOnboarding {
		a.openOverlay(overlayOnboarding)
		a.updateInputFocus()
	} else if a.opts.OpenSessionsModalOnStart {
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

// Handle delivers a single bus event synchronously, exactly as if it had
// arrived via the listener. Used by tests to drive the App without goroutines.
// Returns the same tea.Cmd Update would.
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
		a.clearSelection()
		a.lastCanvas = uv.ScreenBuffer{}
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

	case components.OnboardingGeneratedPromptReady:
		a.onboardingWizard = a.onboardingWizard.ApplyGeneratedPrompt(m)
		return a, nil

	case components.OnboardingSaved:
		a.onboardingWizard.Saving = false
		if m.Err != nil {
			a.onboardingWizard.SaveError = m.Err.Error()
			return a, nil
		}
		a.opts.NeedsOnboarding = false
		a.closeOverlay(overlayOnboarding)
		return a, a.showToast("Setup complete", "Your first mode is ready")

	case spinner.TickMsg:
		var cmd tea.Cmd
		a.spinner, cmd = a.spinner.Update(m)
		a.spinnerTick++
		if len(a.messages) == 0 {
			a.invalidateMsgCache()
		}
		return a, cmd

	case workingVerbTickMsg:
		if !a.busy {
			return a, nil
		}
		a.workingVerb = components.RandomWorkingVerb()
		return a, a.workingVerbTick()

	case anim.StepMsg:
		if a.compactionAnim != nil {
			updated, cmd := a.compactionAnim.Update(m)
			if cmd != nil {
				a.compactionAnim = updated
				a.invalidateMsgCache()
				return a, cmd
			}
		}
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
		a.compactionAnim = nil
		a.invalidateMsgCache()
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

	case rememberSessionMemoryMsg:
		if m.err != nil {
			a.notice = ""
			return a, a.showToast("Memory not saved", m.err.Error())
		}
		body := m.content
		if len(body) > 80 {
			body = body[:80] + "…"
		}
		title := "Memory saved"
		if m.warning != "" {
			title = "Project memory saved"
			body = m.warning
		}
		a.notice = ""
		return a, a.showToast(title, body)

	case forgetMemoryMsg:
		if m.err != nil {
			a.notice = ""
			return a, a.showToast("Memory not forgotten", m.err.Error())
		}
		a.notice = ""
		cmd := a.showToast("Memory forgotten", m.memoryID)
		if a.overlays.Has(overlayMemory) || a.overlays.Has(overlayMemoryForget) {
			return a, tea.Batch(cmd, a.openMemoryModal())
		}
		return a, cmd

	case sessionsLoadedMsg:
		// Sessions loaded (or reloaded after rename/delete).
		a.sessionsModal.Sessions = m.sessions
		// Clamp cursor to avoid out-of-bounds after a delete.
		filtered := a.sessionsModal.FilteredCount()
		if a.sessionsModal.Cursor >= filtered && filtered > 0 {
			a.sessionsModal.Cursor = filtered - 1
		}
		return a, nil

	case memoriesLoadedMsg:
		if m.err != nil {
			a.memoryModal.Memories = nil
			a.memoryModal.Cursor = 0
			return a, a.setNotice("memory: " + m.err.Error())
		}
		a.memoryModal.Memories = m.memories
		filtered := len(a.memoryModal.Filtered())
		if a.memoryModal.Cursor >= filtered && filtered > 0 {
			a.memoryModal.Cursor = filtered - 1
		}
		return a, nil

	case sessionTitleGeneratedMsg:
		// The title cache is updated by the bus.SessionTitleUpdated handler;
		// this message only clears the in-flight tracking so the next user
		// message can schedule another refresh.
		delete(a.titleGeneration, m.sessionID)
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

	case steerCompleted:
		if m.err != nil {
			return a, a.showToast("Steering not sent", m.err.Error())
		}
		text := strings.TrimSpace(m.text)
		if text != "" {
			a.messages = append(a.messages, uiMessage{
				Role:          components.RoleUser,
				Raw:           "Steering: " + text,
				FinalMarkdown: renderMarkdown(a.ensureRenderer(), "Steering: "+text),
				Timestamp:     a.opts.Now(),
			})
			a.invalidateMsgCache()
		}
		return a, a.showToast("Steering sent", "Applies at the next agent step")

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
			if a.beginScrollBarDrag(m.X, m.Y) {
				a.clearSelection()
				return a, nil
			}
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
			if a.scrollDragActive {
				a.dragScrollBar(m.Y)
				return a, nil
			}
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
			if a.scrollDragActive {
				a.dragScrollBar(m.Y)
				a.scrollDragActive = false
				return a, nil
			}
			cmd := a.handleMouseUp(m.X, m.Y)
			return a, cmd
		}
		return a, nil

	case tea.MouseWheelMsg:
		// Clear selection on scroll.
		a.clearSelection()
		if a.handleCompletionWheel(m) {
			return a, nil
		}
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
// is dropped.

// foregroundID returns the current foreground session id (top of the
// navigation stack).  Falls back to opts.SessionID when the stack is
// empty so the pre-T2.2 lazy-create path still works.
// bus.TodoChanged event arrives for the foreground session.  Runs from the
// Update loop in the bubbletea goroutine so no lock is needed on todosCache.
// covering the input area.  Any open modal → muted border; all modals
// dismissed → accent border.
// handleCompactionModalKey routes key presses when the compaction
// confirmation modal is open.
// kind must be "permission_ask" or "turn_complete".
// The send is best-effort: errors are logged at debug level only.
// openSessionsModal loads the current session list from the store and
// transitions activeModal to "sessions".
// (a.messages[:0]) so any prior content is discarded.
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

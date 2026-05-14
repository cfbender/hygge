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
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"

	"github.com/cfbender/hygge/internal/agent"
	"github.com/cfbender/hygge/internal/bus"
	"github.com/cfbender/hygge/internal/command"
	"github.com/cfbender/hygge/internal/cost"
	"github.com/cfbender/hygge/internal/provider"
	"github.com/cfbender/hygge/internal/session"
	"github.com/cfbender/hygge/internal/ui/components"
	"github.com/cfbender/hygge/internal/ui/theme"
)

// AppOptions configures the App.
type AppOptions struct {
	Bus           *bus.Bus
	Agent         *agent.Agent
	Store         session.Store
	Catalog       *cost.Catalog
	Theme         *theme.Theme
	SessionID     string // existing session to resume, or "" to create on first input
	ProjectDir    string
	ModelProvider string // "anthropic" etc, for status bar display
	ModelName     string
	ProfileName   string
	Reasoning     provider.Reasoning
	Commands      *command.Registry // slash-command registry; nil disables slash routing
	Now           func() time.Time

	// OnSessionCreated, if non-nil, is invoked after the App lazily
	// creates a new session on first Send.  The CLI uses this to record
	// the new id in state (RecentSessions).  Best-effort; errors are
	// swallowed internally.
	OnSessionCreated func(id string)
}

// uiMessage is the App's internal alias for the components.UIMessage view
// model.  Kept here so appmsg.go and tests can refer to it without importing
// the components package.
type uiMessage = components.UIMessage

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

	ctx, cancel := context.WithCancel(context.Background())
	a := &App{
		opts:      opts,
		ctx:       ctx,
		cancel:    cancel,
		busCh:     make(chan any, 256),
		input:     components.NewInput(opts.Theme),
		spinner:   spinner.New(),
		width:     80,
		height:    24,
		subagents: make(map[string]*components.SubagentState),
	}
	a.spinner.Spinner = spinner.Dot
	a.bridge()
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

	// renderer is the glamour TermRenderer; rebuilt on resize.
	renderer  *glamour.TermRenderer
	rendererW int

	// messages is the conversation buffer.
	messages []uiMessage

	// permission state
	pendingPerms []components.PermissionRequest // FIFO queue
	modalToast   string                         // transient message inside the modal

	// status state
	busy        bool
	spinner     spinner.Model
	spinnerTick int

	// cost / context state
	costDollars float64
	usedTok     int64
	maxTok      int64
	pctUsed     float64

	// input + send state
	input *components.Input
	// inflightCancel cancels the current Send.
	inflightCancel context.CancelFunc

	// notice is the ephemeral status line raised by slash commands
	// and surfaced briefly under the input.  Cleared on a timer or
	// the next slash invocation.
	notice string

	// paletteHighlight is the current row index into the active
	// command palette matches.  -1 means "no row highlighted".
	// Reset on every buffer change.
	paletteHighlight int

	// activeModal is the named modal currently open from a slash
	// command Outcome (help / sessions).  Empty means none.
	// v0.3 just records the request; rich modal rendering for help
	// and sessions lands alongside T1.2 / T2.3.
	activeModal string

	// subagents tracks every in-flight or completed sub-agent
	// invocation whose root ancestor is opts.SessionID.  Keyed by
	// sub-session id.  Populated on bus.SubagentStarted, updated by
	// the sub-session's normal streaming events, finalised on
	// bus.SubagentCompleted.  Stage A blocks recursion at the
	// runtime layer so depth is currently at most 1 -- but
	// isDescendant() below walks the chain so future relaxation
	// does not break the filter.
	subagents map[string]*components.SubagentState

	// closed protects against double Close.
	closeOnce sync.Once
}

// Init is the bubbletea Model entry point.  Starts the input focus, the
// spinner tick, and the bus listener.
func (a *App) Init() tea.Cmd {
	return tea.Batch(
		a.input.Textarea.Focus(),
		a.spinner.Tick,
		a.listenBus(),
	)
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
	subIter := bus.Subscribe[bus.IterationLimitReached](a.opts.Bus, bus.SubscribeOptions{BufferSize: 8})
	subSubStart := bus.Subscribe[bus.SubagentStarted](a.opts.Bus, bus.SubscribeOptions{BufferSize: 16})
	subSubDone := bus.Subscribe[bus.SubagentCompleted](a.opts.Bus, bus.SubscribeOptions{BufferSize: 16})

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
	go forward(subIter.C(), a.busCh, stop, subIter.Unsubscribe)
	go forward(subSubStart.C(), a.busCh, stop, subSubStart.Unsubscribe)
	go forward(subSubDone.C(), a.busCh, stop, subSubDone.Unsubscribe)
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
func (a *App) View() string {
	width := a.width
	if width <= 0 {
		width = 80
	}
	height := a.height
	if height <= 0 {
		height = 24
	}

	sb := components.StatusBar{
		Profile:     a.opts.ProfileName,
		Provider:    a.opts.ModelProvider,
		Model:       a.opts.ModelName,
		Pwd:         a.opts.ProjectDir,
		Busy:        a.busy,
		SpinnerTick: a.spinnerTick,
		Width:       width,
		Theme:       a.opts.Theme,
	}.View()

	ml := components.MessageList{
		Width:     width,
		Theme:     a.opts.Theme,
		Messages:  a.messages,
		Subagents: a.subagents,
		Now:       a.opts.Now(),
	}.View()

	in := a.input.View()

	// Inline command palette: shown immediately above the input when
	// the buffer starts with "/" and a registry is configured.
	palette := ""
	if a.opts.Commands != nil && strings.HasPrefix(a.input.Value(), "/") {
		matches := a.paletteMatches()
		head, _ := splitSlash(a.input.Value())
		p := components.CommandPalette{
			Width:           width - 2,
			Theme:           a.opts.Theme,
			Matches:         matches,
			Highlight:       a.clampedPaletteHighlight(matches),
			QueryAfterSlash: head,
		}
		palette = p.View()
	}

	// Notice line: one row immediately under the input, dimmed.
	// Rendered only when set so the layout stays compact otherwise.
	notice := ""
	if a.notice != "" {
		style := lipgloss.NewStyle()
		if a.opts.Theme != nil {
			style = a.opts.Theme.Style(theme.AtomMuted)
		}
		notice = style.Render(a.notice)
	}

	fr := components.Footer{
		Width:   width,
		Theme:   a.opts.Theme,
		Cost:    a.costDollars,
		UsedTok: a.usedTok,
		MaxTok:  a.maxTok,
		PctUsed: a.pctUsed,
		Busy:    a.busy,
	}.View()

	// Reserve a small fixed budget for chrome and let the message list take
	// the remainder.  No hard scrolling in v0.1 — the message list just
	// renders the full buffer and the bottom of the terminal shows whatever
	// fits.  Task 13's CLI will wrap us in `tea.WithAltScreen` if desired.
	chrome := lipgloss.Height(sb) + lipgloss.Height(in) + lipgloss.Height(fr)
	if palette != "" {
		chrome += lipgloss.Height(palette)
	}
	if notice != "" {
		chrome += lipgloss.Height(notice)
	}
	bodyHeight := height - chrome
	if bodyHeight < 1 {
		bodyHeight = 1
	}
	bodyStyle := lipgloss.NewStyle().Height(bodyHeight)
	body := bodyStyle.Render(ml)

	sections := []string{sb, body}
	if palette != "" {
		sections = append(sections, palette)
	}
	sections = append(sections, in)
	if notice != "" {
		sections = append(sections, notice)
	}
	sections = append(sections, fr)
	main := strings.Join(sections, "\n")

	// Modal overlays the entire screen when there's a pending permission.
	if len(a.pendingPerms) > 0 {
		modal := components.PermissionModal{
			Width:   width,
			Height:  height,
			Theme:   a.opts.Theme,
			Request: a.pendingPerms[0],
			Toast:   a.modalToast,
		}.View()
		return modal
	}

	return main
}

// Update is the bubbletea Update method.
func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m := msg.(type) {

	case tea.WindowSizeMsg:
		a.width = m.Width
		a.height = m.Height
		a.input.SetWidth(m.Width - 2) // border padding
		// Glamour renderer is sized to the body width; rebuild lazily.
		a.renderer = nil
		a.rendererW = 0
		return a, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		a.spinner, cmd = a.spinner.Update(m)
		a.spinnerTick++
		return a, cmd

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

	case clearNoticeMsg:
		// Only clear when we are still showing the same notice the
		// timer was scheduled for — otherwise a fresher notice that
		// landed in the meantime would be wiped.
		if a.notice == m.notice {
			a.notice = ""
		}
		return a, nil

	case sendStarted:
		a.busy = true
		return a, nil

	case sendCompleted:
		a.busy = false
		a.inflightCancel = nil
		if m.Err != nil && !errors.Is(m.Err, context.Canceled) {
			// Surface the failure so the user has something to react to;
			// silently dropping errors leaves the UI looking dead.
			a.messages = append(a.messages, uiMessage{
				Role:    components.RoleSystem,
				Raw:     "error: " + m.Err.Error(),
				IsError: true,
			})
		}
		return a, nil

	case tea.KeyMsg:
		return a.handleKey(m)

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

// handleKey dispatches a key.  When the modal is open, only the modal
// keybinds work; everything else is dropped.
func (a *App) handleKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if len(a.pendingPerms) > 0 {
		return a.handleModalKey(k)
	}

	switch k.Type {
	case tea.KeyCtrlC:
		if a.busy && a.inflightCancel != nil {
			a.inflightCancel()
			return a, nil
		}
		return a, tea.Quit
	case tea.KeyCtrlL:
		a.input.Reset()
		return a, nil
	case tea.KeyCtrlT:
		// Toggle the most recent sub-agent block.  Chosen over
		// `tab` (would conflict with textarea tab-insertion) and
		// over a bare letter key (would conflict with input mode).
		// `ctrl+t` is otherwise unbound by the textarea bubble.
		a.toggleLatestSubagent()
		return a, nil
	case tea.KeyEnter:
		// Alt+Enter inserts a newline; we differentiate by Alt flag below.
		if k.Alt {
			break // fall through to textarea.Update so it inserts a newline
		}
		if a.busy {
			return a, nil // input blocked
		}
		text := strings.TrimSpace(a.input.Value())
		if text == "" {
			return a, nil
		}
		if strings.HasPrefix(text, "/") {
			a.input.Reset()
			return a, a.runSlashCommand(text)
		}
		a.input.Reset()
		return a, a.startSend(text)
	case tea.KeyTab:
		// Tab completes the currently-highlighted command palette
		// entry when the input is in slash mode.  Outside slash
		// mode it falls through to the textarea (default insert).
		if a.opts.Commands != nil && strings.HasPrefix(a.input.Value(), "/") {
			matches := a.paletteMatches()
			hi := a.clampedPaletteHighlight(matches)
			if hi >= 0 {
				a.input.Textarea.SetValue("/" + matches[hi].Name() + " ")
				// Move cursor to end so further typing extends args.
				a.input.Textarea.CursorEnd()
			}
			return a, nil
		}
	case tea.KeyEsc:
		// Esc dismisses the command palette without changing input.
		// Outside slash mode it falls through (the textarea has no
		// Esc binding by default, so this is a no-op there).
		if a.opts.Commands != nil && strings.HasPrefix(a.input.Value(), "/") {
			a.input.Reset()
			return a, nil
		}
	case tea.KeyUp:
		if a.opts.Commands != nil && strings.HasPrefix(a.input.Value(), "/") {
			a.movePaletteHighlight(-1)
			return a, nil
		}
	case tea.KeyDown:
		if a.opts.Commands != nil && strings.HasPrefix(a.input.Value(), "/") {
			a.movePaletteHighlight(+1)
			return a, nil
		}
	}

	var cmd tea.Cmd
	a.input.Textarea, cmd = a.input.Textarea.Update(k)
	return a, cmd
}

// handleModalKey routes keys to the permission modal.
func (a *App) handleModalKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
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

	switch k.Type {
	case tea.KeyEsc:
		a.pendingPerms = a.pendingPerms[1:]
		a.modalToast = ""
		return a, reply("deny", "once")
	case tea.KeyRunes:
		if len(k.Runes) != 1 {
			return a, nil
		}
		switch k.Runes[0] {
		case 'y':
			a.pendingPerms = a.pendingPerms[1:]
			a.modalToast = ""
			return a, reply("allow", "once")
		case 'Y':
			a.pendingPerms = a.pendingPerms[1:]
			a.modalToast = ""
			return a, reply("allow", "session")
		case 'A':
			a.pendingPerms = a.pendingPerms[1:]
			a.modalToast = ""
			return a, reply("allow", "always")
		case 'n', 'N':
			a.pendingPerms = a.pendingPerms[1:]
			a.modalToast = ""
			return a, reply("deny", "once")
		case 'e', 'E':
			a.modalToast = "edit not yet implemented (v0.2)"
			return a, tea.Tick(2*time.Second, func(time.Time) tea.Msg { return clearToastMsg{} })
		}
	}
	return a, nil
}

// startSend launches a goroutine that calls Agent.Send and returns the
// resulting tea.Cmds (sendStarted now, sendCompleted later).
func (a *App) startSend(text string) tea.Cmd {
	if a.opts.Agent == nil {
		// No agent wired up — useful for tests that just want to verify
		// input handling.  Just emit sendStarted then sendCompleted so the
		// busy state cycles for the test.
		return func() tea.Msg {
			return sendStarted{UserInput: text, StartedAt: a.opts.Now()}
		}
	}
	// Optimistically render the user message so they see it before the
	// provider responds.
	a.messages = append(a.messages, uiMessage{Role: components.RoleUser, Raw: text})

	ctx, cancel := context.WithCancel(a.ctx)
	a.inflightCancel = cancel

	return tea.Batch(
		func() tea.Msg { return sendStarted{UserInput: text, StartedAt: a.opts.Now()} },
		func() tea.Msg {
			sid, err := a.ensureSession(ctx)
			if err != nil {
				return sendCompleted{Err: err}
			}
			msg, err := a.opts.Agent.Send(ctx, sid, []session.Part{
				{Kind: session.PartText, Text: text},
			})
			return sendCompleted{Result: msg, Err: err}
		},
	)
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
		a.appendAssistantDelta(e.Text)

	case bus.AssistantThinkingDelta:
		// v0.1: ignore.  Could be rendered as a faint sidebar in v0.2.

	case bus.MessageAppended:
		if a.routeToSubagent(e.SessionID) {
			a.flushSubagentStream(e.SessionID, e.Role)
			return nil
		}
		if !a.isForeground(e.SessionID) {
			return nil
		}
		a.flushAssistantStream(e.Role)

	case bus.ToolCallRequested:
		if a.routeToSubagent(e.SessionID) {
			a.appendSubagentTool(e.SessionID, e.ToolName, extractTarget(e.Args))
			return nil
		}
		if !a.isForeground(e.SessionID) {
			return nil
		}
		target := extractTarget(e.Args)
		a.messages = append(a.messages, uiMessage{
			Role:        components.RoleTool,
			ToolName:    e.ToolName,
			Target:      target,
			Raw:         "(running…)",
			IsStreaming: true,
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
		if a.routeToSubagent(e.SessionID) {
			a.updateSubagentCost(e.SessionID, e)
			return nil
		}
		if !a.isForeground(e.SessionID) {
			return nil
		}
		a.costDollars = e.DollarsTotal

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
	}
	return nil
}

// foregroundID returns the current foreground session id, or "" if no
// session has been bound yet.
func (a *App) foregroundID() string {
	return a.opts.SessionID
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

// routeToSubagent reports whether sessionID matches a tracked
// sub-agent state.  Walks via the SubagentState chain so future
// multi-level recursion remains supported even though the runtime
// currently blocks it at depth 1.
func (a *App) routeToSubagent(sessionID string) bool {
	if sessionID == "" {
		return false
	}
	_, ok := a.subagents[sessionID]
	return ok
}

// onSubagentStarted reacts to bus.SubagentStarted.  Filtering: only
// track sub-agents whose parent chain roots at the foreground session.
// The state is bound to the most-recent unfinished `task` tool message
// (preferring an exact ToolUseID match, falling back to the most
// recent streaming task entry) so the message list can render the
// nested block in the right place.
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

	a.attachSubagentToTaskMessage(state)

	// Drive the elapsed-time tick while running.  Coalesces with
	// the spinner Tick that's already in flight; bubbletea handles
	// multiple Tick'ers fine.
	return a.subagentTick(e.SubSessionID)
}

// attachSubagentToTaskMessage walks the message buffer for the
// matching `task` tool message and stamps SubagentID on it.  Tries an
// exact ToolUseID match first; falls back to the most recent
// streaming task message.  Doing nothing when no candidate exists
// (the block then renders only once a future task message claims it,
// which is fine -- the nested view is bound to the message, not the
// state).
func (a *App) attachSubagentToTaskMessage(state *components.SubagentState) {
	for i := len(a.messages) - 1; i >= 0; i-- {
		msg := &a.messages[i]
		if msg.Role != components.RoleTool || msg.ToolName != "task" {
			continue
		}
		if state.ParentMessageID != "" && msg.ToolUseID != "" &&
			msg.ToolUseID != state.ParentMessageID {
			continue
		}
		if msg.SubagentID != "" && msg.SubagentID != state.SubSessionID {
			continue
		}
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
		Role:        components.RoleAssistant,
		Raw:         text,
		IsStreaming: true,
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

// toggleLatestSubagent flips the Expanded flag on the most recently
// started sub-agent block (running or completed).  No-op when no
// sub-agent has been tracked yet.  Chosen as the simplest UX
// consistent with the existing scroll-and-render TUI: a single
// keybind toggles the obviously-latest block.  When cursor-based
// navigation lands (v0.3), this should be replaced by a per-block
// toggle keyed off the cursor selection.
func (a *App) toggleLatestSubagent() {
	if len(a.subagents) == 0 {
		return
	}
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
		return
	}
	latest.Expanded = !latest.Expanded
}

// appendAssistantDelta appends text to the streaming assistant message, or
// starts a new one if the last message isn't a streaming assistant.
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
	})
}

// flushAssistantStream marks the most recent assistant message as final and
// renders it through glamour.
func (a *App) flushAssistantStream(role string) {
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
	last.FinalMarkdown = renderMarkdown(a.ensureRenderer(), last.Raw)
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
		} else {
			msg.Raw = string(e.Result)
		}
		return
	}
	// No matching streaming tool entry — synthesise one.
	out := string(e.Result)
	if e.Err != "" {
		out = e.Err
	}
	a.messages = append(a.messages, uiMessage{
		Role:     components.RoleTool,
		ToolName: e.ToolName,
		Raw:      out,
		IsError:  e.Err != "",
	})
}

// ensureRenderer constructs (or returns the cached) glamour renderer for the
// current width.
func (a *App) ensureRenderer() *glamour.TermRenderer {
	if a.renderer != nil && a.rendererW == a.width {
		return a.renderer
	}
	w := a.width
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

// extractTarget makes a best-effort attempt to surface a useful target
// string (path, command) from a tool's raw JSON args.  Returns "" when the
// args don't decode or don't contain anything obvious.  We are intentionally
// duck-typed here — internal/ui must not depend on internal/tool schemas.
func extractTarget(args []byte) string {
	s := string(args)
	for _, key := range []string{`"path"`, `"file"`, `"command"`, `"url"`, `"target"`} {
		if idx := strings.Index(s, key); idx >= 0 {
			rest := s[idx+len(key):]
			rest = strings.TrimLeft(rest, " \t:")
			if !strings.HasPrefix(rest, `"`) {
				continue
			}
			rest = rest[1:]
			end := strings.Index(rest, `"`)
			if end < 0 {
				continue
			}
			return rest[:end]
		}
	}
	return ""
}

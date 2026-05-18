package ui

import (
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/cfbender/hygge/internal/session"
	"github.com/cfbender/hygge/internal/ui/components"
	"github.com/cfbender/hygge/internal/ui/components/anim"
)

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

// --- Remember scope modal integration ---------------------------------------

func (a *App) handleRememberScopeModalKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	sk := components.RememberScopeKey{Name: k.String()}
	switch k.String() {
	case "up":
		sk.Name = "up"
	case "down":
		sk.Name = "down"
	case "enter":
		sk.Name = "enter"
	case "esc":
		sk.Name = "esc"
	}

	updated, msg := a.rememberScopeModal.HandleKey(sk)
	a.rememberScopeModal = updated
	if msg == nil {
		return a, nil
	}
	return a, a.applyRememberScopeModalMsg(msg)
}

func (a *App) applyRememberScopeModalMsg(msg components.RememberScopeModalMsg) tea.Cmd {
	switch m := msg.(type) {
	case components.CloseRememberScopeModal:
		a.closeOverlay(overlayMemoryRemember)
		return nil
	case components.RememberScopeAction:
		a.closeOverlay(overlayMemoryRemember)
		return a.rememberSessionMemoryCmd(string(m.Scope) + "\n" + m.Content)
	}
	return nil
}

// --- Memory modal integration ----------------------------------------------

func (a *App) openMemoryModal() tea.Cmd {
	return func() tea.Msg {
		var memories []*session.Memory
		if a.opts.Store != nil && a.opts.SessionID != "" {
			sessionMemories, err := a.opts.Store.ListSessionMemories(a.ctx, a.opts.SessionID)
			if err != nil {
				return memoriesLoadedMsg{err: err}
			}
			memories = append(memories, sessionMemories...)
		}
		if a.opts.ListMemories != nil {
			fileMemories, err := a.opts.ListMemories(a.ctx)
			if err != nil {
				return memoriesLoadedMsg{err: err}
			}
			memories = append(memories, fileMemories...)
		}
		return memoriesLoadedMsg{memories: memories}
	}
}

func (a *App) handleMemoryModalKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	sk := components.MemoryKey{Name: k.String(), Runes: []rune(k.Text)}
	switch k.String() {
	case "up":
		sk.Name = "up"
	case "down":
		sk.Name = "down"
	case "enter":
		sk.Name = "enter"
	case "esc":
		sk.Name = "esc"
	case "backspace", "delete":
		sk.Name = "backspace"
	default:
		if len(k.Text) == 1 {
			sk.Name = k.Text
		}
	}

	updated, msg := a.memoryModal.HandleKey(sk)
	a.memoryModal = updated
	if msg == nil {
		return a, nil
	}
	return a, a.applyMemoryModalMsg(msg)
}

func (a *App) applyMemoryModalMsg(msg components.MemoryModalMsg) tea.Cmd {
	switch m := msg.(type) {
	case components.CloseMemoryModal:
		a.closeOverlay(overlayMemory)
		a.closeOverlay(overlayMemoryForget)
		return nil
	case components.ForgetMemoryAction:
		return a.forgetMemoryCmd(string(m.Scope) + "\n" + m.ID)
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
		a.todosCache = nil
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

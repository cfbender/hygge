package ui

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/cfbender/hygge/internal/command"
	"github.com/cfbender/hygge/internal/config"
	"github.com/cfbender/hygge/internal/provider"
	"github.com/cfbender/hygge/internal/session"
	"github.com/cfbender/hygge/internal/ui/components"
	"github.com/cfbender/hygge/internal/ui/styles"
)

// noticeLifetime is how long an ephemeral slash-command notice
// remains visible.  Long enough to read; short enough to not
// overstay its welcome.
const noticeLifetime = 4 * time.Second

// runSlashCommand parses text (which begins with "/"), looks the
// command up in the registry, and returns a tea.Cmd that runs it
// and applies the [command.Outcome] to the App.  Errors are
// surfaced as a notice rather than a tea error: the input loop
// should not crash when the user mistypes a command name.
func (a *App) runSlashCommand(text string) tea.Cmd {
	if a.opts.Commands == nil {
		return a.setNotice("commands unavailable (no registry configured)")
	}
	name, body := splitSlash(text)
	cmd, ok := a.opts.Commands.Get(name)
	if !ok {
		return a.setNotice("unknown command /" + name + " — try /help")
	}
	adapter := &commandAppAdapter{a: a}
	// Synchronous: built-in commands never block; template
	// commands do trivial string work.
	out, err := cmd.Execute(a.ctx, adapter, body)
	if err != nil {
		return a.setNotice("command failed: " + err.Error())
	}
	return a.applyOutcome(out)
}

// splitSlash splits "/name rest of text" into ("name", "rest of text").
// The leading slash is required and stripped; surrounding whitespace
// on the command name and the body is trimmed.
func splitSlash(text string) (name, body string) {
	text = strings.TrimPrefix(text, "/")
	idx := strings.IndexAny(text, " \t")
	if idx < 0 {
		return strings.TrimSpace(text), ""
	}
	return strings.TrimSpace(text[:idx]), strings.TrimLeft(text[idx:], " \t")
}

// applyOutcome interprets the fields of out and returns a single
// tea.Cmd that performs every effect the outcome asked for.  Several
// fields may be set on the same outcome; applyOutcome combines them with
// tea.Batch.
func (a *App) applyOutcome(out command.Outcome) tea.Cmd {
	var cmds []tea.Cmd

	if out.NewSession {
		cmds = append(cmds, a.applySwitchSession(""))
	}

	if out.ClearHistory {
		a.messages = nil
		a.lastAssistantFlushIdx = -1
		a.subagents = map[string]*components.SubagentState{}
		a.renderer = nil
		a.rendererW = 0
	}

	apiKeyProvider := strings.TrimSpace(out.Updates["apikey_provider"])
	if out.OpenModal != "" {
		switch out.OpenModal {
		case command.ModalHelp:
			a.openOverlay(overlayHelp)
			a.updateInputFocus()
		case command.ModalSessions:
			a.openOverlay(overlaySessions)
			a.sessionsModal = components.SessionsModal{
				Theme:         a.opts.Theme,
				ForegroundID:  a.opts.SessionID,
				ShowSubagents: false,
				ShowDeleted:   false,
			}
			a.updateInputFocus()
			return tea.Batch(append(cmds, a.openSessionsModal())...)
		case command.ModalMemory, command.ModalForgetMemory:
			forgetOnly := out.OpenModal == command.ModalForgetMemory
			kind := overlayMemory
			if forgetOnly {
				kind = overlayMemoryForget
			}
			a.openOverlay(kind)
			a.memoryModal = components.MemoryModal{Theme: a.opts.Theme, ForgetOnly: forgetOnly}
			a.updateInputFocus()
			return tea.Batch(append(cmds, a.openMemoryModal())...)
		case command.ModalRememberMemory:
			a.openOverlay(overlayMemoryRemember)
			a.rememberScopeModal = components.RememberScopeModal{Theme: a.opts.Theme, Content: out.Updates[command.UpdateRememberMemoryDraft]}
			a.updateInputFocus()
		case command.ModalCompactConfirm:
			// Populate the modal with live session metadata.
			msgCount := a.resolveCompactionMessageCount()
			a.compactionModal = components.CompactionModal{
				Theme:         a.opts.Theme,
				SessionID:     a.foregroundID(),
				MessageCount:  msgCount,
				ContextPct:    a.pctUsed * 100,
				ContextWindow: a.opts.ContextWindow,
			}
			a.openOverlay(overlayCompactConfirm)
			a.updateInputFocus()
		case command.ModalModel:
			a.modelModal = components.ModelModal{
				Theme:     a.opts.Theme,
				Current:   a.opts.ModelProvider + "/" + a.opts.ModelName,
				Models:    a.catalogModelOptions(),
				Favorites: append([]string(nil), a.opts.FavoriteModels...),
			}
			a.openOverlay(overlayModel)
			a.updateInputFocus()
		case command.ModalAPIKey:
			if apiKeyProvider == "" {
				apiKeyProvider = a.opts.ModelProvider
			}
			if apiKeyProvider == "" {
				return a.setNotice("/apikey: no current provider")
			}
			if !a.knownProvider(apiKeyProvider) {
				return a.setNotice("/apikey: unknown provider " + apiKeyProvider)
			}
			a.apiKeyModal = components.APIKeyModal{Theme: a.opts.Theme, Provider: apiKeyProvider, HasExisting: providerHasAPIKey(a.opts.Config, apiKeyProvider, a.opts.ModelProvider)}
			a.openOverlay(overlayAPIKey)
			a.updateInputFocus()
		case command.ModalTheme:
			themeNames := a.themeNames()
			previewThemes := make(map[string]*styles.Styles, len(themeNames)+1)
			if a.opts.Theme != nil {
				previewThemes[currentThemeName(a.opts.Theme)] = a.opts.Theme
			}
			for _, name := range themeNames {
				if _, ok := previewThemes[name]; ok {
					continue
				}
				if a.opts.LoadTheme != nil {
					if th, err := a.opts.LoadTheme(a.ctx, name); err == nil {
						previewThemes[name] = th
						continue
					}
				}
				if th, err := styles.Load(name, styles.LoadOptions{}); err == nil {
					previewThemes[name] = th
				}
			}
			a.themeModal = components.ThemeModal{Theme: a.opts.Theme, Current: currentThemeName(a.opts.Theme), Themes: themeNames, PreviewTheme: func(name string) *styles.Styles { return previewThemes[name] }}
			a.openOverlay(overlayTheme)
			a.updateInputFocus()
		default:
			slogWarnUnknownModal(out.OpenModal)
		}
	}

	for _, key := range orderedUpdateKeys(out.Updates) {
		if cmd := a.applyUpdate(key, out.Updates[key]); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}

	if out.Compact && a.opts.Agent != nil && a.opts.SessionID != "" {
		// Run compaction on a goroutine — it issues at least one
		// provider call, which can be slow.  The result is folded
		// back into the UI through the normal bus events.
		sid := a.opts.SessionID
		cmds = append(cmds, func() tea.Msg {
			ctx, cancel := context.WithTimeout(a.ctx, 60*time.Second)
			defer cancel()
			if _, err := a.opts.Agent.Compact(ctx, sid); err != nil {
				return clearNoticeMsg{notice: ""} // no-op fallback
			}
			return nil
		})
	}

	if out.Message != "" && a.opts.Agent != nil {
		// Reuse the existing send path so streaming + cost events
		// flow through unchanged.
		cmds = append(cmds, a.startSend(out.Message))
	}

	// Drain the pending fork intent set by applyUpdate for UpdateForkAt.
	if a.forkPendingID != "" {
		fromID := a.forkPendingID
		msgID := a.forkPendingMsgID
		a.forkPendingID = ""
		a.forkPendingMsgID = ""
		cmds = append(cmds, a.applyForkSession(fromID, msgID))
	}

	if out.Notice != "" {
		cmds = append(cmds, a.setNotice(out.Notice))
	}

	return tea.Batch(cmds...)
}

func (a *App) steerCmd(text string) tea.Cmd {
	return func() tea.Msg {
		sid := a.foregroundID()
		if sid == "" {
			return steerCompleted{text: text, err: fmt.Errorf("no active session to steer")}
		}
		parts := []session.Part{{Kind: session.PartText, Text: text}}
		if a.testAgentSteerFn != nil {
			return steerCompleted{text: text, err: a.testAgentSteerFn(sid, parts)}
		}
		if a.opts.Agent == nil {
			return steerCompleted{text: text, err: fmt.Errorf("agent unavailable")}
		}
		return steerCompleted{text: text, err: a.opts.Agent.Steer(sid, parts)}
	}
}

// applyUpdate dispatches a single Outcome.Updates entry. Unknown keys are
// logged and ignored. Model changes return a command so provider/model rebuilds
// happen off the render loop; reasoning remains local UI state.
func orderedUpdateKeys(updates map[string]string) []string {
	if len(updates) == 0 {
		return nil
	}
	keys := make([]string, 0, len(updates))
	for _, key := range []string{command.UpdateMode, command.UpdateModel} {
		if _, ok := updates[key]; ok {
			keys = append(keys, key)
		}
	}
	for key := range updates {
		if key != command.UpdateMode && key != command.UpdateModel {
			keys = append(keys, key)
		}
	}
	return keys
}

func (a *App) applyUpdate(key, value string) tea.Cmd {
	switch key {
	case command.UpdateModel:
		if provName, modelName, ok := splitModelRef(value); ok {
			return a.switchModelCmd(provName, modelName)
		}
	case command.UpdateTheme:
		name := strings.TrimSpace(value)
		if name != "" {
			return a.switchThemeCmd(name)
		}
	case command.UpdateMode:
		return a.switchModeByName(value)
	case command.UpdateYolo:
		return a.switchYoloCmd(value)
	case command.UpdateRememberSessionMemory:
		return a.rememberSessionMemoryCmd(value)
	case command.UpdateRememberMemoryDraft:
		return nil
	case command.UpdateForgetMemory:
		return a.forgetMemoryCmd(value)
	case command.UpdateQueueMessage:
		a.enqueuePromptDraft(queuedPromptDraft{Text: value})
		return a.showToast("Message queued", "It will send after the active turn")
	case command.UpdateSteerMessage:
		return a.steerCmd(value)
	case command.UpdateReasoning:
		switch value {
		case "off", "low", "medium", "high":
			a.opts.Reasoning = provider.Reasoning{Effort: value}
		}
	case command.UpdateForkAt:
		// Wire the fork-at action.  "latest" (or empty) resolves to the
		// foreground session's most recent user message.
		if a.opts.SessionID == "" {
			break
		}
		fromID := a.opts.SessionID
		// Enqueue the fork as a cmd — cannot return it from applyUpdate directly.
		// The caller (applyOutcome) collects cmds separately; store in a
		// deferred cmd via a side channel.  We set a flag on the app and the
		// next applyOutcome call picks it up.
		//
		// Simpler approach: just run the fork inline here as a goroutine-based
		// tea.Cmd.  applyUpdate can't return a cmd, so stash it.
		_ = fromID // used below when we generate the notice
		// The fork is triggered by returning a cmd from applyOutcome, not here.
		// We signal via the forkPending flag which applyOutcome checks.
		a.forkPendingID = fromID
		a.forkPendingMsgID = value
	case command.UpdateAttachFile:
		att, err := loadPromptAttachment(value)
		if err != nil {
			a.notice = "attach: " + err.Error()
			break
		}
		a.pendingAttachments = append(a.pendingAttachments, att)
		a.notice = fmt.Sprintf("attached %s (%s)", att.Name, formatBytes(att.Size))
	case command.UpdateAttachments:
		if value == "clear" {
			n := len(a.pendingAttachments)
			a.pendingAttachments = nil
			a.notice = fmt.Sprintf("cleared %d attachment(s)", n)
		}
	case command.UpdateLayout:
		return a.applyLayoutOverride(value)
	default:
		if key == "apikey_provider" {
			return nil
		}
		slogWarnUnknownUpdate(key, value)
	}
	return nil
}

func (a *App) rememberSessionMemoryCmd(value string) tea.Cmd {
	scope, content := parseRememberUpdate(value)
	if content == "" {
		return func() tea.Msg { return rememberSessionMemoryMsg{err: fmt.Errorf("usage: /remember <fact>")} }
	}
	if scope == session.MemoryScopeProject || scope == session.MemoryScopeGlobal {
		if a.opts.RememberMemory == nil {
			return func() tea.Msg { return rememberSessionMemoryMsg{err: fmt.Errorf("%s memory unavailable", scope)} }
		}
		return func() tea.Msg {
			var warning string
			if scope == session.MemoryScopeProject && a.opts.ProjectMemoryGitignoreWarning != nil {
				var err error
				warning, err = a.opts.ProjectMemoryGitignoreWarning(a.ctx)
				if err != nil {
					return rememberSessionMemoryMsg{err: err}
				}
			}
			_, err := a.opts.RememberMemory(a.ctx, scope, content)
			return rememberSessionMemoryMsg{content: content, warning: warning, err: err}
		}
	}
	if a.opts.Store == nil {
		return func() tea.Msg { return rememberSessionMemoryMsg{err: fmt.Errorf("no store configured")} }
	}
	if a.opts.SessionID == "" {
		return func() tea.Msg { return rememberSessionMemoryMsg{err: fmt.Errorf("no active session yet")} }
	}
	sessionID := a.opts.SessionID
	return func() tea.Msg {
		latestUserID, err := a.opts.Store.LatestUserMessageID(a.ctx, sessionID)
		if err != nil {
			return rememberSessionMemoryMsg{err: fmt.Errorf("lookup latest user message: %w", err)}
		}
		if latestUserID == "" {
			return rememberSessionMemoryMsg{err: fmt.Errorf("send a message before saving session memory")}
		}
		_, err = a.opts.Store.RememberSessionMemory(a.ctx, sessionID, session.NewMemory{Content: content})
		return rememberSessionMemoryMsg{content: content, err: err}
	}
}

func parseRememberUpdate(value string) (session.MemoryScope, string) {
	return parseMemoryUpdate(value)
}

func (a *App) forgetMemoryCmd(value string) tea.Cmd {
	scope, memoryID := parseForgetUpdate(value)
	if memoryID == "" {
		return func() tea.Msg { return forgetMemoryMsg{err: fmt.Errorf("usage: /forget <memory-id>")} }
	}
	if scope == session.MemoryScopeProject || scope == session.MemoryScopeGlobal {
		if a.opts.ForgetMemory == nil {
			return func() tea.Msg { return forgetMemoryMsg{err: fmt.Errorf("%s memory unavailable", scope)} }
		}
		return func() tea.Msg {
			_, err := a.opts.ForgetMemory(a.ctx, scope, memoryID)
			return forgetMemoryMsg{memoryID: memoryID, err: err}
		}
	}
	if a.opts.Store == nil {
		return func() tea.Msg { return forgetMemoryMsg{err: fmt.Errorf("no store configured")} }
	}
	if a.opts.SessionID == "" {
		return func() tea.Msg { return forgetMemoryMsg{err: fmt.Errorf("no active session yet")} }
	}
	sessionID := a.opts.SessionID
	return func() tea.Msg {
		_, err := a.opts.Store.ForgetSessionMemory(a.ctx, sessionID, memoryID)
		return forgetMemoryMsg{memoryID: memoryID, err: err}
	}
}

func parseForgetUpdate(value string) (session.MemoryScope, string) {
	return parseMemoryUpdate(value)
}

func parseMemoryUpdate(value string) (session.MemoryScope, string) {
	scopeText, content, ok := strings.Cut(value, "\n")
	if !ok {
		return session.MemoryScopeSession, strings.TrimSpace(value)
	}
	scope := session.MemoryScope(strings.TrimSpace(scopeText))
	switch scope {
	case session.MemoryScopeSession, session.MemoryScopeProject, session.MemoryScopeGlobal:
		return scope, strings.TrimSpace(content)
	default:
		return session.MemoryScopeSession, strings.TrimSpace(content)
	}
}

type modelSwitchResult struct {
	provider string
	model    string
	err      error
}

type yoloSwitchResult struct {
	enabled bool
	err     error
}

type toggleFavoriteResult struct {
	ref string
	err error
}

// toggleStringSlice adds ref to slice if not present, or removes it if present.
// Returns a new slice; does not modify the original.
func toggleStringSlice(slice []string, ref string) []string {
	out := make([]string, 0, len(slice))
	found := false
	for _, v := range slice {
		if v == ref {
			found = true
			continue
		}
		out = append(out, v)
	}
	if !found {
		out = append(out, ref)
	}
	return out
}

// toggleFavoriteModelCmd calls opts.ToggleFavoriteModel if wired, ignoring
// errors non-fatally (a missing persist is cosmetically silent; the in-memory
// modal state is already updated).
func (a *App) toggleFavoriteModelCmd(providerName, modelName string) tea.Cmd {
	ref := providerName + "/" + modelName
	return func() tea.Msg {
		if a.opts.ToggleFavoriteModel != nil {
			if err := a.opts.ToggleFavoriteModel(a.ctx, providerName, modelName); err != nil {
				return toggleFavoriteResult{ref: ref, err: err}
			}
		}
		return toggleFavoriteResult{ref: ref}
	}
}

func (a *App) switchYoloCmd(value string) tea.Cmd {
	enabled := a.opts.Yolo
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "on":
		enabled = true
	case "off":
		enabled = false
	case "", "toggle":
		enabled = !enabled
	default:
		return a.setNotice(`/yolo: expected "on", "off", or "toggle"`)
	}
	return func() tea.Msg {
		if a.opts.SetYolo != nil {
			if err := a.opts.SetYolo(a.ctx, enabled); err != nil {
				return yoloSwitchResult{enabled: enabled, err: err}
			}
		}
		return yoloSwitchResult{enabled: enabled}
	}
}

func (a *App) switchModelCmd(providerName, modelName string) tea.Cmd {
	return func() tea.Msg {
		if a.opts.SwitchModel != nil {
			if err := a.opts.SwitchModel(a.ctx, providerName, modelName, ""); err != nil {
				return modelSwitchResult{provider: providerName, model: modelName, err: err}
			}
		}
		return modelSwitchResult{provider: providerName, model: modelName}
	}
}

// splitModelRef splits "provider/model" into its two halves.  Empty
// halves or missing separators report not-ok and leave state
// unchanged.
func splitModelRef(ref string) (string, string, bool) {
	idx := strings.Index(ref, "/")
	if idx <= 0 || idx == len(ref)-1 {
		return "", "", false
	}
	return ref[:idx], ref[idx+1:], true
}

func (a *App) knownProvider(providerName string) bool {
	if providerName == a.opts.ModelProvider {
		return true
	}
	if a.opts.Catalog == nil || a.opts.Catalog.Source() == nil {
		return true
	}
	return slices.Contains(a.opts.Catalog.Source().Providers(), providerName)
}

func providerHasAPIKey(cfg *config.Config, providerName, currentProvider string) bool {
	if cfg == nil || providerName != currentProvider {
		return false
	}
	v, ok := cfg.Model.Options["api_key"].(string)
	return ok && strings.TrimSpace(v) != ""
}

// setNotice raises a new ephemeral status line and schedules its
// clearing.  The scheduled clear carries the notice text so a fresher
// one that overlaps the timer is not wiped.
func (a *App) setNotice(text string) tea.Cmd {
	a.notice = text
	if text == "" {
		return nil
	}
	captured := text
	return tea.Tick(noticeLifetime, func(time.Time) tea.Msg {
		return clearNoticeMsg{notice: captured}
	})
}

// paletteMatches returns the current command-palette matches.  When
// the input buffer does not start with "/" or no Commands registry
// is wired up, returns nil.
func (a *App) paletteMatches() []command.Command {
	if a.opts.Commands == nil {
		return nil
	}
	buf := a.input.Value()
	if !strings.HasPrefix(buf, "/") {
		return nil
	}
	// Filter by the head token: characters between the slash and
	// the first space.  This way `/co` filters by "co" and
	// `/model anth` still shows /model as the highlight while
	// the user types args.
	head, _ := splitSlash(buf)
	return a.opts.Commands.LookupPrefix(head)
}

// clampedPaletteHighlight returns the current highlight clamped to
// the bounds of matches, or -1 when matches is empty.
func (a *App) clampedPaletteHighlight(matches []command.Command) int {
	if len(matches) == 0 {
		return -1
	}
	hi := a.paletteHighlight
	if hi < 0 {
		return 0
	}
	if hi >= len(matches) {
		return len(matches) - 1
	}
	return hi
}

// movePaletteHighlight shifts the highlight index, snapping into
// range against the current match set.  delta < 0 selects an earlier
// row; delta > 0 selects a later row.
func (a *App) movePaletteHighlight(delta int) {
	matches := a.paletteMatches()
	if len(matches) == 0 {
		a.paletteHighlight = -1
		return
	}
	hi := max(a.paletteHighlight, 0)
	hi += delta
	if hi < 0 {
		hi = 0
	}
	if hi >= len(matches) {
		hi = len(matches) - 1
	}
	a.paletteHighlight = hi
}

func (a *App) acceptPaletteCompletion() bool {
	if a.opts.Commands == nil || !strings.HasPrefix(a.input.Value(), "/") {
		return false
	}
	matches := a.paletteMatches()
	hi := a.clampedPaletteHighlight(matches)
	if hi < 0 {
		return false
	}
	a.input.Textarea.SetValue("/" + matches[hi].Name() + " ")
	a.input.Textarea.CursorEnd()
	return true
}

func slashPrefixOnly(text string) bool {
	if !strings.HasPrefix(text, "/") {
		return false
	}
	return !strings.ContainsAny(strings.TrimPrefix(text, "/"), " \t\n")
}

// commandAppAdapter is the read-only [command.App] view onto the
// running App.  Commands hold a pointer to the App but see only
// this narrow interface, so they can never mutate state directly.
type commandAppAdapter struct{ a *App }

func (c *commandAppAdapter) SessionID() string             { return c.a.opts.SessionID }
func (c *commandAppAdapter) Model() string                 { return c.a.opts.ModelProvider + "/" + c.a.opts.ModelName }
func (c *commandAppAdapter) Reasoning() provider.Reasoning { return c.a.opts.Reasoning }
func (c *commandAppAdapter) Cost() float64                 { return c.a.costDollars }
func (c *commandAppAdapter) Sessions(ctx context.Context, limit int) ([]*session.Session, error) {
	if c.a.opts.Store == nil {
		return nil, nil
	}
	return c.a.opts.Store.ListSessions(ctx, session.ListOpts{Limit: limit})
}

// Compile-time guard that the adapter satisfies the interface.
var _ command.App = (*commandAppAdapter)(nil)

// applyLayoutOverride processes a /layout UpdateLayout value.  It toggles or
// sets a.layoutOverride without touching a.opts.Config.
func (a *App) applyLayoutOverride(value string) tea.Cmd {
	switch value {
	case "compact":
		a.layoutOverride = "compact"
	case "default":
		a.layoutOverride = "default"
	case "toggle":
		if a.effectiveLayout() == "compact" {
			a.layoutOverride = "default"
		} else {
			a.layoutOverride = "compact"
		}
	}
	a.msgCacheValid = false
	if a.layoutOverride == "" {
		return nil
	}
	return a.showToast("Layout", fmt.Sprintf("Using %s layout", a.layoutOverride))
}

// effectiveLayout returns the active layout name ("default" or "compact"),
// preferring the in-session override over the persisted config value.
func (a *App) effectiveLayout() string {
	if a.layoutOverride != "" {
		return a.layoutOverride
	}
	if a.opts.Config != nil && a.opts.Config.Layout == "compact" {
		return "compact"
	}
	return "default"
}

// slogWarnUnknownModal is a thin helper so the call site stays
// readable; pulls the slog line into one place.
func slogWarnUnknownModal(name string) {
	slog.Warn("ui: slash command requested unknown modal; ignored", "modal", name)
}

// slogWarnUnknownUpdate logs a slash-command Updates entry whose key
// hygge does not yet recognise.
func slogWarnUnknownUpdate(key, value string) {
	slog.Warn("ui: slash command requested unknown update; ignored", "key", key, "value", value)
}

// resolveCompactionMessageCount returns the number of messages since the
// latest compaction marker for the foreground session.  Falls back to 0
// when the store is unavailable or an error occurs.
func (a *App) resolveCompactionMessageCount() int {
	sid := a.foregroundID()
	if sid == "" || a.opts.Store == nil {
		return 0
	}
	msgs, _, err := a.opts.Store.MessagesSinceLatestMarker(a.ctx, sid)
	if err != nil {
		return 0
	}
	return len(msgs)
}

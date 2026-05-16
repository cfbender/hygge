package ui

import (
	"context"
	"log/slog"
	"time"

	tea "charm.land/bubbletea/v2"
)

type sessionTitleGeneratedMsg struct {
	sessionID string
	title     string
	err       error
}

// maybeRefreshSessionTitle starts a best-effort title refresh. The title model
// sees the current slug plus recent conversation and returns KEEP when the
// current title still fits, otherwise a newly formatted title.
func (a *App) maybeRefreshSessionTitle(sessionID string) tea.Cmd {
	if a.opts.Agent == nil || a.opts.Store == nil || sessionID == "" {
		slog.Debug("ui: title refresh skipped (missing deps)",
			"session", sessionID, "agent_nil", a.opts.Agent == nil, "store_nil", a.opts.Store == nil)
		return nil
	}
	if a.titleGeneration == nil {
		a.titleGeneration = make(map[string]bool)
	}
	if a.titleGeneration[sessionID] {
		slog.Debug("ui: title refresh skipped (already in flight)", "session", sessionID)
		return nil
	}
	if _, err := a.opts.Store.GetSession(a.ctx, sessionID); err != nil {
		slog.Debug("ui: title refresh skipped (session lookup failed)", "session", sessionID, "err", err)
		return nil
	}
	a.titleGeneration[sessionID] = true
	slog.Debug("ui: title refresh scheduled", "session", sessionID)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(a.ctx, 15*time.Second)
		defer cancel()

		title, changed, err := a.opts.Agent.RefreshSessionTitle(ctx, sessionID)
		if err != nil {
			slog.Warn("ui: title refresh failed", "session", sessionID, "err", err)
			return sessionTitleGeneratedMsg{sessionID: sessionID, err: err}
		}
		slog.Debug("ui: title refresh completed", "session", sessionID, "title", title, "changed", changed)
		if !changed || title == "" {
			return sessionTitleGeneratedMsg{sessionID: sessionID}
		}
		return sessionTitleGeneratedMsg{sessionID: sessionID, title: title}
	}
}

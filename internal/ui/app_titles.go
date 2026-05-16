package ui

import (
	"context"
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
		return nil
	}
	if a.titleGeneration == nil {
		a.titleGeneration = make(map[string]bool)
	}
	if a.titleGeneration[sessionID] {
		return nil
	}
	if _, err := a.opts.Store.GetSession(a.ctx, sessionID); err != nil {
		return nil
	}
	a.titleGeneration[sessionID] = true
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(a.ctx, 15*time.Second)
		defer cancel()

		title, changed, err := a.opts.Agent.RefreshSessionTitle(ctx, sessionID)
		if err != nil {
			return sessionTitleGeneratedMsg{sessionID: sessionID, err: err}
		}
		if !changed || title == "" {
			return sessionTitleGeneratedMsg{sessionID: sessionID}
		}
		return sessionTitleGeneratedMsg{sessionID: sessionID, title: title}
	}
}

package ui

import (
	"context"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

type sessionTitleGeneratedMsg struct {
	sessionID string
	title     string
	err       error
}

// maybeGenerateSessionTitle starts a best-effort model title generation for a
// session after its first user message. It only runs when the session has no
// explicit slug yet, so user-renamed sessions are never overwritten.
func (a *App) maybeGenerateSessionTitle(sessionID string) tea.Cmd {
	if a.opts.Agent == nil || a.opts.Store == nil || sessionID == "" {
		return nil
	}
	if a.titleGeneration == nil {
		a.titleGeneration = make(map[string]bool)
	}
	if a.titleGeneration[sessionID] {
		return nil
	}
	sess, err := a.opts.Store.GetSession(a.ctx, sessionID)
	if err != nil || strings.TrimSpace(sess.Slug) != "" {
		return nil
	}
	prompt := strings.TrimSpace(sess.FirstMessagePreview)
	if prompt == "" {
		return nil
	}
	a.titleGeneration[sessionID] = true
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(a.ctx, 15*time.Second)
		defer cancel()

		title, err := a.opts.Agent.GenerateTitle(ctx, prompt)
		if err != nil {
			return sessionTitleGeneratedMsg{sessionID: sessionID, err: err}
		}
		title = cleanGeneratedSessionTitle(title)
		if title == "" {
			return sessionTitleGeneratedMsg{sessionID: sessionID}
		}
		latest, err := a.opts.Store.GetSession(ctx, sessionID)
		if err != nil {
			return sessionTitleGeneratedMsg{sessionID: sessionID, err: err}
		}
		if strings.TrimSpace(latest.Slug) != "" {
			return sessionTitleGeneratedMsg{sessionID: sessionID}
		}
		if err := a.opts.Store.RenameSession(ctx, sessionID, title); err != nil {
			return sessionTitleGeneratedMsg{sessionID: sessionID, err: err}
		}
		return sessionTitleGeneratedMsg{sessionID: sessionID, title: title}
	}
}

func cleanGeneratedSessionTitle(title string) string {
	title = strings.TrimSpace(title)
	title = strings.Trim(title, " \t\n\r\"'`“”‘’")
	title = strings.Join(strings.Fields(title), " ")
	if title == "" {
		return ""
	}
	const maxRunes = 80
	runes := []rune(title)
	if len(runes) > maxRunes {
		title = string(runes[:maxRunes-1]) + "…"
	}
	return title
}

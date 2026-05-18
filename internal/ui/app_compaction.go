package ui

import (
	"context"
	"errors"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/cfbender/hygge/internal/agent"
)

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

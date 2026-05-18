package ui

import (
	"log/slog"

	"github.com/cfbender/hygge/internal/notify"
)

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

package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/cfbender/hygge/internal/command"
	"github.com/cfbender/hygge/internal/ui/components"
)

func TestOverlayInputFocusTracksOpenAndClosed(t *testing.T) {
	t.Parallel()
	app, _, _ := newSlashApp(t)

	if !app.input.Focused {
		t.Fatal("input should start focused")
	}
	app.openOverlay(overlayHelp)
	app.updateInputFocus()
	if app.input.Focused {
		t.Fatal("input should blur while overlay is open")
	}
	app.closeOverlay(overlayHelp)
	if !app.input.Focused {
		t.Fatal("input should refocus after overlay closes")
	}

	app.pendingPerms = append(app.pendingPerms, components.PermissionRequest{RequestID: "req-1", ToolName: "tool"})
	app.syncPermissionOverlay()
	app.updateInputFocus()
	if app.input.Focused {
		t.Fatal("input should blur while permission is pending")
	}
	app.pendingPerms = nil
	app.syncPermissionOverlay()
	app.updateInputFocus()
	if !app.input.Focused {
		t.Fatal("input should refocus after permission queue drains")
	}
}

func TestOverlayTopmostRoutesKeysFirst(t *testing.T) {
	t.Parallel()
	app, _, _ := newSlashApp(t)
	app.openOverlay(overlaySessions)
	app.openOverlay(overlayHelp)

	app.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	if app.activeModal != command.ModalSessions {
		t.Fatalf("activeModal = %q, want %q", app.activeModal, command.ModalSessions)
	}
	if !app.overlays.Has(overlaySessions) {
		t.Fatal("sessions overlay should remain after top help overlay closes")
	}
}

func TestHelpOverlayOpensRendersAndCloses(t *testing.T) {
	t.Parallel()
	app, _, _ := newSlashApp(t)
	typeInto(app, "/help")
	app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	if app.activeModal != command.ModalHelp {
		t.Fatalf("activeModal = %q, want %q", app.activeModal, command.ModalHelp)
	}
	view := app.View().Content
	if !strings.Contains(view, "Common commands") {
		t.Fatalf("help overlay did not render expected body:\n%s", view)
	}
	app.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if app.activeModal != "" {
		t.Fatalf("help overlay still open after Esc: %q", app.activeModal)
	}
}

func TestPermissionOverlayRoutesBeforeLowerOverlay(t *testing.T) {
	t.Parallel()
	app, _, _ := newSlashApp(t)
	app.openOverlay(overlaySessions)
	app.pendingPerms = append(app.pendingPerms, components.PermissionRequest{RequestID: "req-1", ToolName: "tool"})
	app.syncPermissionOverlay()
	app.updateInputFocus()

	app.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	if len(app.pendingPerms) != 0 {
		t.Fatalf("pendingPerms len = %d, want 0", len(app.pendingPerms))
	}
	if app.activeModal != command.ModalSessions {
		t.Fatalf("activeModal = %q, want lower sessions overlay preserved", app.activeModal)
	}
}

func TestMessageActionOverlayMirrorsActiveModal(t *testing.T) {
	t.Parallel()
	app, _, _ := newSlashApp(t)

	app.openOverlay(overlayMessageAction)
	if app.activeModal != string(overlayMessageAction) {
		t.Fatalf("activeModal = %q, want %q", app.activeModal, overlayMessageAction)
	}

	app.closeOverlay(overlayMessageAction)
	if app.activeModal != "" {
		t.Fatalf("activeModal after close = %q, want empty", app.activeModal)
	}
}

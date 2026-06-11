package ui

import (
	"slices"

	tea "charm.land/bubbletea/v2"

	"github.com/cfbender/hygge/internal/command"
)

// overlay is a self-contained modal: it owns its state, handles its own
// input, and renders itself. Update returns done=true when the overlay
// should close; the App pops it from the stack and restores input focus.
// Migrated modals implement this interface and are routed generically by
// key handling and rendering; unmigrated modals still go through the
// per-kind switches in app_keys.go and render.go.
type overlay interface {
	Update(msg tea.Msg) (cmd tea.Cmd, done bool)
	View(w, h int) string
}

// overlayFor returns the overlay implementation for kind, or nil when that
// modal has not been migrated to the overlay interface yet. As modals
// migrate, they gain a case here and lose their branches in the key/render
// switches.
func (a *App) overlayFor(kind overlayKind) overlay {
	switch kind {
	case overlayQuit:
		return a.quitConfirm
	default:
		return nil
	}
}

type overlayKind string

const (
	overlayHelp           overlayKind = command.ModalHelp
	overlaySessions       overlayKind = command.ModalSessions
	overlayMemory         overlayKind = command.ModalMemory
	overlayMemoryRemember overlayKind = command.ModalRememberMemory
	overlayMemoryForget   overlayKind = command.ModalForgetMemory
	overlayCompactConfirm overlayKind = command.ModalCompactConfirm
	overlayModel          overlayKind = command.ModalModel
	overlayAPIKey         overlayKind = command.ModalAPIKey
	overlayTheme          overlayKind = command.ModalTheme
	overlayOnboarding     overlayKind = "onboarding"
	overlayPermission     overlayKind = "permission"
	overlayQuestion       overlayKind = "question"
	overlayQuit           overlayKind = "quit"
	overlayMessageAction  overlayKind = "message_action"
)

type overlayStack struct {
	entries []overlayKind
}

func (s *overlayStack) Push(kind overlayKind) {
	s.Remove(kind)
	s.entries = append(s.entries, kind)
}

func (s *overlayStack) Pop() (overlayKind, bool) {
	if len(s.entries) == 0 {
		return "", false
	}
	idx := len(s.entries) - 1
	kind := s.entries[idx]
	s.entries = s.entries[:idx]
	return kind, true
}

func (s *overlayStack) Remove(kind overlayKind) bool {
	for i := len(s.entries) - 1; i >= 0; i-- {
		if s.entries[i] == kind {
			s.entries = append(s.entries[:i], s.entries[i+1:]...)
			return true
		}
	}
	return false
}

func (s overlayStack) Top() (overlayKind, bool) {
	if len(s.entries) == 0 {
		return "", false
	}
	return s.entries[len(s.entries)-1], true
}

func (s overlayStack) Has(kind overlayKind) bool {
	return slices.Contains(s.entries, kind)
}

func (s overlayStack) Open() bool { return len(s.entries) > 0 }

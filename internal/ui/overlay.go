package ui

import (
	"slices"

	"github.com/cfbender/hygge/internal/command"
)

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

package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

const maxHistoryEntries = 50

// xdgStateHome returns the XDG state directory.
func xdgStateHome(homeDir string) string {
	if v := os.Getenv("XDG_STATE_HOME"); v != "" {
		return v
	}
	if homeDir == "" {
		homeDir, _ = os.UserHomeDir()
	}
	return filepath.Join(homeDir, ".local", "state")
}

// inputHistory manages a persistent list of previously sent inputs.
// Thread-safe for concurrent load/save but UI access is single-goroutine.
type inputHistory struct {
	entries []string // oldest first
	cursor  int      // -1 = not browsing; 0..len-1 = current position
	draft   string   // saved draft when user starts browsing
	path    string   // file path for persistence

	mu sync.Mutex
}

func newInputHistory(stateDir string) *inputHistory {
	h := &inputHistory{
		cursor: -1,
		path:   filepath.Join(stateDir, "hygge", "input_history.json"),
	}
	h.load()
	return h
}

// Add appends an entry and persists. Deduplicates against the last entry.
func (h *inputHistory) Add(text string) {
	if text == "" {
		return
	}
	// Deduplicate against the most recent entry.
	if len(h.entries) > 0 && h.entries[len(h.entries)-1] == text {
		h.Reset()
		return
	}
	h.entries = append(h.entries, text)
	if len(h.entries) > maxHistoryEntries {
		h.entries = h.entries[len(h.entries)-maxHistoryEntries:]
	}
	h.Reset()
	h.save()
}

// Up moves to the previous history entry. Returns the entry text and true
// if there is one, or empty string and false if already at the oldest.
// On first call, saves currentInput as the draft.
func (h *inputHistory) Up(currentInput string) (string, bool) {
	if len(h.entries) == 0 {
		return "", false
	}
	if h.cursor == -1 {
		// Start browsing from the end.
		h.draft = currentInput
		h.cursor = len(h.entries) - 1
		return h.entries[h.cursor], true
	}
	if h.cursor > 0 {
		h.cursor--
		return h.entries[h.cursor], true
	}
	// Already at oldest.
	return h.entries[h.cursor], false
}

// Down moves to the next history entry, or restores the draft.
func (h *inputHistory) Down() (string, bool) {
	if h.cursor == -1 {
		return "", false
	}
	if h.cursor < len(h.entries)-1 {
		h.cursor++
		return h.entries[h.cursor], true
	}
	// Past the newest entry — restore draft.
	text := h.draft
	h.Reset()
	return text, true
}

// Browsing reports whether the user is currently cycling through history.
func (h *inputHistory) Browsing() bool {
	return h.cursor != -1
}

// Reset exits history browsing mode.
func (h *inputHistory) Reset() {
	h.cursor = -1
	h.draft = ""
}

func (h *inputHistory) load() {
	data, err := os.ReadFile(h.path)
	if err != nil {
		return
	}
	var entries []string
	if err := json.Unmarshal(data, &entries); err != nil {
		return
	}
	if len(entries) > maxHistoryEntries {
		entries = entries[len(entries)-maxHistoryEntries:]
	}
	h.entries = entries
}

func (h *inputHistory) save() {
	h.mu.Lock()
	defer h.mu.Unlock()

	dir := filepath.Dir(h.path)
	_ = os.MkdirAll(dir, 0o700)

	data, err := json.Marshal(h.entries)
	if err != nil {
		return
	}
	tmp := h.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, h.path)
}

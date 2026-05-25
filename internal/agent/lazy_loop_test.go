package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cfbender/hygge/internal/agentsmd"
	"github.com/cfbender/hygge/internal/bus"
	"github.com/cfbender/hygge/internal/provider"
)

// TestSend_LazyContextInjectedNextTurn verifies the end-to-end loop
// behaviour: when a tool_use call touches a path inside a subdirectory
// containing an AGENTS.md, that file's contents are injected into the
// system prompt of the NEXT provider turn — and only that turn.
func TestSend_LazyContextInjectedNextTurn(t *testing.T) {
	env := newTestEnv(t)

	// Create a subdir under pwd that has its own AGENTS.md and a
	// file the read tool can succeed on.
	subdir := filepath.Join(env.pwd, "pkg")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	subAgents := filepath.Join(subdir, "AGENTS.md")
	if err := os.WriteFile(subAgents, []byte("pkg-only rules"), 0o600); err != nil {
		t.Fatalf("write agents: %v", err)
	}
	target := filepath.Join(subdir, "code.go")
	if err := os.WriteFile(target, []byte("package pkg"), 0o600); err != nil {
		t.Fatalf("write code: %v", err)
	}
	// Mark the pwd as a project root so the tracker accepts it.
	if err := os.WriteFile(filepath.Join(env.pwd, ".git"), nil, 0o600); err != nil {
		t.Fatalf("write .git marker: %v", err)
	}

	tracker := agentsmd.NewLazyTracker("", env.pwd, nil)

	// Scripted provider: turn 1 emits a read tool_use; turn 2 emits
	// final text.  We capture the system prompt of every Stream call
	// so we can assert turn 2 carries the lazy block.
	var (
		mu      sync.Mutex
		systems []string
	)
	prov := newFakeProvider("fake",
		scriptToolUse("", toolUseEvent(t, "tu1", "read", map[string]any{"path": target})),
		scriptText("done", provider.Usage{InputTokens: 1, OutputTokens: 1}),
	)
	prov.onStream = func(req provider.Request) {
		mu.Lock()
		systems = append(systems, req.System)
		mu.Unlock()
	}

	a := env.newAgent(prov, func(o *Options) {
		o.SystemPrompt = "base prompt"
		o.LazyContext = tracker
	})

	if _, err := a.Send(context.Background(), env.sessionID, userText("read pkg/code.go")); err != nil {
		t.Fatalf("Send: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(systems) != 2 {
		t.Fatalf("want 2 provider streams, got %d", len(systems))
	}
	// First turn: just the base prompt, no lazy injection yet.
	if strings.Contains(systems[0], "Additional project context") {
		t.Fatalf("first-turn prompt should not carry lazy context, got %q", systems[0])
	}
	// Second turn: lazy block must be present, headered correctly,
	// and the content must NOT have been persisted to history.
	if !strings.Contains(systems[1], "## Additional project context (loaded for this turn)") {
		t.Fatalf("second-turn prompt missing lazy header, got %q", systems[1])
	}
	if !strings.Contains(systems[1], "pkg-only rules") {
		t.Fatalf("second-turn prompt missing lazy content, got %q", systems[1])
	}

	// Session history must not include the lazy content anywhere —
	// it rides ONLY in the system prompt, never as a message.
	for _, m := range readMessages(t, env.Store, env.sessionID) {
		for _, p := range m.Parts {
			if strings.Contains(p.Text, "pkg-only rules") ||
				strings.Contains(p.Content, "pkg-only rules") {
				t.Fatalf("lazy content leaked into history: %+v", p)
			}
		}
	}
}

// TestSend_LazyContextNotReinjected verifies that once a directory's
// AGENTS.md has ridden along in a turn, subsequent tool calls touching
// the same directory do not re-inject it.
func TestSend_LazyContextNotReinjected(t *testing.T) {
	env := newTestEnv(t)

	subdir := filepath.Join(env.pwd, "pkg")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(subdir, "AGENTS.md"), []byte("pkg rules"), 0o600); err != nil {
		t.Fatalf("write agents: %v", err)
	}
	target := filepath.Join(subdir, "code.go")
	if err := os.WriteFile(target, []byte("x"), 0o600); err != nil {
		t.Fatalf("write code: %v", err)
	}
	if err := os.WriteFile(filepath.Join(env.pwd, ".git"), nil, 0o600); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	tracker := agentsmd.NewLazyTracker("", env.pwd, nil)

	var (
		mu      sync.Mutex
		systems []string
	)
	// Three turns: two tool calls then a final text.
	prov := newFakeProvider("fake",
		scriptToolUse("", toolUseEvent(t, "tu1", "read", map[string]any{"path": target})),
		scriptToolUse("", toolUseEvent(t, "tu2", "read", map[string]any{"path": target})),
		scriptText("done", provider.Usage{InputTokens: 1, OutputTokens: 1}),
	)
	prov.onStream = func(req provider.Request) {
		mu.Lock()
		systems = append(systems, req.System)
		mu.Unlock()
	}

	a := env.newAgent(prov, func(o *Options) {
		o.SystemPrompt = "base"
		o.LazyContext = tracker
	})
	if _, err := a.Send(context.Background(), env.sessionID, userText("read it twice")); err != nil {
		t.Fatalf("Send: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(systems) != 3 {
		t.Fatalf("want 3 streams, got %d", len(systems))
	}
	// Turn 2 carries the lazy block; turn 3 must not re-inject it.
	if !strings.Contains(systems[1], "pkg rules") {
		t.Fatalf("turn-2 prompt missing lazy content")
	}
	if strings.Contains(systems[2], "## Additional project context (loaded for this turn)") {
		t.Fatalf("turn-3 prompt should not re-inject lazy header, got %q", systems[2])
	}
}

// TestSend_LazyContextLoadedEventEmitted verifies that a LazyContextLoaded bus
// event is published when a tool call touches a subdirectory that contains an
// AGENTS.md the tracker has not yet seen.  The event must carry the session ID
// and at least the relative path of the discovered file.
func TestSend_LazyContextLoadedEventEmitted(t *testing.T) {
	env := newTestEnv(t)

	subdir := filepath.Join(env.pwd, "lib")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	agentsFile := filepath.Join(subdir, "AGENTS.md")
	if err := os.WriteFile(agentsFile, []byte("lib rules"), 0o600); err != nil {
		t.Fatalf("write agents: %v", err)
	}
	target := filepath.Join(subdir, "main.go")
	if err := os.WriteFile(target, []byte("package lib"), 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}
	if err := os.WriteFile(filepath.Join(env.pwd, ".git"), nil, 0o600); err != nil {
		t.Fatalf("write .git marker: %v", err)
	}

	tracker := agentsmd.NewLazyTracker("", env.pwd, nil)

	prov := newFakeProvider("fake",
		scriptToolUse("", toolUseEvent(t, "tu1", "read", map[string]any{"path": target})),
		scriptText("done", provider.Usage{InputTokens: 1, OutputTokens: 1}),
	)

	a := env.newAgent(prov, func(o *Options) {
		o.LazyContext = tracker
	})

	events, drain := collectEvents[bus.LazyContextLoaded](t, env.Bus, 8)
	_ = events

	if _, err := a.Send(context.Background(), env.sessionID, userText("read lib/main.go")); err != nil {
		t.Fatalf("Send: %v", err)
	}

	got := waitForLazyContextLoadedEvents(t, events, 1)
	got = append(got, drain()...)

	if len(got) == 0 {
		t.Fatal("want at least one LazyContextLoaded event, got none")
	}
	ev := got[0]
	if ev.SessionID != env.sessionID {
		t.Fatalf("event.SessionID = %q, want %q", ev.SessionID, env.sessionID)
	}
	if len(ev.Files) == 0 {
		t.Fatal("event.Files is empty; expected at least one file path")
	}
	// The relative path should identify the file without revealing full contents.
	found := false
	for _, f := range ev.Files {
		if strings.Contains(f, "AGENTS.md") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("event.Files = %v; expected an entry containing AGENTS.md", ev.Files)
	}
}

// TestSend_LazyContextLoadedEventNotEmittedWhenNoNewContext verifies that
// no LazyContextLoaded event is published when a tool call touches a path
// whose containing directory was already seen by the tracker (i.e. the
// second tool call to the same directory).
func TestSend_LazyContextLoadedEventNotEmittedWhenNoNewContext(t *testing.T) {
	env := newTestEnv(t)

	subdir := filepath.Join(env.pwd, "lib")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(subdir, "AGENTS.md"), []byte("lib rules"), 0o600); err != nil {
		t.Fatalf("write agents: %v", err)
	}
	target := filepath.Join(subdir, "main.go")
	if err := os.WriteFile(target, []byte("package lib"), 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}
	if err := os.WriteFile(filepath.Join(env.pwd, ".git"), nil, 0o600); err != nil {
		t.Fatalf("write .git marker: %v", err)
	}

	tracker := agentsmd.NewLazyTracker("", env.pwd, nil)

	// Three turns: two tool calls to the same path, then final text.
	// Only the first tool call should fire a LazyContextLoaded event.
	prov := newFakeProvider("fake",
		scriptToolUse("", toolUseEvent(t, "tu1", "read", map[string]any{"path": target})),
		scriptToolUse("", toolUseEvent(t, "tu2", "read", map[string]any{"path": target})),
		scriptText("done", provider.Usage{InputTokens: 1, OutputTokens: 1}),
	)

	a := env.newAgent(prov, func(o *Options) {
		o.LazyContext = tracker
	})

	events, drain := collectEvents[bus.LazyContextLoaded](t, env.Bus, 8)
	_ = events

	if _, err := a.Send(context.Background(), env.sessionID, userText("read twice")); err != nil {
		t.Fatalf("Send: %v", err)
	}

	got := waitForLazyContextLoadedEvents(t, events, 1)
	got = append(got, drain()...)

	// Exactly one event: from the first tool call.  The second call touches
	// an already-seen directory so no new blocks are returned by Touch.
	if len(got) != 1 {
		t.Fatalf("want exactly 1 LazyContextLoaded event, got %d", len(got))
	}
}

func waitForLazyContextLoadedEvents(t *testing.T, events <-chan bus.LazyContextLoaded, want int) []bus.LazyContextLoaded {
	t.Helper()
	deadline := time.After(2 * time.Second)
	got := make([]bus.LazyContextLoaded, 0, want)
	for len(got) < want {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatalf("events channel closed while waiting for %d LazyContextLoaded events; got %d", want, len(got))
			}
			got = append(got, ev)
		case <-deadline:
			t.Fatalf("timed out waiting for %d LazyContextLoaded events; got %d", want, len(got))
		}
	}
	return got
}

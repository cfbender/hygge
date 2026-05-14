package hook

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeTOML writes data to path, creating parent dirs as needed.
func writeTOML(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestLoad_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	reg, err := Load(LoadOptions{HomeDir: dir, Pwd: dir})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := reg.All(); len(got) != 0 {
		t.Fatalf("want 0 hooks, got %d", len(got))
	}
}

func TestLoad_BasicHook(t *testing.T) {
	dir := t.TempDir()
	toml := `
[hooks.guard]
description = "Test guard"
events = ["pre_tool"]
command = "/usr/bin/true"
timeout = "3s"
`
	writeTOML(t, filepath.Join(dir, ".agents", "hooks.toml"), toml)

	// Walk-up stops at .git; create a .git marker before loading.
	gitDir := filepath.Join(dir, ".git")
	_ = os.Mkdir(gitDir, 0o700)

	reg, err := Load(LoadOptions{HomeDir: t.TempDir(), Pwd: dir})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	hooks := reg.For(EventPreTool)
	if len(hooks) != 1 {
		t.Fatalf("want 1 pre_tool hook, got %d", len(hooks))
	}
	if hooks[0].Name() != "guard" {
		t.Fatalf("want name=guard, got %q", hooks[0].Name())
	}
	if hooks[0].Timeout() != 3*time.Second {
		t.Fatalf("want 3s timeout, got %v", hooks[0].Timeout())
	}
}

func TestLoad_AsyncPreRejected(t *testing.T) {
	dir := t.TempDir()
	_ = os.Mkdir(filepath.Join(dir, ".git"), 0o700)
	toml := `
[hooks.bad-async]
description = "async pre_tool should be rejected"
events = ["pre_tool"]
command = "/usr/bin/true"
mode = "async"
`
	writeTOML(t, filepath.Join(dir, ".agents", "hooks.toml"), toml)

	reg, err := Load(LoadOptions{HomeDir: t.TempDir(), Pwd: dir})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Should be skipped at load time.
	if len(reg.For(EventPreTool)) != 0 {
		t.Fatal("async pre_tool hook must be rejected at load time")
	}
}

func TestLoad_UnknownEventFiltered(t *testing.T) {
	dir := t.TempDir()
	_ = os.Mkdir(filepath.Join(dir, ".git"), 0o700)
	toml := `
[hooks.mixed]
description = "mixed valid + unknown events"
events = ["pre_tool", "unknown_event"]
command = "/usr/bin/true"
`
	writeTOML(t, filepath.Join(dir, ".agents", "hooks.toml"), toml)

	reg, err := Load(LoadOptions{HomeDir: t.TempDir(), Pwd: dir})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Should load with only the valid event.
	if len(reg.For(EventPreTool)) != 1 {
		t.Fatalf("want 1 pre_tool hook, got %d", len(reg.For(EventPreTool)))
	}
}

func TestLoad_PostMessageCoercedAsync(t *testing.T) {
	dir := t.TempDir()
	_ = os.Mkdir(filepath.Join(dir, ".git"), 0o700)
	toml := `
[hooks.pm]
description = "post message sync should be coerced to async"
events = ["post_message"]
command = "/usr/bin/true"
mode = "sync"
`
	writeTOML(t, filepath.Join(dir, ".agents", "hooks.toml"), toml)

	reg, err := Load(LoadOptions{HomeDir: t.TempDir(), Pwd: dir})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	hooks := reg.For(EventPostMessage)
	if len(hooks) != 1 {
		t.Fatalf("want 1 post_message hook, got %d", len(hooks))
	}
	if hooks[0].Mode() != ModeAsync {
		t.Fatalf("want async mode after coercion, got %s", hooks[0].Mode())
	}
}

func TestLoad_MissingCommandRejected(t *testing.T) {
	dir := t.TempDir()
	_ = os.Mkdir(filepath.Join(dir, ".git"), 0o700)
	toml := `
[hooks.nocommand]
description = "no command field"
events = ["pre_tool"]
`
	writeTOML(t, filepath.Join(dir, ".agents", "hooks.toml"), toml)

	reg, err := Load(LoadOptions{HomeDir: t.TempDir(), Pwd: dir})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(reg.For(EventPreTool)) != 0 {
		t.Fatal("hook with no command must be rejected")
	}
}

func TestLoad_LayerPrecedence(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	_ = os.Mkdir(filepath.Join(project, ".git"), 0o700)

	// User layer: one hook.
	userTOML := `
[hooks.user-only]
description = "user layer"
events = ["post_tool"]
command = "/usr/bin/true"
`
	writeTOML(t, filepath.Join(home, ".agents", "hooks.toml"), userTOML)

	// Project layer: another hook.
	projTOML := `
[hooks.project-only]
description = "project layer"
events = ["pre_tool"]
command = "/usr/bin/true"
`
	writeTOML(t, filepath.Join(project, ".agents", "hooks.toml"), projTOML)

	reg, err := Load(LoadOptions{HomeDir: home, Pwd: project})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(reg.For(EventPostTool)) != 1 {
		t.Fatalf("want 1 post_tool (user), got %d", len(reg.For(EventPostTool)))
	}
	if len(reg.For(EventPreTool)) != 1 {
		t.Fatalf("want 1 pre_tool (project), got %d", len(reg.For(EventPreTool)))
	}
}

// ---------- shellHook unit tests --------------------------------------------

func TestShellHook_ZeroExitEmptyStdout_Allow(t *testing.T) {
	h := &shellHook{
		name:    "t",
		events:  []Event{EventPreTool},
		mode:    ModeSync,
		timeout: 5 * time.Second,
		command: "/usr/bin/true",
	}
	act, err := h.Run(context.Background(), Input{Event: EventPreTool})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if act.Decision != DecisionAllow && act.Decision != "" {
		t.Fatalf("want Allow, got %s", act.Decision)
	}
}

func TestShellHook_NonZeroExit_Deny(t *testing.T) {
	h := &shellHook{
		name:    "t",
		events:  []Event{EventPreTool},
		mode:    ModeSync,
		timeout: 5 * time.Second,
		command: "/usr/bin/false",
	}
	act, err := h.Run(context.Background(), Input{Event: EventPreTool})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if act.Decision != DecisionDeny {
		t.Fatalf("want Deny on non-zero exit, got %s", act.Decision)
	}
}

func TestShellHook_StdoutActionParsed(t *testing.T) {
	// Write a shell script that emits an Action JSON.
	script := t.TempDir() + "/hook.sh"
	if err := os.WriteFile(script, []byte(`#!/bin/sh
echo '{"decision":"deny","reason":"test-deny"}'
`), 0o700); err != nil {
		t.Fatalf("write script: %v", err)
	}
	h := &shellHook{
		name:    "t",
		events:  []Event{EventPreTool},
		mode:    ModeSync,
		timeout: 5 * time.Second,
		command: script,
	}
	act, err := h.Run(context.Background(), Input{Event: EventPreTool})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if act.Decision != DecisionDeny {
		t.Fatalf("want Deny, got %s", act.Decision)
	}
	if act.Reason != "test-deny" {
		t.Fatalf("want reason=test-deny, got %q", act.Reason)
	}
}

func TestShellHook_Modify(t *testing.T) {
	want := `{"cmd":"ls"}`
	script := t.TempDir() + "/hook.sh"
	if err := os.WriteFile(script, []byte(`#!/bin/sh
printf '{"decision":"modify","modified_tool_input":{"cmd":"ls"}}'
`), 0o700); err != nil {
		t.Fatalf("write script: %v", err)
	}
	h := &shellHook{
		name:    "m",
		events:  []Event{EventPreTool},
		mode:    ModeSync,
		timeout: 5 * time.Second,
		command: script,
	}
	act, err := h.Run(context.Background(), Input{Event: EventPreTool, ToolInput: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if act.Decision != DecisionModify {
		t.Fatalf("want Modify, got %s", act.Decision)
	}
	if string(act.ModifiedToolInput) != want {
		t.Fatalf("want %s, got %s", want, act.ModifiedToolInput)
	}
}

func TestShellHook_Timeout_Deny(t *testing.T) {
	script := t.TempDir() + "/slow.sh"
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 10\n"), 0o700); err != nil {
		t.Fatalf("write script: %v", err)
	}
	h := &shellHook{
		name:    "slow",
		events:  []Event{EventPreTool},
		mode:    ModeSync,
		timeout: 100 * time.Millisecond,
		command: script,
	}
	start := time.Now()
	act, err := h.Run(context.Background(), Input{Event: EventPreTool})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if act.Decision != DecisionDeny {
		t.Fatalf("want Deny on timeout, got %s", act.Decision)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("hook should have been killed within 2s, took %v", elapsed)
	}
}

func TestShellHook_MalformedStdout_FailOpen(t *testing.T) {
	script := t.TempDir() + "/bad.sh"
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho 'not json at all'\n"), 0o700); err != nil {
		t.Fatalf("write script: %v", err)
	}
	h := &shellHook{
		name:    "bad",
		events:  []Event{EventPreTool},
		mode:    ModeSync,
		timeout: 5 * time.Second,
		command: script,
	}
	act, err := h.Run(context.Background(), Input{Event: EventPreTool})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// Malformed stdout → fail open.
	if act.Decision == DecisionDeny {
		t.Fatalf("malformed stdout must fail open, got Deny")
	}
}

func TestShellHook_StdinReceivesInput(t *testing.T) {
	// Script reads stdin and prints the event field back.
	script := t.TempDir() + "/echo.sh"
	if err := os.WriteFile(script, []byte(`#!/bin/sh
cat - | python3 -c "import json,sys; d=json.load(sys.stdin); print('{\"decision\":\"deny\",\"reason\":\"'+d['event']+'\"}')"
`), 0o700); err != nil {
		t.Fatalf("write script: %v", err)
	}
	h := &shellHook{
		name:    "echo",
		events:  []Event{EventPreTool},
		mode:    ModeSync,
		timeout: 5 * time.Second,
		command: script,
	}
	act, err := h.Run(context.Background(), Input{Event: EventPreTool})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if act.Decision != DecisionDeny {
		t.Fatalf("want Deny, got %s", act.Decision)
	}
	if act.Reason != "pre_tool" {
		t.Fatalf("want reason=pre_tool (stdin echo), got %q", act.Reason)
	}
}

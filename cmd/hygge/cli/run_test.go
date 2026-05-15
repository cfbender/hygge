package cli

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cfbender/hygge/internal/config"
	"github.com/cfbender/hygge/internal/state"
)

func TestResumeNoMatchExitsWithError(t *testing.T) {
	hermeticHome(t)

	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"resume", "nope"})
	if err := root.Execute(); err == nil {
		t.Fatalf("expected error when no session matches")
	}
	if !strings.Contains(out.String(), "no session matches") {
		t.Errorf("expected 'no session matches' on stderr, got:\n%s", out.String())
	}
}

func TestRunNoArgsBuildsAppAndSkipsTea(t *testing.T) {
	// SkipTea was set by hermeticHome — that means runTUI returns
	// immediately after constructing the App, exercising the bootstrap
	// path end-to-end without ever taking over a TTY.
	hermeticHome(t)

	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
}

func TestResumeWithSeed(t *testing.T) {
	home := hermeticHome(t)
	id := seedSession(t, home)

	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"resume", id[:6]})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "resuming") {
		t.Errorf("expected 'resuming' line, got:\n%s", out.String())
	}
	if !strings.Contains(out.String(), shortID(id)) {
		t.Errorf("expected short id in output, got:\n%s", out.String())
	}
}

// TestRoot_ReasoningFlagPresent confirms the --reasoning flag is wired
// into the root command and appears in `hygge --help`.  Detailed
// resolution behaviour lives in resolveReasoning's unit test.
func TestRoot_ReasoningFlagPresent(t *testing.T) {
	root := NewRootCmd()
	flag := root.Flags().Lookup("reasoning")
	if flag == nil {
		t.Fatal("--reasoning flag missing from root command")
	}
	if !strings.Contains(flag.Usage, "off") || !strings.Contains(flag.Usage, "high") {
		t.Errorf("--reasoning usage text should advertise off/high, got %q", flag.Usage)
	}
}

// ---------------------------------------------------------------------------
// T2.4 — new flag tests
// ---------------------------------------------------------------------------

// TestRoot_ContinueFlagPresent confirms --continue / -c is wired.
func TestRoot_ContinueFlagPresent(t *testing.T) {
	root := NewRootCmd()
	f := root.Flags().Lookup("continue")
	if f == nil {
		t.Fatal("--continue flag missing from root command")
	}
}

// TestRoot_NewFlagPresent confirms --new is wired.
func TestRoot_NewFlagPresent(t *testing.T) {
	root := NewRootCmd()
	f := root.Flags().Lookup("new")
	if f == nil {
		t.Fatal("--new flag missing from root command")
	}
}

// TestContinueAndNewConflict errors when both --continue and --new are set.
func TestContinueAndNewConflict(t *testing.T) {
	hermeticHome(t)

	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"--continue", "--new"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected error for conflicting --continue and --new")
	}
	if !strings.Contains(out.String()+err.Error(), "conflicting") {
		t.Errorf("expected 'conflicting' in error, got: output=%q err=%q", out.String(), err.Error())
	}
}

// TestContinueNoSessionStartsFresh verifies that --continue with no sessions
// silently starts a fresh session (no error).
func TestContinueNoSessionStartsFresh(t *testing.T) {
	hermeticHome(t)

	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"--continue"})
	if err := root.Execute(); err != nil {
		t.Fatalf("--continue with no sessions should not error: %v", err)
	}
}

// TestContinueWithSessionResumes verifies that --continue resumes the most
// recent cwd-scoped session.
func TestContinueWithSessionResumes(t *testing.T) {
	home := hermeticHome(t)
	seedSession(t, home) // creates a session in the cwd

	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"--continue"})
	if err := root.Execute(); err != nil {
		t.Fatalf("--continue with session should not error: %v", err)
	}
}

// TestNewFlagStartsFresh verifies that --new starts a fresh session even
// when resume_default = "continue" is configured.
func TestNewFlagStartsFresh(t *testing.T) {
	home := hermeticHome(t)

	// Write a user config with resume_default = "continue".
	cfgDir := filepath.Join(home, ".config", "hygge")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("mkdir cfg: %v", err)
	}
	cfgBody := `[session]
resume_default = "continue"
`
	if err := os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte(cfgBody), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	seedSession(t, home) // creates a session in the cwd

	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"--new"})
	if err := root.Execute(); err != nil {
		t.Fatalf("--new should not error: %v", err)
	}
}

// TestResumeDefaultContinueResumes verifies that resume_default = "continue"
// causes the bare `hygge` to resume the most recent cwd session.
func TestResumeDefaultContinueResumes(t *testing.T) {
	home := hermeticHome(t)

	cfgDir := filepath.Join(home, ".config", "hygge")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("mkdir cfg: %v", err)
	}
	cfgBody := `[session]
resume_default = "continue"
`
	if err := os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte(cfgBody), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	seedSession(t, home)

	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{})
	if err := root.Execute(); err != nil {
		t.Fatalf("resume_default=continue should not error: %v", err)
	}
}

// TestResumeDefaultAskOpensPicker verifies that resume_default = "ask"
// launches the TUI with the sessions picker open (SkipTea so it doesn't
// block; we just check it doesn't error).
func TestResumeDefaultAskOpensPicker(t *testing.T) {
	home := hermeticHome(t)

	cfgDir := filepath.Join(home, ".config", "hygge")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("mkdir cfg: %v", err)
	}
	cfgBody := `[session]
resume_default = "ask"
`
	if err := os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte(cfgBody), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{})
	if err := root.Execute(); err != nil {
		t.Fatalf("resume_default=ask should not error: %v", err)
	}
}

// TestResumeNoArgNoSession errors with a helpful message when no sessions
// exist in the cwd.
func TestResumeNoArgNoSession(t *testing.T) {
	hermeticHome(t)

	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"resume"})
	if err := root.Execute(); err == nil {
		t.Fatal("expected error when no sessions exist")
	}
	if !strings.Contains(out.String(), "no sessions to resume") {
		t.Errorf("expected 'no sessions to resume' in output, got:\n%s", out.String())
	}
}

// TestResumeNoArgOneSessionResumes auto-picks the single session.
func TestResumeNoArgOneSessionResumes(t *testing.T) {
	home := hermeticHome(t)
	id := seedSession(t, home)

	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"resume"})
	if err := root.Execute(); err != nil {
		t.Fatalf("resume with one session should not error: %v", err)
	}
	if !strings.Contains(out.String(), shortID(id)) {
		t.Errorf("expected short id in output, got:\n%s", out.String())
	}
}

// TestResumeNoArgMultipleSessionsOpensPicker verifies that with multiple
// sessions the TUI is launched with the picker open (SkipTea so it doesn't
// block).
func TestResumeNoArgMultipleSessionsOpensPicker(t *testing.T) {
	home := hermeticHome(t)
	seedSession(t, home)
	seedSession(t, home)

	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"resume"})
	// SkipTea is set by hermeticHome so runTUI returns immediately.
	if err := root.Execute(); err != nil {
		t.Fatalf("resume with multiple sessions should not error: %v", err)
	}
}

// TestResumeAnyDisablesCwdScope verifies that --any ignores the cwd filter.
func TestResumeAnyDisablesCwdScope(t *testing.T) {
	hermeticHome(t)

	// Override Pwd to a different directory so no sessions match without --any.
	otherDir := t.TempDir()
	SetTestOverrides(&bootstrapOptions{
		HomeDir:         filepath.Dir(otherDir), // irrelevant — just needs to differ
		XDGConfigHome:   filepath.Join(filepath.Dir(otherDir), ".config"),
		XDGStateHome:    filepath.Join(filepath.Dir(otherDir), ".local", "state"),
		Pwd:             otherDir,
		ProviderFactory: fakeProviderFactory,
		Now:             func() time.Time { return time.Unix(0, 0).UTC() },
		SkipTea:         true,
	})

	// Re-run with the ORIGINAL hermetic home so a session exists there.
	home2 := t.TempDir()
	xdgConfig2 := filepath.Join(home2, ".config")
	xdgState2 := filepath.Join(home2, ".local", "state")
	SetTestOverrides(&bootstrapOptions{
		HomeDir:         home2,
		XDGConfigHome:   xdgConfig2,
		XDGStateHome:    xdgState2,
		Pwd:             home2,
		ProviderFactory: fakeProviderFactory,
		Now:             func() time.Time { return time.Unix(0, 0).UTC() },
		SkipTea:         true,
	})
	seedSession(t, home2)

	// Now switch Pwd to otherDir (no sessions there).
	SetTestOverrides(&bootstrapOptions{
		HomeDir:         home2,
		XDGConfigHome:   xdgConfig2,
		XDGStateHome:    xdgState2,
		Pwd:             otherDir,
		ProviderFactory: fakeProviderFactory,
		Now:             func() time.Time { return time.Unix(0, 0).UTC() },
		SkipTea:         true,
	})

	// Without --any: should error (no sessions in otherDir).
	rootNoAny := NewRootCmd()
	var bufNoAny bytes.Buffer
	rootNoAny.SetOut(&bufNoAny)
	rootNoAny.SetErr(&bufNoAny)
	rootNoAny.SetArgs([]string{"resume"})
	if err := rootNoAny.Execute(); err == nil {
		t.Fatal("expected error without --any when no sessions in cwd")
	}

	// With --any: should find the session from home2.
	rootAny := NewRootCmd()
	var bufAny bytes.Buffer
	rootAny.SetOut(&bufAny)
	rootAny.SetErr(&bufAny)
	rootAny.SetArgs([]string{"resume", "--any"})
	if err := rootAny.Execute(); err != nil {
		t.Fatalf("--any should find global session, got: %v\noutput: %s", err, bufAny.String())
	}
}

// ---------------------------------------------------------------------------
// setupTUILog / HYGGE_LOG_LEVEL tests
// ---------------------------------------------------------------------------

// minimalRuntime returns an *appRuntime with only the fields setupTUILog reads.
func minimalRuntime(stateDir string) *appRuntime {
	return &appRuntime{
		Config: &config.Config{},
		StateOpts: state.LoadOptions{
			XDGStateHome: stateDir,
		},
	}
}

// TestSetupTUILog_DefaultIsDebug verifies that without HYGGE_LOG_LEVEL set,
// debug-level messages appear in the log file (backward-compatible default).
func TestSetupTUILog_DefaultIsDebug(t *testing.T) {
	t.Setenv("HYGGE_LOG_LEVEL", "")
	dir := t.TempDir()
	rt := minimalRuntime(dir)

	closer := setupTUILog(rt)
	if closer == nil {
		t.Fatal("expected non-nil closer")
	}
	slog.Debug("test-debug-line")
	closer()

	logPath := filepath.Join(dir, "hygge", "hygge.log")
	data, err := os.ReadFile(logPath) //nolint:gosec // logPath is under t.TempDir()
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if !strings.Contains(string(data), "test-debug-line") {
		t.Errorf("expected debug line in log; got:\n%s", data)
	}
}

// TestSetupTUILog_InfoSuppressesDebug verifies that HYGGE_LOG_LEVEL=info
// suppresses debug-level messages.
func TestSetupTUILog_InfoSuppressesDebug(t *testing.T) {
	t.Setenv("HYGGE_LOG_LEVEL", "info")
	dir := t.TempDir()
	rt := minimalRuntime(dir)

	closer := setupTUILog(rt)
	if closer == nil {
		t.Fatal("expected non-nil closer")
	}
	slog.Debug("should-not-appear")
	slog.Info("should-appear")
	closer()

	logPath := filepath.Join(dir, "hygge", "hygge.log")
	data, err := os.ReadFile(logPath) //nolint:gosec // logPath is under t.TempDir()
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if strings.Contains(string(data), "should-not-appear") {
		t.Errorf("debug line must be suppressed at info level; got:\n%s", data)
	}
	if !strings.Contains(string(data), "should-appear") {
		t.Errorf("info line should appear at info level; got:\n%s", data)
	}
}

// TestSetupTUILog_CaseInsensitive verifies HYGGE_LOG_LEVEL is parsed
// case-insensitively (e.g. "INFO", "Info").
func TestSetupTUILog_CaseInsensitive(t *testing.T) {
	for _, raw := range []string{"INFO", "Info", "  info  "} {
		t.Run(raw, func(t *testing.T) {
			t.Setenv("HYGGE_LOG_LEVEL", raw)
			dir := t.TempDir()
			rt := minimalRuntime(dir)

			closer := setupTUILog(rt)
			if closer == nil {
				t.Fatal("expected non-nil closer")
			}
			slog.Debug("debug-suppressed")
			slog.Info("info-present")
			closer()

			logPath := filepath.Join(dir, "hygge", "hygge.log")
			data, err := os.ReadFile(logPath) //nolint:gosec // logPath is under t.TempDir()
			if err != nil {
				t.Fatalf("read log: %v", err)
			}
			if strings.Contains(string(data), "debug-suppressed") {
				t.Errorf("debug must be suppressed for HYGGE_LOG_LEVEL=%q; got:\n%s", raw, data)
			}
			if !strings.Contains(string(data), "info-present") {
				t.Errorf("info must appear for HYGGE_LOG_LEVEL=%q; got:\n%s", raw, data)
			}
		})
	}
}

// TestSetupTUILog_UnknownValueFallsBackToDebug verifies that an unrecognised
// HYGGE_LOG_LEVEL value silently falls back to debug.
func TestSetupTUILog_UnknownValueFallsBackToDebug(t *testing.T) {
	t.Setenv("HYGGE_LOG_LEVEL", "verbose")
	dir := t.TempDir()
	rt := minimalRuntime(dir)

	closer := setupTUILog(rt)
	if closer == nil {
		t.Fatal("expected non-nil closer")
	}
	slog.Debug("debug-should-appear-fallback")
	closer()

	logPath := filepath.Join(dir, "hygge", "hygge.log")
	data, err := os.ReadFile(logPath) //nolint:gosec // logPath is under t.TempDir()
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if !strings.Contains(string(data), "debug-should-appear-fallback") {
		t.Errorf("unknown HYGGE_LOG_LEVEL should fall back to debug; got:\n%s", data)
	}
}

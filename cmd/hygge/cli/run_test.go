package cli

import (
	"bytes"
	"strings"
	"testing"
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

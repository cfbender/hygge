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

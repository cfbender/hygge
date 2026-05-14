package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestSessionsListEmpty(t *testing.T) {
	hermeticHome(t)

	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"sessions", "list"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "no sessions") {
		t.Errorf("expected 'no sessions', got:\n%s", got)
	}
}

func TestSessionsListWithSeed(t *testing.T) {
	home := hermeticHome(t)

	id1 := seedSession(t, home)
	id2 := seedSession(t, home)

	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"sessions", "list"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, shortID(id1)) {
		t.Errorf("output missing %s:\n%s", shortID(id1), got)
	}
	if !strings.Contains(got, shortID(id2)) {
		t.Errorf("output missing %s:\n%s", shortID(id2), got)
	}
}

func TestSessionsShow(t *testing.T) {
	home := hermeticHome(t)
	id := seedSession(t, home)

	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"sessions", "show", id[:6]})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, id) {
		t.Errorf("output missing full id:\n%s", got)
	}
	if !strings.Contains(got, "anthropic") {
		t.Errorf("output missing model provider:\n%s", got)
	}
}

func TestSessionsDeleteNoConfirm(t *testing.T) {
	home := hermeticHome(t)
	id := seedSession(t, home)

	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"sessions", "delete", id[:6], "--no-confirm"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	// list (without --include-deleted) should now omit it.
	root2 := NewRootCmd()
	var listOut bytes.Buffer
	root2.SetOut(&listOut)
	root2.SetErr(&listOut)
	root2.SetArgs([]string{"sessions", "list"})
	if err := root2.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if strings.Contains(listOut.String(), shortID(id)) {
		t.Errorf("deleted session still present in default list:\n%s", listOut.String())
	}

	// list --include-deleted should still show it.
	root3 := NewRootCmd()
	var listAllOut bytes.Buffer
	root3.SetOut(&listAllOut)
	root3.SetErr(&listAllOut)
	root3.SetArgs([]string{"sessions", "list", "--include-deleted"})
	if err := root3.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(listAllOut.String(), shortID(id)) {
		t.Errorf("deleted session missing from --include-deleted list:\n%s", listAllOut.String())
	}
}

func TestSessionsDeleteWithoutForceErrors(t *testing.T) {
	home := hermeticHome(t)
	id := seedSession(t, home)

	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"sessions", "delete", id[:6]})
	if err := root.Execute(); err == nil {
		t.Fatalf("expected error without -f or --no-confirm")
	}
	if !strings.Contains(out.String(), "refusing to delete") {
		t.Errorf("expected refusal message, got:\n%s", out.String())
	}
}

package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/cfbender/hygge/internal/session"
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

func TestSessionsListShowsTitleAndLatestMessages(t *testing.T) {
	home := hermeticHome(t)

	rt, err := bootstrap(context.Background(), bootstrapOptions{})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	sess, err := rt.Store.CreateSession(context.Background(), session.NewSession{
		ProjectDir: home,
		Model:      session.ModelRef{Provider: "anthropic", Name: "claude-sonnet-4-5"},
		Slug:       "Investigate sessions list",
	})
	if err != nil {
		_ = rt.Close()
		t.Fatalf("CreateSession: %v", err)
	}
	for _, msg := range []session.NewMessage{
		{Role: session.RoleUser, Parts: []session.Part{{Kind: session.PartText, Text: "initial request"}}},
		{Role: session.RoleAssistant, Parts: []session.Part{{Kind: session.PartText, Text: "initial answer"}}},
		{Role: session.RoleUser, Parts: []session.Part{{Kind: session.PartText, Text: "latest user message"}}},
		{Role: session.RoleAssistant, Parts: []session.Part{{Kind: session.PartText, Text: "latest agent message"}}},
	} {
		if _, err := rt.Store.AppendMessage(context.Background(), sess.ID, msg); err != nil {
			_ = rt.Close()
			t.Fatalf("AppendMessage: %v", err)
		}
	}
	_ = rt.Close()

	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"sessions", "list"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := out.String()
	for _, want := range []string{"TITLE", "LAST USER", "LAST AGENT", "Investigate sessions list", "latest user message", "latest agent message"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
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

func TestSessionsRename(t *testing.T) {
	home := hermeticHome(t)
	id := seedSession(t, home)

	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"sessions", "rename", id[:6], "my-new-slug"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	// Verify the slug was persisted via show.
	root2 := NewRootCmd()
	var showOut bytes.Buffer
	root2.SetOut(&showOut)
	root2.SetErr(&showOut)
	root2.SetArgs([]string{"sessions", "show", id[:6]})
	if err := root2.Execute(); err != nil {
		t.Fatalf("show: %v", err)
	}
	if !strings.Contains(showOut.String(), "my-new-slug") {
		t.Errorf("slug not visible in show output:\n%s", showOut.String())
	}
}

func TestSessionsListIncludeSubagents(t *testing.T) {
	home := hermeticHome(t)
	parentID := seedSession(t, home)

	// Seed a subagent session manually via the store.
	rt, err := bootstrap(context.Background(), bootstrapOptions{})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	subSess, err := rt.Store.CreateSession(context.Background(), session.NewSession{
		ProjectDir: home,
		Model:      session.ModelRef{Provider: "anthropic", Name: "claude-sonnet-4-5"},
		ParentID:   parentID,
		Kind:       session.KindSubagent,
	})
	if err != nil {
		_ = rt.Close()
		t.Fatalf("CreateSession subagent: %v", err)
	}
	_ = rt.Close()

	subPrefix := subSess.ID[:10] // use longer prefix to avoid collision

	// Default list: subagent should be hidden (only primary kind returned).
	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"sessions", "list"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	// Verify 'subagent' kind label is not in the default output.
	// A subagent row would show "subagent" in the KIND column.
	defaultRows := strings.Split(strings.TrimSpace(out.String()), "\n")
	for _, row := range defaultRows[1:] { // skip header
		if strings.Contains(row, subPrefix[:8]) && strings.Contains(row, "subagent") {
			t.Errorf("subagent row should be hidden by default:\n%s", out.String())
		}
	}

	// With --include-subagents the subagent row (KIND=subagent) should appear.
	root2 := NewRootCmd()
	var out2 bytes.Buffer
	root2.SetOut(&out2)
	root2.SetErr(&out2)
	root2.SetArgs([]string{"sessions", "list", "--include-subagents"})
	if err := root2.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out2.String(), "subagent") {
		t.Errorf("subagent kind missing from --include-subagents list:\n%s", out2.String())
	}
	if !strings.Contains(out2.String(), subPrefix[:8]) {
		t.Errorf("subagent id missing from --include-subagents list:\n%s", out2.String())
	}
}

func TestSessionsListQueryFilter(t *testing.T) {
	home := hermeticHome(t)

	// Create a session with a known slug.
	rt, err := bootstrap(context.Background(), bootstrapOptions{})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	defer func() { _ = rt.Close() }()
	slugSess, err := rt.Store.CreateSession(context.Background(), session.NewSession{
		ProjectDir: home,
		Model:      session.ModelRef{Provider: "anthropic", Name: "claude-sonnet-4-5"},
		Slug:       "special-task",
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	_, _ = rt.Store.CreateSession(context.Background(), session.NewSession{
		ProjectDir: home,
		Model:      session.ModelRef{Provider: "anthropic", Name: "claude-sonnet-4-5"},
		Slug:       "other-session",
	})
	_ = rt.Close()

	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"sessions", "list", "--query", "special"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, shortID(slugSess.ID)) {
		t.Errorf("query filter should show matching session:\n%s", got)
	}
	if strings.Contains(got, "other-session") {
		t.Errorf("query filter should hide non-matching session:\n%s", got)
	}
}

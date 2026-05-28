package command

import (
	"context"
	"strings"
	"testing"

	"github.com/cfbender/hygge/internal/provider"
	"github.com/cfbender/hygge/internal/session"
)

// fakeAppCfg is a builder for fakeApp values used by table tests.
type fakeAppCfg struct {
	sessionID string
	model     string
	reasoning string
	cost      float64
	sessions  []*session.Session
}

func (c *fakeAppCfg) build() *fakeApp {
	return &fakeApp{
		sessionID: c.sessionID,
		model:     c.model,
		reasoning: c.reasoning,
		cost:      c.cost,
		sessions:  c.sessions,
	}
}

// fakeApp is a tiny [App] implementation for tests.
type fakeApp struct {
	sessionID string
	model     string
	reasoning string
	cost      float64
	sessions  []*session.Session
}

func (f *fakeApp) SessionID() string { return f.sessionID }
func (f *fakeApp) Model() string     { return f.model }
func (f *fakeApp) Reasoning() provider.Reasoning {
	return provider.Reasoning{Effort: f.reasoning}
}
func (f *fakeApp) Cost() float64 { return f.cost }
func (f *fakeApp) Sessions(_ context.Context, _ int) ([]*session.Session, error) {
	return f.sessions, nil
}

func TestCommandInterfaceImplementations(t *testing.T) {
	t.Parallel()
	// Compile-time sanity that the package surface still exposes
	// the names we expect.
	var _ Command = (*helpCmd)(nil)
	var _ Command = (*newCmd)(nil)
	var _ Command = (*clearCmd)(nil)
	var _ Command = (*compactCmd)(nil)
	var _ Command = (*costCmd)(nil)
	var _ Command = (*sessionsCmd)(nil)
	var _ Command = (*forkCmd)(nil)
	var _ Command = (*modelCmd)(nil)
	var _ Command = (*reasonCmd)(nil)
	var _ Command = (*rememberCmd)(nil)
	var _ Command = (*memoryCmd)(nil)
	var _ Command = (*forgetCmd)(nil)
	var _ Command = (*versionCmd)(nil)
	var _ Command = (*layoutCmd)(nil)
	var _ Command = (*templateCommand)(nil)
	var _ App = (*fakeApp)(nil)
}

func TestOutcomeZeroValueIsNoop(t *testing.T) {
	t.Parallel()
	o := Outcome{}
	if o.Message != "" || o.Notice != "" || o.ClearHistory || o.NewSession || o.Compact || o.OpenModal != "" || len(o.Updates) != 0 {
		t.Fatalf("zero Outcome has non-zero fields: %+v", o)
	}
}

func TestNameRegexpAcceptsExpectedNames(t *testing.T) {
	t.Parallel()
	for _, ok := range []string{"help", "ab", "a1", "fork", "my-cmd", "with_underscore", "z9z9"} {
		if !nameRe.MatchString(ok) {
			t.Errorf("expected %q to match nameRe", ok)
		}
	}
	for _, bad := range []string{"", "Help", "1help", "-abc", "_abc", "hi there", "/help"} {
		if nameRe.MatchString(bad) {
			t.Errorf("expected %q to NOT match nameRe", bad)
		}
	}
}

// invalidNameCmd is used purely to exercise [Registry.Register]'s
// validation error path.
type invalidNameCmd struct{ name string }

func (c *invalidNameCmd) Name() string        { return c.name }
func (c *invalidNameCmd) Description() string { return "x" }
func (c *invalidNameCmd) Source() string      { return "builtin" }
func (c *invalidNameCmd) Args() []ArgSpec     { return nil }
func (c *invalidNameCmd) Execute(_ context.Context, _ App, _ string) (Outcome, error) {
	return Outcome{}, nil
}

func TestRegistryRegisterDuplicateNameErrors(t *testing.T) {
	t.Parallel()
	r := New()
	if err := r.Register(&clearCmd{}); err != nil {
		t.Fatalf("first register: %v", err)
	}
	if err := r.Register(&clearCmd{}); err == nil {
		t.Fatal("expected duplicate-name error on second register")
	}
}

func TestRegistryRegisterInvalidName(t *testing.T) {
	t.Parallel()
	r := New()
	if err := r.Register(&invalidNameCmd{name: "BAD-NAME"}); err == nil {
		t.Fatal("expected invalid-name error")
	}
}

func TestRegistryGetAndListSorted(t *testing.T) {
	t.Parallel()
	r := New()
	RegisterBuiltins(r)
	list := r.List()
	if len(list) == 0 {
		t.Fatal("expected built-ins")
	}
	for i := 1; i < len(list); i++ {
		if list[i-1].Name() >= list[i].Name() {
			t.Errorf("List not sorted: %q >= %q", list[i-1].Name(), list[i].Name())
		}
	}
	if _, ok := r.Get("help"); !ok {
		t.Error("expected /help to be registered")
	}
	if _, ok := r.Get("nope"); ok {
		t.Error("unexpected lookup hit for /nope")
	}
}

func TestRegistryLookupPrefix(t *testing.T) {
	t.Parallel()
	r := New()
	RegisterBuiltins(r)
	got := r.LookupPrefix("co")
	gotNames := names(got)
	want := []string{"compact", "cost"}
	if !equalStrings(gotNames, want) {
		t.Errorf("LookupPrefix(\"co\") = %v, want %v", gotNames, want)
	}
	if len(r.LookupPrefix("")) != r.Len() {
		t.Errorf("LookupPrefix(\"\") should return all %d commands, got %d", r.Len(), len(r.LookupPrefix("")))
	}
	if got := r.LookupPrefix("CO"); len(got) != 0 {
		t.Errorf("LookupPrefix is case-sensitive; got %v", names(got))
	}
}

func TestBuiltinHelpListsEverything(t *testing.T) {
	t.Parallel()
	r := New()
	RegisterBuiltins(r)
	AttachHelpRegistry(r)
	t.Cleanup(func() { AttachHelpRegistry(nil) })

	cmd, _ := r.Get("help")
	out, err := cmd.Execute(context.Background(), nil, "")
	if err != nil {
		t.Fatalf("/help: %v", err)
	}
	if out.OpenModal != ModalHelp {
		t.Errorf("OpenModal = %q, want %q", out.OpenModal, ModalHelp)
	}
	for _, name := range []string{"help", "new", "clear", "compact", "cost", "sessions", "fork", "model", "reason", "yolo", "layout", "version"} {
		if !strings.Contains(out.Notice, "/"+name) {
			t.Errorf("/help notice missing /%s:\n%s", name, out.Notice)
		}
	}
}

func TestBuiltinHelpDetail(t *testing.T) {
	t.Parallel()
	r := New()
	RegisterBuiltins(r)
	AttachHelpRegistry(r)
	t.Cleanup(func() { AttachHelpRegistry(nil) })

	cmd, _ := r.Get("help")
	out, err := cmd.Execute(context.Background(), nil, "model")
	if err != nil {
		t.Fatalf("/help model: %v", err)
	}
	if !strings.Contains(out.Notice, "/model") {
		t.Errorf("detail missing /model header:\n%s", out.Notice)
	}
	if !strings.Contains(out.Notice, "example:") {
		t.Errorf("detail missing example line:\n%s", out.Notice)
	}
}

func TestBuiltinHelpUnknownDetail(t *testing.T) {
	t.Parallel()
	r := New()
	RegisterBuiltins(r)
	AttachHelpRegistry(r)
	t.Cleanup(func() { AttachHelpRegistry(nil) })

	cmd, _ := r.Get("help")
	out, err := cmd.Execute(context.Background(), nil, "nope")
	if err != nil {
		t.Fatalf("/help nope: %v", err)
	}
	if !strings.Contains(out.Notice, "no command named") {
		t.Errorf("expected friendly error notice, got %q", out.Notice)
	}
}

func TestBuiltinOutcomes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		cmdName string
		input   string
		check   func(t *testing.T, o Outcome)
		appCfg  *fakeAppCfg
	}{
		{
			name:    "clear",
			cmdName: "clear",
			check: func(t *testing.T, o Outcome) {
				if !o.NewSession {
					t.Error("expected NewSession=true")
				}
				if o.ClearHistory {
					t.Error("expected ClearHistory=false")
				}
			},
		},
		{
			name:    "new",
			cmdName: "new",
			check: func(t *testing.T, o Outcome) {
				if !o.NewSession {
					t.Error("expected NewSession=true")
				}
			},
		},
		{
			name:    "compact",
			cmdName: "compact",
			check: func(t *testing.T, o Outcome) {
				if o.OpenModal != ModalCompactConfirm {
					t.Errorf("OpenModal=%q, want %q", o.OpenModal, ModalCompactConfirm)
				}
			},
		},
		{
			name:    "compact-force",
			cmdName: "compact",
			input:   "--force",
			check: func(t *testing.T, o Outcome) {
				if !o.Compact {
					t.Error("expected Compact=true for --force")
				}
			},
		},
		{
			name:    "cost",
			cmdName: "cost",
			appCfg:  &fakeAppCfg{cost: 0.1234},
			check: func(t *testing.T, o Outcome) {
				if !strings.Contains(o.Notice, "$0.1234") {
					t.Errorf("notice missing cost: %q", o.Notice)
				}
			},
		},
		{
			name:    "queue",
			cmdName: "queue",
			input:   "follow up after this",
			check: func(t *testing.T, o Outcome) {
				if got := o.Updates[UpdateQueueMessage]; got != "follow up after this" {
					t.Errorf("Updates[%q]=%q, want queued text", UpdateQueueMessage, got)
				}
			},
		},
		{
			name:    "steer",
			cmdName: "steer",
			input:   "prefer the smaller change",
			check: func(t *testing.T, o Outcome) {
				if got := o.Updates[UpdateSteerMessage]; got != "prefer the smaller change" {
					t.Errorf("Updates[%q]=%q, want steering text", UpdateSteerMessage, got)
				}
			},
		},
		{
			name:    "sessions",
			cmdName: "sessions",
			check: func(t *testing.T, o Outcome) {
				if o.OpenModal != ModalSessions {
					t.Errorf("OpenModal=%q, want %q", o.OpenModal, ModalSessions)
				}
			},
		},
		{
			name:    "fork-no-id",
			cmdName: "fork",
			check: func(t *testing.T, o Outcome) {
				if !strings.Contains(o.Notice, "latest message") {
					t.Errorf("expected latest-message notice, got %q", o.Notice)
				}
				if got := o.Updates[UpdateForkAt]; got != "latest" {
					t.Errorf("Updates[%q]=%q, want %q", UpdateForkAt, got, "latest")
				}
			},
		},
		{
			name:    "fork-with-id",
			cmdName: "fork",
			input:   "msg_abc",
			check: func(t *testing.T, o Outcome) {
				if got := o.Updates[UpdateForkAt]; got != "msg_abc" {
					t.Errorf("Updates[fork_at]=%q, want msg_abc", got)
				}
			},
		},
		{
			name:    "model-no-arg",
			cmdName: "model",
			check: func(t *testing.T, o Outcome) {
				if o.OpenModal != ModalModel {
					t.Errorf("OpenModal=%q, want %q", o.OpenModal, ModalModel)
				}
				if len(o.Updates) != 0 {
					t.Errorf("expected no updates, got %v", o.Updates)
				}
			},
		},
		{
			name:    "model-with-arg",
			cmdName: "model",
			input:   "openrouter/google-gemini-2-5-pro",
			check: func(t *testing.T, o Outcome) {
				if got := o.Updates[UpdateModel]; got != "openrouter/google-gemini-2-5-pro" {
					t.Errorf("Updates[model]=%q", got)
				}
			},
		},
		{
			name:    "model-malformed",
			cmdName: "model",
			input:   "justaname",
			check: func(t *testing.T, o Outcome) {
				if len(o.Updates) != 0 {
					t.Errorf("malformed model should not produce updates, got %v", o.Updates)
				}
				if !strings.Contains(o.Notice, "expected") {
					t.Errorf("expected hint notice, got %q", o.Notice)
				}
			},
		},
		{
			name:    "reason-no-arg",
			cmdName: "reason",
			appCfg:  &fakeAppCfg{reasoning: "medium"},
			check: func(t *testing.T, o Outcome) {
				if !strings.Contains(o.Notice, "medium") {
					t.Errorf("expected current reasoning in notice, got %q", o.Notice)
				}
			},
		},
		{
			name:    "reason-set",
			cmdName: "reason",
			input:   "HIGH",
			check: func(t *testing.T, o Outcome) {
				if got := o.Updates[UpdateReasoning]; got != "high" {
					t.Errorf("Updates[reasoning]=%q, want high", got)
				}
			},
		},
		{
			name:    "reason-invalid",
			cmdName: "reason",
			input:   "extreme",
			check: func(t *testing.T, o Outcome) {
				if len(o.Updates) != 0 {
					t.Errorf("invalid level should not produce updates, got %v", o.Updates)
				}
			},
		},
		{
			name:    "yolo-toggle",
			cmdName: "yolo",
			check: func(t *testing.T, o Outcome) {
				if got := o.Updates[UpdateYolo]; got != "toggle" {
					t.Errorf("Updates[yolo]=%q, want toggle", got)
				}
			},
		},
		{
			name:    "yolo-on",
			cmdName: "yolo",
			input:   "ON",
			check: func(t *testing.T, o Outcome) {
				if got := o.Updates[UpdateYolo]; got != "on" {
					t.Errorf("Updates[yolo]=%q, want on", got)
				}
			},
		},
		{
			name:    "yolo-invalid",
			cmdName: "yolo",
			input:   "forever",
			check: func(t *testing.T, o Outcome) {
				if len(o.Updates) != 0 {
					t.Errorf("invalid yolo state should not produce updates, got %v", o.Updates)
				}
				if !strings.Contains(o.Notice, "expected") {
					t.Errorf("expected hint notice, got %q", o.Notice)
				}
			},
		},
		{
			name:    "remember-picker",
			cmdName: "remember",
			input:   "prefers short final summaries",
			check: func(t *testing.T, o Outcome) {
				if o.OpenModal != ModalRememberMemory {
					t.Errorf("OpenModal=%q, want %q", o.OpenModal, ModalRememberMemory)
				}
				if got := o.Updates[UpdateRememberMemoryDraft]; got != "prefers short final summaries" {
					t.Errorf("Updates[remember_memory_draft]=%q", got)
				}
				if o.Notice != "" {
					t.Errorf("remember should let the UI choose scope, got notice %q", o.Notice)
				}
			},
		},
		{
			name:    "remember-project",
			cmdName: "remember",
			input:   "--project use mise run precommit",
			check: func(t *testing.T, o Outcome) {
				if got := o.Updates[UpdateRememberSessionMemory]; got != "project\nuse mise run precommit" {
					t.Errorf("Updates[remember_session_memory]=%q", got)
				}
			},
		},
		{
			name:    "remember-empty",
			cmdName: "remember",
			input:   "   ",
			check: func(t *testing.T, o Outcome) {
				if o.OpenModal != ModalRememberMemory {
					t.Errorf("OpenModal=%q, want %q", o.OpenModal, ModalRememberMemory)
				}
				if got := o.Updates[UpdateRememberMemoryDraft]; got != "" {
					t.Errorf("empty remember draft=%q, want empty", got)
				}
				if o.Notice != "" {
					t.Errorf("remember should not use notice, got %q", o.Notice)
				}
			},
		},
		{
			name:    "memory",
			cmdName: "memory",
			check: func(t *testing.T, o Outcome) {
				if o.OpenModal != ModalMemory {
					t.Errorf("OpenModal=%q, want %q", o.OpenModal, ModalMemory)
				}
			},
		},
		{
			name:    "forget",
			cmdName: "forget",
			input:   "01MEMORY",
			check: func(t *testing.T, o Outcome) {
				if got := o.Updates[UpdateForgetMemory]; got != "session\n01MEMORY" {
					t.Errorf("Updates[forget_memory]=%q", got)
				}
				if o.Notice != "" {
					t.Errorf("forget should let the UI report delete/failure state, got notice %q", o.Notice)
				}
			},
		},
		{
			name:    "forget-picker",
			cmdName: "forget",
			check: func(t *testing.T, o Outcome) {
				if o.OpenModal != ModalForgetMemory {
					t.Errorf("OpenModal=%q, want %q", o.OpenModal, ModalForgetMemory)
				}
				if len(o.Updates) != 0 {
					t.Errorf("Updates=%v, want none", o.Updates)
				}
			},
		},
		{
			name:    "forget-global",
			cmdName: "forget",
			input:   "--global 01GLOBALMEMORY",
			check: func(t *testing.T, o Outcome) {
				if got := o.Updates[UpdateForgetMemory]; got != "global\n01GLOBALMEMORY" {
					t.Errorf("Updates[forget_memory]=%q", got)
				}
			},
		},
		{
			name:    "layout-toggle",
			cmdName: "layout",
			check: func(t *testing.T, o Outcome) {
				if got := o.Updates[UpdateLayout]; got != "toggle" {
					t.Errorf("Updates[layout]=%q, want toggle", got)
				}
				if o.Notice != "" {
					t.Errorf("command-layer notice = %q, want empty so UI can report resolved layout", o.Notice)
				}
			},
		},
		{
			name:    "layout-compact",
			cmdName: "layout",
			input:   "compact",
			check: func(t *testing.T, o Outcome) {
				if got := o.Updates[UpdateLayout]; got != "compact" {
					t.Errorf("Updates[layout]=%q, want compact", got)
				}
			},
		},
		{
			name:    "layout-default",
			cmdName: "layout",
			input:   "default",
			check: func(t *testing.T, o Outcome) {
				if got := o.Updates[UpdateLayout]; got != "default" {
					t.Errorf("Updates[layout]=%q, want default", got)
				}
			},
		},
		{
			name:    "layout-invalid",
			cmdName: "layout",
			input:   "wide",
			check: func(t *testing.T, o Outcome) {
				if len(o.Updates) != 0 {
					t.Errorf("invalid layout arg should not produce updates, got %v", o.Updates)
				}
				if !strings.Contains(o.Notice, "expected") {
					t.Errorf("expected hint notice, got %q", o.Notice)
				}
			},
		},
		{
			name:    "version",
			cmdName: "version",
			check: func(t *testing.T, o Outcome) {
				if !strings.Contains(o.Notice, "hygge") {
					t.Errorf("expected version in notice, got %q", o.Notice)
				}
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := New()
			RegisterBuiltins(r)
			cmd, ok := r.Get(c.cmdName)
			if !ok {
				t.Fatalf("no built-in named %q", c.cmdName)
			}
			var app App
			if c.appCfg != nil {
				app = c.appCfg.build()
			}
			out, err := cmd.Execute(context.Background(), app, c.input)
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			c.check(t, out)
		})
	}
}

func TestSetVersion(t *testing.T) {
	prev := currentVersion()
	t.Cleanup(func() { versionString.Store(prev) })
	SetVersion("9.9.9")
	v := &versionCmd{}
	out, _ := v.Execute(context.Background(), nil, "")
	if !strings.Contains(out.Notice, "9.9.9") {
		t.Errorf("notice = %q, want to contain 9.9.9", out.Notice)
	}
	// Empty SetVersion is a no-op.
	SetVersion("")
	if currentVersion() != "9.9.9" {
		t.Errorf("empty SetVersion should not change versionString, got %q", currentVersion())
	}
}

func names(cs []Command) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Name()
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

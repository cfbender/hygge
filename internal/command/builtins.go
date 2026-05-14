package command

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync/atomic"
)

// RegisterBuiltins registers the hygge built-in commands on reg.
// Built-ins always carry Source() == "builtin".  TOML files MAY
// override them by name; that's how users customise prompts hygge
// would otherwise hard-code.
//
// Failures here are programmer errors (duplicate names in the
// built-in set) and panic — there is no recovery path.  Tests pin
// the full list so regressions are caught at the unit-test level.
func RegisterBuiltins(reg *Registry) {
	for _, c := range builtinCommands() {
		if err := reg.Register(c); err != nil {
			panic(fmt.Sprintf("command: register builtin %q: %v", c.Name(), err))
		}
	}
}

// builtinCommands returns the full built-in set in stable order.
// Exposed as a function (not a package var) so tests can verify the
// list independently of whatever Load may have layered on top.
func builtinCommands() []Command {
	return []Command{
		&helpCmd{},
		&clearCmd{},
		&compactCmd{},
		&costCmd{},
		&sessionsCmd{},
		&forkCmd{},
		&modelCmd{},
		&reasonCmd{},
		&versionCmd{},
	}
}

// versionString is overridden at link time / by tests to inject the
// hygge version surfaced by /version.  Defaults to "dev" when unset.
// Stored via [atomic.Value] so concurrent [SetVersion] calls (rare
// in production, common in parallel tests) are race-free.
var versionString atomic.Value // string

func init() {
	versionString.Store("dev")
}

// SetVersion installs the version string surfaced by the /version
// built-in.  Called by cmd/hygge/cli at bootstrap so the built-in
// reports the same version as `hygge version`.  Empty input is a
// no-op.
func SetVersion(v string) {
	if v == "" {
		return
	}
	versionString.Store(v)
}

// currentVersion returns the active version string for the /version
// command.  Falls back to "dev" when nothing has been stored.
func currentVersion() string {
	if v, ok := versionString.Load().(string); ok && v != "" {
		return v
	}
	return "dev"
}

// --- /help ----------------------------------------------------------------

type helpCmd struct{}

func (*helpCmd) Name() string        { return "help" }
func (*helpCmd) Description() string { return "Show available commands or details for one" }
func (*helpCmd) Source() string      { return "builtin" }
func (*helpCmd) Args() []ArgSpec {
	return []ArgSpec{{Name: "command", Description: "name of the command to detail", Required: false}}
}

// helpRegistry is the package-level pointer the /help command reads
// to enumerate every registered command (including TOML extensions).
// Stored via [atomic.Pointer] so concurrent SetVersion / Load /
// Attach calls (used widely by tests running in parallel) are
// race-free.  A nil pointer means "fall back to the built-in list
// only" so /help is still useful in tests that construct a registry
// by hand and skip [AttachHelpRegistry].
var helpRegistry atomic.Pointer[Registry]

func (*helpCmd) Execute(_ context.Context, _ App, input string) (Outcome, error) {
	name := strings.TrimSpace(input)
	if name != "" {
		cmd, ok := lookupHelp(name)
		if !ok {
			return Outcome{Notice: fmt.Sprintf("/help: no command named %q", name)}, nil
		}
		return Outcome{
			Notice:    formatCommandHelp(cmd),
			OpenModal: ModalHelp,
		}, nil
	}
	return Outcome{
		Notice:    formatHelpList(allHelp()),
		OpenModal: ModalHelp,
	}, nil
}

// lookupHelp finds a command in the active registry, falling back to
// the built-in set when no registry has been wired up.
func lookupHelp(name string) (Command, bool) {
	if reg := helpRegistry.Load(); reg != nil {
		return reg.Get(name)
	}
	for _, c := range builtinCommands() {
		if c.Name() == name {
			return c, true
		}
	}
	return nil, false
}

// allHelp returns every command visible to /help, sorted by name.
func allHelp() []Command {
	if reg := helpRegistry.Load(); reg != nil {
		return reg.List()
	}
	out := builtinCommands()
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

func formatHelpList(cmds []Command) string {
	var b strings.Builder
	b.WriteString("Available commands:\n")
	width := 0
	for _, c := range cmds {
		if n := len(c.Name()); n > width {
			width = n
		}
	}
	for _, c := range cmds {
		fmt.Fprintf(&b, "  /%-*s  %s  [%s]\n", width, c.Name(), c.Description(), c.Source())
	}
	b.WriteString("Type `/help <name>` for details.")
	return b.String()
}

func formatCommandHelp(c Command) string {
	var b strings.Builder
	fmt.Fprintf(&b, "/%s  [%s]\n", c.Name(), c.Source())
	fmt.Fprintf(&b, "  %s\n", c.Description())
	if args := c.Args(); len(args) > 0 {
		b.WriteString("  args:\n")
		for _, a := range args {
			req := ""
			if a.Required {
				req = " (required)"
			}
			desc := a.Description
			if desc == "" {
				desc = "(no description)"
			}
			fmt.Fprintf(&b, "    %s%s — %s\n", a.Name, req, desc)
		}
		// Example.
		parts := make([]string, 0, len(args))
		for _, a := range args {
			parts = append(parts, "<"+a.Name+">")
		}
		fmt.Fprintf(&b, "  example: /%s %s\n", c.Name(), strings.Join(parts, " "))
	} else {
		fmt.Fprintf(&b, "  example: /%s\n", c.Name())
	}
	return b.String()
}

// --- /clear ---------------------------------------------------------------

type clearCmd struct{}

func (*clearCmd) Name() string        { return "clear" }
func (*clearCmd) Description() string { return "Clear the rendered session history" }
func (*clearCmd) Source() string      { return "builtin" }
func (*clearCmd) Args() []ArgSpec     { return nil }
func (*clearCmd) Execute(_ context.Context, _ App, _ string) (Outcome, error) {
	return Outcome{
		ClearHistory: true,
		Notice:       "history cleared",
	}, nil
}

// --- /compact -------------------------------------------------------------

type compactCmd struct{}

func (*compactCmd) Name() string        { return "compact" }
func (*compactCmd) Description() string { return "Run session compaction now" }
func (*compactCmd) Source() string      { return "builtin" }
func (*compactCmd) Args() []ArgSpec     { return nil }
func (*compactCmd) Execute(_ context.Context, _ App, _ string) (Outcome, error) {
	return Outcome{Compact: true}, nil
}

// --- /cost ----------------------------------------------------------------

type costCmd struct{}

func (*costCmd) Name() string        { return "cost" }
func (*costCmd) Description() string { return "Show running session cost" }
func (*costCmd) Source() string      { return "builtin" }
func (*costCmd) Args() []ArgSpec     { return nil }
func (*costCmd) Execute(_ context.Context, app App, _ string) (Outcome, error) {
	dollars := 0.0
	if app != nil {
		dollars = app.Cost()
	}
	return Outcome{Notice: fmt.Sprintf("running cost: $%.4f", dollars)}, nil
}

// --- /sessions ------------------------------------------------------------

type sessionsCmd struct{}

func (*sessionsCmd) Name() string        { return "sessions" }
func (*sessionsCmd) Description() string { return "Open the sessions picker" }
func (*sessionsCmd) Source() string      { return "builtin" }
func (*sessionsCmd) Args() []ArgSpec     { return nil }
func (*sessionsCmd) Execute(_ context.Context, _ App, _ string) (Outcome, error) {
	return Outcome{
		OpenModal: ModalSessions,
	}, nil
}

// --- /fork ----------------------------------------------------------------

type forkCmd struct{}

func (*forkCmd) Name() string        { return "fork" }
func (*forkCmd) Description() string { return "Fork the session at a message id" }
func (*forkCmd) Source() string      { return "builtin" }
func (*forkCmd) Args() []ArgSpec {
	return []ArgSpec{{Name: "message_id", Description: "id of the message to fork at", Required: false}}
}

func (*forkCmd) Execute(_ context.Context, _ App, input string) (Outcome, error) {
	id := strings.TrimSpace(input)
	if id == "" {
		// No arg: fork at the foreground session's latest user message.
		return Outcome{
			Updates: map[string]string{UpdateForkAt: "latest"},
			Notice:  "forking at latest message…",
		}, nil
	}
	return Outcome{
		Updates: map[string]string{UpdateForkAt: id},
		Notice:  fmt.Sprintf("forking at message %s…", id),
	}, nil
}

// --- /model ---------------------------------------------------------------

type modelCmd struct{}

func (*modelCmd) Name() string        { return "model" }
func (*modelCmd) Description() string { return "Show or switch the active model" }
func (*modelCmd) Source() string      { return "builtin" }
func (*modelCmd) Args() []ArgSpec {
	return []ArgSpec{{Name: "ref", Description: `"<provider>/<model-id>"`, Required: false}}
}
func (*modelCmd) Execute(_ context.Context, app App, input string) (Outcome, error) {
	ref := strings.TrimSpace(input)
	if ref == "" {
		current := "(unknown)"
		if app != nil {
			current = app.Model()
		}
		return Outcome{Notice: fmt.Sprintf("current model: %s", current)}, nil
	}
	if !strings.Contains(ref, "/") {
		return Outcome{Notice: `/model: expected "<provider>/<model-id>"`}, nil
	}
	return Outcome{
		Updates: map[string]string{UpdateModel: ref},
		Notice:  fmt.Sprintf("switching model to %s", ref),
	}, nil
}

// --- /reason --------------------------------------------------------------

type reasonCmd struct{}

func (*reasonCmd) Name() string        { return "reason" }
func (*reasonCmd) Description() string { return "Show or switch reasoning depth" }
func (*reasonCmd) Source() string      { return "builtin" }
func (*reasonCmd) Args() []ArgSpec {
	return []ArgSpec{{Name: "level", Description: "off | low | medium | high", Required: false}}
}
func (*reasonCmd) Execute(_ context.Context, app App, input string) (Outcome, error) {
	level := strings.ToLower(strings.TrimSpace(input))
	if level == "" {
		current := "off"
		if app != nil {
			if eff := app.Reasoning().Effort; eff != "" {
				current = eff
			}
		}
		return Outcome{Notice: fmt.Sprintf("current reasoning: %s", current)}, nil
	}
	switch level {
	case "off", "low", "medium", "high":
		// ok
	default:
		return Outcome{Notice: `/reason: expected "off", "low", "medium", or "high"`}, nil
	}
	return Outcome{
		Updates: map[string]string{UpdateReasoning: level},
		Notice:  fmt.Sprintf("reasoning set to %s", level),
	}, nil
}

// --- /version -------------------------------------------------------------

type versionCmd struct{}

func (*versionCmd) Name() string        { return "version" }
func (*versionCmd) Description() string { return "Show hygge version" }
func (*versionCmd) Source() string      { return "builtin" }
func (*versionCmd) Args() []ArgSpec     { return nil }
func (*versionCmd) Execute(_ context.Context, _ App, _ string) (Outcome, error) {
	return Outcome{Notice: fmt.Sprintf("hygge %s", currentVersion())}, nil
}

// AttachHelpRegistry wires reg into the /help command so it sees the
// fully-layered command set, not just the built-ins.  Called by
// [Load] after the layered loader has finished.  Safe for concurrent
// invocation across many [Load] calls in parallel tests.
func AttachHelpRegistry(reg *Registry) {
	helpRegistry.Store(reg)
}

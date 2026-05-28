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
	for _, c := range builtinCommandsFor(reg) {
		if err := reg.Register(c); err != nil {
			panic(fmt.Sprintf("command: register builtin %q: %v", c.Name(), err))
		}
	}
}

// builtinCommands returns the full built-in set in stable order.
// Exposed as a function (not a package var) so tests can verify the
// list independently of whatever Load may have layered on top.
func builtinCommands() []Command {
	return builtinCommandsFor(nil)
}

func builtinCommandsFor(reg *Registry) []Command {
	return []Command{
		&helpCmd{registry: reg},
		&newCmd{},
		&clearCmd{},
		&compactCmd{},
		&costCmd{},
		&queueCmd{},
		&steerCmd{},
		&attachCmd{},
		&attachmentsCmd{},
		&memoryCmd{},
		&sessionsCmd{},
		&forkCmd{},
		&modelCmd{},
		&themeCmd{},
		&apiKeyCmd{},
		&reasonCmd{},
		&yoloCmd{},
		&rememberCmd{},
		&forgetCmd{},
		&versionCmd{},
		&layoutCmd{},
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

// --- /memory ---------------------------------------------------------------

type memoryCmd struct{}

func (*memoryCmd) Name() string        { return "memory" }
func (*memoryCmd) Description() string { return "Open the memory manager" }
func (*memoryCmd) Source() string      { return "builtin" }
func (*memoryCmd) Args() []ArgSpec     { return nil }
func (*memoryCmd) Execute(_ context.Context, _ App, _ string) (Outcome, error) {
	return Outcome{OpenModal: ModalMemory}, nil
}

// --- /help ----------------------------------------------------------------

type helpCmd struct {
	registry *Registry
}

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

func (h *helpCmd) Execute(_ context.Context, _ App, input string) (Outcome, error) {
	name := strings.TrimSpace(input)
	if name != "" {
		cmd, ok := h.lookup(name)
		if !ok {
			return Outcome{Notice: fmt.Sprintf("/help: no command named %q", name)}, nil
		}
		return Outcome{
			Notice:    formatCommandHelp(cmd),
			OpenModal: ModalHelp,
		}, nil
	}
	return Outcome{
		Notice:    formatHelpList(h.all()),
		OpenModal: ModalHelp,
	}, nil
}

// lookup finds a command in this help command's registry, falling back
// to the global registry for legacy callers that construct help by hand.
func (h *helpCmd) lookup(name string) (Command, bool) {
	if h.registry != nil {
		return h.registry.Get(name)
	}
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

// all returns every command visible to /help, sorted by name.
func (h *helpCmd) all() []Command {
	if h.registry != nil {
		return h.registry.List()
	}
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

// --- /attach ---------------------------------------------------------------

type attachCmd struct{}

func (*attachCmd) Name() string        { return "attach" }
func (*attachCmd) Description() string { return "Attach a local file to the next prompt" }
func (*attachCmd) Source() string      { return "builtin" }
func (*attachCmd) Args() []ArgSpec {
	return []ArgSpec{{Name: "path", Description: "local file path to attach", Required: true}}
}
func (*attachCmd) Execute(_ context.Context, _ App, input string) (Outcome, error) {
	path := strings.TrimSpace(input)
	if path == "" {
		return Outcome{Notice: "usage: /attach <path>"}, nil
	}
	return Outcome{Updates: map[string]string{UpdateAttachFile: path}}, nil
}

// --- /attachments ---------------------------------------------------------

type attachmentsCmd struct{}

func (*attachmentsCmd) Name() string        { return "attachments" }
func (*attachmentsCmd) Description() string { return "Manage pending prompt attachments" }
func (*attachmentsCmd) Source() string      { return "builtin" }
func (*attachmentsCmd) Args() []ArgSpec {
	return []ArgSpec{{Name: "action", Description: "clear removes all pending attachments", Required: true}}
}
func (*attachmentsCmd) Execute(_ context.Context, _ App, input string) (Outcome, error) {
	action := strings.TrimSpace(input)
	if action != "clear" {
		return Outcome{Notice: "usage: /attachments clear"}, nil
	}
	return Outcome{Updates: map[string]string{UpdateAttachments: "clear"}}, nil
}

// --- /new /clear ----------------------------------------------------------

type newCmd struct{}

func (*newCmd) Name() string        { return "new" }
func (*newCmd) Description() string { return "Start a fresh session" }
func (*newCmd) Source() string      { return "builtin" }
func (*newCmd) Args() []ArgSpec     { return nil }
func (*newCmd) Execute(_ context.Context, _ App, _ string) (Outcome, error) {
	return Outcome{NewSession: true}, nil
}

type clearCmd struct{}

func (*clearCmd) Name() string        { return "clear" }
func (*clearCmd) Description() string { return "Alias for /new" }
func (*clearCmd) Source() string      { return "builtin" }
func (*clearCmd) Args() []ArgSpec     { return nil }
func (*clearCmd) Execute(_ context.Context, _ App, _ string) (Outcome, error) {
	return Outcome{NewSession: true}, nil
}

// --- /compact -------------------------------------------------------------

type compactCmd struct{}

func (*compactCmd) Name() string        { return "compact" }
func (*compactCmd) Description() string { return "Compact the session (opens confirmation modal)" }
func (*compactCmd) Source() string      { return "builtin" }
func (*compactCmd) Args() []ArgSpec {
	return []ArgSpec{{Name: "force", Description: "--force skips the modal and compacts immediately", Required: false}}
}
func (*compactCmd) Execute(_ context.Context, _ App, input string) (Outcome, error) {
	// /compact --force bypasses the modal (power-user escape hatch).
	if strings.TrimSpace(input) == "--force" {
		return Outcome{Compact: true}, nil
	}
	// Default: open the confirmation modal.  The TUI instantiates
	// components.CompactionModal with the foreground session's metadata.
	// On [y], the App calls Agent.Compact directly.
	return Outcome{
		OpenModal: ModalCompactConfirm,
		Notice:    "review compaction details",
	}, nil
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

// --- /queue ---------------------------------------------------------------

type queueCmd struct{}

func (*queueCmd) Name() string        { return "queue" }
func (*queueCmd) Description() string { return "Queue a message for after the active turn" }
func (*queueCmd) Source() string      { return "builtin" }
func (*queueCmd) Args() []ArgSpec {
	return []ArgSpec{{Name: "message", Description: "message to send after the active turn", Required: true}}
}
func (*queueCmd) Execute(_ context.Context, _ App, input string) (Outcome, error) {
	text := strings.TrimSpace(input)
	if text == "" {
		return Outcome{Notice: "usage: /queue <message>"}, nil
	}
	return Outcome{Updates: map[string]string{UpdateQueueMessage: text}}, nil
}

// --- /steer ---------------------------------------------------------------

type steerCmd struct{}

func (*steerCmd) Name() string        { return "steer" }
func (*steerCmd) Description() string { return "Guide the active turn at the next Fantasy step" }
func (*steerCmd) Source() string      { return "builtin" }
func (*steerCmd) Args() []ArgSpec {
	return []ArgSpec{{Name: "message", Description: "guidance for the active turn", Required: true}}
}
func (*steerCmd) Execute(_ context.Context, _ App, input string) (Outcome, error) {
	text := strings.TrimSpace(input)
	if text == "" {
		return Outcome{Notice: "usage: /steer <message>"}, nil
	}
	return Outcome{Updates: map[string]string{UpdateSteerMessage: text}}, nil
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
func (*modelCmd) Execute(_ context.Context, _ App, input string) (Outcome, error) {
	ref := strings.TrimSpace(input)
	if ref == "" {
		return Outcome{OpenModal: ModalModel}, nil
	}
	if !strings.Contains(ref, "/") {
		return Outcome{Notice: `/model: expected "<provider>/<model-id>"`}, nil
	}
	return Outcome{
		Updates: map[string]string{UpdateModel: ref},
		Notice:  fmt.Sprintf("switching model to %s", ref),
	}, nil
}

// --- /theme ---------------------------------------------------------------

type themeCmd struct{}

func (*themeCmd) Name() string        { return "theme" }
func (*themeCmd) Description() string { return "Show or switch the active theme" }
func (*themeCmd) Source() string      { return "builtin" }
func (*themeCmd) Args() []ArgSpec {
	return []ArgSpec{{Name: "name", Description: "theme name", Required: false}}
}
func (*themeCmd) Execute(_ context.Context, _ App, input string) (Outcome, error) {
	name := strings.TrimSpace(input)
	if name == "" {
		return Outcome{OpenModal: ModalTheme}, nil
	}
	return Outcome{Updates: map[string]string{UpdateTheme: name}, Notice: fmt.Sprintf("switching theme to %s", name)}, nil
}

// --- /apikey --------------------------------------------------------------

type apiKeyCmd struct{}

func (*apiKeyCmd) Name() string        { return "apikey" }
func (*apiKeyCmd) Description() string { return "Set the API key for a provider" }
func (*apiKeyCmd) Source() string      { return "builtin" }
func (*apiKeyCmd) Args() []ArgSpec {
	return []ArgSpec{{Name: "provider", Description: "provider id (defaults to current provider)", Required: false}}
}
func (*apiKeyCmd) Execute(_ context.Context, _ App, input string) (Outcome, error) {
	provider := strings.TrimSpace(input)
	return Outcome{OpenModal: ModalAPIKey, Updates: map[string]string{"apikey_provider": provider}}, nil
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

// --- /yolo ----------------------------------------------------------------

type yoloCmd struct{}

func (*yoloCmd) Name() string        { return "yolo" }
func (*yoloCmd) Description() string { return "Toggle yolo mode (allow non-secret tool actions)" }
func (*yoloCmd) Source() string      { return "builtin" }
func (*yoloCmd) Args() []ArgSpec {
	return []ArgSpec{{Name: "state", Description: "on | off | toggle", Required: false}}
}
func (*yoloCmd) Execute(_ context.Context, _ App, input string) (Outcome, error) {
	state := strings.ToLower(strings.TrimSpace(input))
	if state == "" {
		state = "toggle"
	}
	switch state {
	case "on", "off", "toggle":
		return Outcome{Updates: map[string]string{UpdateYolo: state}}, nil
	default:
		return Outcome{Notice: `/yolo: expected "on", "off", or "toggle"`}, nil
	}
}

// --- /remember -------------------------------------------------------------

type rememberCmd struct{}

func (*rememberCmd) Name() string        { return "remember" }
func (*rememberCmd) Description() string { return "Remember a fact for this session" }
func (*rememberCmd) Source() string      { return "builtin" }
func (*rememberCmd) Args() []ArgSpec {
	return []ArgSpec{{Name: "fact", Description: "fact to remember; choose scope when omitted", Required: false}}
}
func (*rememberCmd) Execute(_ context.Context, _ App, input string) (Outcome, error) {
	scope, fact, explicitScope := parseRememberInput(input)
	if !explicitScope {
		return Outcome{OpenModal: ModalRememberMemory, Updates: map[string]string{UpdateRememberMemoryDraft: fact}}, nil
	}
	return Outcome{
		Updates: map[string]string{UpdateRememberSessionMemory: string(scope) + "\n" + fact},
	}, nil
}

func parseRememberInput(input string) (string, string, bool) {
	return parseMemoryScopedInput(input)
}

// --- /forget ---------------------------------------------------------------

type forgetCmd struct{}

func (*forgetCmd) Name() string        { return "forget" }
func (*forgetCmd) Description() string { return "Forget a remembered fact by memory ID" }
func (*forgetCmd) Source() string      { return "builtin" }
func (*forgetCmd) Args() []ArgSpec {
	return []ArgSpec{{Name: "memory_id", Description: "memory ID to forget", Required: true}}
}
func (*forgetCmd) Execute(_ context.Context, _ App, input string) (Outcome, error) {
	if strings.TrimSpace(input) == "" {
		return Outcome{OpenModal: ModalForgetMemory}, nil
	}
	scope, memoryID := parseForgetInput(input)
	return Outcome{
		Updates: map[string]string{UpdateForgetMemory: string(scope) + "\n" + memoryID},
	}, nil
}

func parseForgetInput(input string) (string, string) {
	scope, value, _ := parseMemoryScopedInput(input)
	return scope, value
}

func parseMemoryScopedInput(input string) (string, string, bool) {
	input = strings.TrimSpace(input)
	for _, scope := range []string{"session", "project", "global"} {
		flag := "--" + scope
		if input == flag {
			return scope, "", true
		}
		if rest, ok := strings.CutPrefix(input, flag+" "); ok {
			return scope, strings.TrimSpace(rest), true
		}
	}
	return "session", input, false
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

// --- /layout --------------------------------------------------------------

type layoutCmd struct{}

func (*layoutCmd) Name() string        { return "layout" }
func (*layoutCmd) Description() string { return "Toggle or set in-session layout (default | compact)" }
func (*layoutCmd) Source() string      { return "builtin" }
func (*layoutCmd) Args() []ArgSpec {
	return []ArgSpec{{Name: "mode", Description: "default | compact (omit to toggle)", Required: false}}
}
func (*layoutCmd) Execute(_ context.Context, _ App, input string) (Outcome, error) {
	mode := strings.ToLower(strings.TrimSpace(input))
	if mode == "" {
		mode = "toggle"
	}
	switch mode {
	case "default", "compact", "toggle":
		return Outcome{Updates: map[string]string{UpdateLayout: mode}}, nil
	default:
		return Outcome{Notice: `/layout: expected "default", "compact", or omit to toggle`}, nil
	}
}

// AttachHelpRegistry wires reg into the /help command so it sees the
// fully-layered command set, not just the built-ins.  Called by
// [Load] after the layered loader has finished.  Safe for concurrent
// invocation across many [Load] calls in parallel tests.
func AttachHelpRegistry(reg *Registry) {
	helpRegistry.Store(reg)
}

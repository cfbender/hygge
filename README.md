# hygge

A terminal-based AI coding assistant.

## Status

v0.1-dev. The core loop is working:

- Anthropic provider with streaming responses and prompt caching.
- Six builtin tools: `read`, `write`, `edit`, `bash`, `grep`, `glob`.
- Permission engine with secrets denylist, scope-based allowances
  (`once` / `session` / `always`), and a `.hygge/config.toml` walk-up.
- SQLite-backed session store with resume.
- TOML config with profile inheritance and `state.json` for runtime data.
- Cost tracking via the [models.dev](https://models.dev) catalog with an
  embedded snapshot for offline use.
- Unified model catalog (`internal/catalog`) sourced from models.dev,
  driving cost lookups, provider model lists, and capability detection
  (reasoning, tools, vision).

## Quick start

Requires [mise](https://mise.jdx.dev) for the Go toolchain pin.

```sh
mise install                    # installs Go 1.26 and golangci-lint 2
mise run build                  # compiles to ./bin/hygge
export ANTHROPIC_API_KEY=...    # required to talk to the model
./bin/hygge                     # launches the TUI in the current directory
```

## Commands

- `hygge` — launch the TUI for a new session in the current directory.
- `hygge resume [id-prefix]` — re-open the most recent matching session.
- `hygge sessions list` — list recent sessions; `--include-deleted` to show soft-deleted rows.
- `hygge sessions delete <id-prefix>` — soft-delete a session.
- `hygge profile list` / `hygge profile use <name>` — manage config profiles.
- `hygge provider auth [name]` / `list` / `remove` — manage per-machine API credentials.
- `hygge config explain [key]` — print the effective config with provenance.
- `hygge theme list` / `hygge theme show <name>` — inspect available themes.
- `hygge skills list` / `show <name>` / `doctor` — inspect loaded skills.
- `hygge subagents list` / `hygge subagents show <name>` — inspect registered sub-agent types invokable by the `task` tool.
- `hygge commands list` / `hygge commands show <name>` — inspect slash commands available in the TUI.
- `hygge context list` / `show` / `paths` — inspect the project-context files (`AGENTS.md` / `CLAUDE.md`) contributing to the system prompt.
- `hygge catalog list [<provider>]` / `show <provider>/<model>` / `refresh` — inspect and refresh the models.dev-backed model catalog.
- `hygge version` — print version, Go version, OS/arch.

## Configuration

User config lives at `~/.config/hygge/config.toml` (or
`$XDG_CONFIG_HOME/hygge/config.toml`).

Profiles live in `~/.config/hygge/profiles/<name>.toml` and are activated
with `hygge profile use <name>`. A profile can `extends = "other"` to
inherit from another profile.

A `.hygge/config.toml` file in the current directory (or any parent) is
picked up automatically as the highest-priority layer. Use this for
per-project model or permission overrides.

`hygge config explain` shows the resolved config along with the source
of every value: builtin default, user config, profile, or project file.

### Conventions

Hygge follows the vendor-neutral [`.agents`](https://agents.md) directory
convention for shared agent assets (skills, prompts, and any future
filesystem-discovered config) alongside its own `.hygge` directories.
Every feature that loads config from disk consults the same four layers,
in this order (lowest priority first; later layers override earlier ones
with the same name):

1. `~/.agents/<feature>/...` — vendor-neutral, per-user.
2. `~/.config/hygge/<feature>/...` — hygge-native, per-user.
3. `<project-root>/.agents/<feature>/...` — vendor-neutral, per-project (discovered by walking up from the current directory).
4. `<project-root>/.hygge/<feature>/...` — hygge-native, per-project (same walk-up).

Hygge-native paths override `.agents` paths so users can shadow a
shared asset with a hygge-specific tweak. Project paths override user
paths so per-repo conventions win.

The walk-up for project layers stops at the first `.git` directory at
or above the current level (the conventional "this is the project
root" signal) and never climbs into `$HOME`.

### Skills

Skills are named markdown procedures the model can invoke at runtime.
Hygge supports two file layouts in any `skills/` directory under the
four layers above:

**Directory-style** (the `.agents` standard — recommended):

```
skills/
  refactor-handler/
    SKILL.md           # frontmatter + body
    scripts/           # optional auxiliary files
    reference/
```

**Flat-style** (simpler — one file per skill):

```
skills/
  refactor-handler.md
```

Either way the file carries a `name` and `description` in frontmatter
plus a free-form markdown body. `when_to_use` is optional — fold that
guidance into the description if you prefer the `.agents` convention.
Any additional keys (`version`, `allowed-tools`, etc.) are preserved in
`extras` and shown by `hygge skills show`. Multi-line values are
supported via YAML block scalars (`>` folded, `|` literal) and implicit
indented blocks.

```markdown
---
name: refactor-handler
description: >
  Refactor an HTTP handler to extract its core logic. Use when asked
  to split a long handler into testable pieces.
version: 1.0.0
---
# Procedure

1. Identify the handler function …
2. …
```

For directory-style skills, `Skill directory: <absolute path>` is
prepended to the body when the model loads it via the `skill` tool, so
the model can resolve relative paths to auxiliary scripts or
references.

The system prompt advertises every loaded skill's `name` and
`description` (and `when_to_use` when present); the full body is
fetched on demand via the built-in `skill` tool. Use `hygge skills
list` to see what is loaded, `hygge skills show <name>` to inspect one,
and `hygge skills doctor` to diagnose files that failed to parse.

### Sub-agents and the `task` tool

The `task` tool dispatches a focused mission to a sub-agent that runs
in isolation and returns a single summary message. Use it for
self-contained work — codebase searches, documentation lookups,
focused refactors — that would otherwise pollute the main context.

Hygge ships a built-in **general** sub-agent type with access to all
built-in tools (except `task` itself). Add more types by declaring
them in a `subagents.toml` under any of the four discovery layers
(`~/.agents/`, `~/.config/hygge/`, `<project>/.agents/`,
`<project>/.hygge/`):

```toml
# subagents.toml
[subagents.search]
description = "Codebase recon. Reads files, runs grep/glob, returns a summary with file:line citations."
prompt = """
You are a search sub-agent.  Your job is to find specific facts in
this codebase and return a concise summary with file:line citations.
Don't read more than necessary.  Return one final message.
"""
tools = ["read", "grep", "glob", "bash"]

[subagents.librarian]
description = "External documentation lookup."
prompt = "..."
tools = ["read", "bash"]
model = "anthropic/claude-haiku-4-5"   # optional: pin this type to a specific provider+model
```

#### Per-type model overrides

A sub-agent type may pin its own provider and model via the `model`
field. The value must be of the form `<provider>/<model-id>`, e.g.
`anthropic/claude-haiku-4-5`, `openai/gpt-4o-mini`, or
`openrouter/anthropic/claude-haiku-4-5`. The provider name must match
a registered provider (`anthropic`, `openai`, `openrouter`, ...); the
model id is passed through unchanged.

When the override targets the same provider as the parent's config,
hygge reuses the parent's already-authenticated provider instance.
When it targets a different provider, hygge constructs that provider
on demand using the same credential precedence as the parent
(`model.options.api_key` → `$<PROVIDER>_API_KEY` → `auth.json`), so
make sure the relevant key is configured before launching a sub-agent
that needs it.

Malformed model strings (anything not matching
`<provider>/<model-id>`) are logged with a warning at load time and
the override is dropped — the type still loads and falls back to the
parent's model. Providers that fail to construct (missing
credentials, unknown name) surface as task-tool errors so the model
sees a clear diagnostic.

Sub-agents NEVER see the `task` tool, even when their TOML config
asks for it: the recursion guard strips it from every sub-agent's
tool set. Each side-effecting tool the sub-agent invokes still goes
through the same permission engine as the parent, so you keep
fine-grained control after the umbrella "launch the sub-agent" prompt.

Sub-agent runs are persisted as their own sessions with `kind =
subagent` and a `parent_id` link back to the dispatching session.
They are auditable and replayable via the standard `hygge sessions`
plumbing. Tokens and cost accumulate on the sub-session row; Stage A
does not aggregate them into the parent's running totals — the `task`
tool surfaces them in its metadata so the model (and you) can see
what the run cost.

Use `hygge subagents list` to see the registered types and `hygge
subagents show <name>` to inspect a single type's system prompt and
tool allowlist.

#### TUI experience

When you invoke the `task` tool from inside the TUI, the sub-agent's
live transcript appears as a nested collapsible block underneath the
`task` tool call line in the message list.

- **Collapsed by default.** The header reads
  `▸ task[<type>] · <provider>/<model> · <state> · <elapsed> · <tokens> · $<cost>`
  followed by the description quote. The model label always shows the
  resolved provider+model — handy when a per-type override pins
  something different from the parent's model.
- **Toggle with `Ctrl+T`.** Expands or collapses the most recently
  started sub-agent block. Hygge does not yet have cursor-based
  message navigation; when it lands (v0.3), `Ctrl+T` will become a
  per-block toggle keyed off the cursor.
- **Live updates.** Streaming assistant text, tool calls, and tool
  results appear in real time inside the expanded block, indented
  with a `│` gutter so the nesting reads cleanly. Running cost and
  token totals update on the header as the sub-agent works.
- **Final state.** On completion the chevron flips, the header shows
  `done` with the final cost/usage, and the elapsed-time tick stops.
  When the sub-agent hit its iteration cap the header reads
  `failed (iteration limit)` in the error colour.

Sub-agent events are routed by session id, so blocks from a previous
foreground session never leak into the current view.

### Project context (AGENTS.md / CLAUDE.md)

Project-context files describe house rules, terminology, and
conventions the model should always have in mind. Unlike skills,
their contents are appended to the system prompt unconditionally on
every turn. Hygge treats `AGENTS.md` and `CLAUDE.md` (and
`CLAUDE.local.md`) as first-class equivalents so the same repo can
be used by either tool without duplication.

Hygge discovers context files across eight project-root layers, in
precedence order (lowest first; all that exist are concatenated, none
override each other):

1. `~/.agents/AGENTS.md` — vendor-neutral, per-user.
2. `~/.config/hygge/AGENTS.md` — hygge-native, per-user.
3. `~/.claude/CLAUDE.md` — CLAUDE-format compat, per-user.
4. `<project-root>/.agents/AGENTS.md` — vendor-neutral, per-project.
5. `<project-root>/AGENTS.md` — conventional project root file.
6. `<project-root>/AGENTS.local.md` — local AGENTS override (ignored from VCS).
7. `<project-root>/CLAUDE.md` — CLAUDE-format compat at project root.
8. `<project-root>/CLAUDE.local.md` — local CLAUDE override (ignored from VCS).

The project-root walk-up stops at the first directory containing
`AGENTS.md`, `CLAUDE.md`, `.git`, `.agents/`, or `.hygge/`, and never
climbs into `$HOME`.

**Subdirectory context (lazy):** subdirectory `AGENTS.md`, `CLAUDE.md`,
and `CLAUDE.local.md` files are loaded automatically when the agent's
tools (`read`, `write`, `edit`, `grep`, `glob`) touch a path inside that
directory. The loader walks upward from each touched path toward the
project root, picking up any context files in directories it has not
already visited this session, and injects them into the system prompt
of the NEXT provider turn only — they are never persisted into session
history. Directories named `node_modules`, `vendor`, `.venv`,
`__pycache__`, `dist`, `target`, `bin`, `build`, `.git`, `.agents`, and
`.hygge` are walked through transparently but never contribute context.
A session-wide cap (`MaxLazyContextFiles = 50`, `MaxLazyContextBytes =
256 KiB`) bounds how much extra material can ride along; once a cap
fires the loader disables itself for the rest of the session and logs
a `slog.Warn`.

Use:

- `hygge context list` — tabular summary (source layer, path, bytes, lines).
- `hygge context show` — print every loaded file, in precedence order, exactly as the system prompt sees it.
- `hygge context paths` — one absolute path per line for shell pipelines.

### Reasoning models

Hygge exposes a single knob for reasoning / extended-thinking models
that both Anthropic and OpenAI-family adapters translate into the
appropriate wire format. The session-wide default lives under
`[model]` in any config layer:

```toml
[model]
provider = "anthropic"
name = "claude-sonnet-4-5"
reasoning = "medium"            # "" / "off" / "low" / "medium" / "high"
# reasoning_budget = 12000      # optional explicit Anthropic token budget
```

Override per run with the `--reasoning` flag:

```sh
hygge --reasoning high -p "Plan the migration from X to Y."
```

Adapter behaviour:

- **Anthropic** — `claude-sonnet-4-5` and `claude-opus-4-5` support
  extended thinking. `low` / `medium` / `high` map to a budget of
  `2048` / `8192` / `16384` tokens unless `reasoning_budget` pins an
  explicit value. The `max_tokens` default is raised so it sits at
  least `budget + 1024` above the thinking budget. Models without
  extended-thinking support silently ignore the field.
- **OpenAI o-series** — `o1`, `o1-mini`, `o3`, `o3-mini`, `o4-mini`,
  and any `reasoning-*` model are auto-detected by name prefix. The
  request body switches to `max_completion_tokens`, drops
  `temperature` entirely, and sends `reasoning_effort` when the knob
  is `low` / `medium` / `high`. With `--reasoning off` on a
  reasoning-class model, the field is omitted and the server picks
  its default. Non-reasoning models silently ignore the knob.

Reasoning tokens are billed (OpenAI counts them under
`completion_tokens`) and propagated through `provider.Usage` and the
running cost totals. Reasoning-summary chunks streamed by the o-series
arrive as the same `thinking_delta` events the TUI already renders, so
no extra UI is needed.

The reasoning-model detection list is currently a hardcoded prefix
matcher in `internal/provider/openaicompat`. When the models-catalog
work lands the detection will move to a capability lookup; see
`STATUS.md`.

### Provider credentials

- `hygge provider auth [name]` — save an API key for a provider. Reads
  a single line from stdin when piped, or prompts interactively
  (hidden input) when run from a TTY.
- `hygge provider list` — show stored credentials with masked keys.
- `hygge provider remove <name>` — delete a stored credential
  (`-f` / `--no-confirm` skips the prompt).

Credential precedence at startup:

1. `model.options.api_key` in config (explicit override).
2. The canonical `$<PROVIDER>_API_KEY` env var (e.g.
   `ANTHROPIC_API_KEY`).
3. The auth store entry for the configured provider.

### MCP servers

Hygge connects to [MCP](https://modelcontextprotocol.io) (Model Context
Protocol) servers and exposes every tool they advertise to the agent,
prefixed with the server's name (e.g. `github_create_issue`).

Configure servers in `mcp.toml`, discovered through the same four-layer
`.agents` search path as skills and AGENTS.md (later overrides
earlier):

1. `~/.agents/mcp.toml`
2. `~/.config/hygge/mcp.toml`
3. `<project>/.agents/mcp.toml`  (walk-up; stops at `.git` or `$HOME`)
4. `<project>/.hygge/mcp.toml`   (walk-up; stops at `.git` or `$HOME`)

Example:

```toml
[[servers]]
name = "github"
transport = "stdio"
command = "mcp-server-github"
args = ["--token", "$GITHUB_TOKEN"]
env = { GITHUB_API_URL = "https://api.github.com" }

[[servers]]
name = "postgres"
transport = "stdio"
command = "mcp-server-postgres"
args = ["postgres://localhost/mydb"]
permission_category = "network"   # gate via the network category
```

`$VAR` / `${VAR}` references in `command`, `args`, `env`, and `dir` are
interpolated at load time so secrets can come from the environment.

Commands:

- `hygge mcp list` — show configured servers and their boot-time
  status (ready / failed / disabled).
- `hygge mcp ping <name>` — spawn a temporary client and verify the
  server responds (init + ping latency).
- `hygge mcp tools [server]` — list every tool advertised by the
  connected servers, optionally filtered to one.
- `hygge mcp doctor` — walk every discovered `mcp.toml`, validate it,
  and report issues.

Permission gating: MCP tool calls go through the new `mcp` category
(default: `ask`). Override per-server with `permission_category` in
`mcp.toml` if a particular server is better gated as `shell`,
`network`, `file.read`, or `file.write`.

Only the `stdio` transport is supported in v0.2. SSE and Streamable
HTTP transports are deferred to v0.3.

## Slash commands

Anything typed into the TUI input that starts with `/` is interpreted
as a slash command instead of a chat message. An inline palette
opens above the input, filtered live by what you've typed; use
Up/Down to highlight, Tab to complete, Enter to run, Esc to
dismiss.

The built-in command set:

| Command     | Effect                                           |
|-------------|--------------------------------------------------|
| `/help`     | List every registered command (built-in + user). `/help <name>` shows full detail. |
| `/clear`    | Clear the rendered session history.              |
| `/compact`  | Trigger a session compaction now.                |
| `/cost`     | Show running session cost.                       |
| `/sessions` | Open the sessions picker (rich UI lands in T1.2). |
| `/fork`     | Fork the session at a message id (T1.2 will wire). |
| `/model`    | No-arg: show current model. With `<provider>/<id>`: switch. |
| `/reason`   | No-arg: show current reasoning depth. With `off|low|medium|high`: switch. |
| `/version`  | Show the hygge version.                          |

`/q` and `/quit` are intentionally NOT claimed — the existing
keybinds (Ctrl+C) handle exit.

### Defining your own commands

Drop a `commands.toml` at one of the standard four discovery layers
(`~/.agents`, `~/.config/hygge`, `<project>/.agents`,
`<project>/.hygge`). Later layers override earlier same-named
entries; user TOML can even override the built-ins if you want a
custom `/compact` prompt.

```toml
[commands.review]
description = "Review code for issues"
prompt = """
Review the following code for bugs, security issues, and
improvements:

{{code}}
"""
args = [
  { name = "code", description = "code to review", required = true },
]

[commands.explain]
description = "Explain a concept"
prompt = "Explain {{topic}} in plain language."
args = [
  { name = "topic", description = "what to explain", required = true },
]

[commands.brief]
description = "Quick TLDR of a file"
prompt = "Summarise this file in 3 bullet points: {{tail}}"
```

Template rules:

- `{{name}}` substitutes the matching arg by name.
- `{{tail}}` is reserved and captures every remaining word after the
  last named arg. With no `args` declared, `{{tail}}` receives the
  entire input.
- Whitespace splits arguments; wrap a value in double quotes to
  include spaces (`/review "with spaces" rest`). Backslash escapes
  the next character inside quotes.
- A missing required arg produces a friendly notice and does not
  send the message.
- Unknown `{{names}}` are left literal at runtime (a load-time
  warning surfaces in `~/.local/state/hygge/hygge.log`).

Command names must match `[a-z][a-z0-9_-]*` and are case-sensitive.

Inspect what's loaded with `hygge commands list` (`--source builtin`
/ `user` / `project` to filter) and `hygge commands show <name>` for
the full detail.

## Model catalog

Hygge's model metadata — pricing, capabilities, context-window limits —
is sourced from [models.dev](https://models.dev) through a unified
catalog in `internal/catalog`. The catalog is the single source of
truth: cost lookups, provider model lists (`ListModels`), and reasoning
capability detection all flow through it.

Three layers in the resolution cascade, in order:

1. **Disk snapshot** at `$XDG_STATE_HOME/hygge/catalog.json` —
   refreshed by `hygge catalog refresh`.
2. **Embedded snapshot** compiled into the binary at build time.
   ~270 KiB; covers the major providers (anthropic, openai,
   openrouter, google, mistral, groq, deepseek, xai, …) and ~860
   models.  Bedrock fallback so hygge always has a usable catalog,
   even with no network and no on-disk cache.
3. **Background refresh** kicked off on startup when the disk snapshot
   is older than 7 days.  Runs in a goroutine; never blocks startup;
   logs to `slog.Debug` on success and `slog.Warn` on failure.

Inspect or refresh the catalog from the CLI:

```sh
hygge catalog list                              # per-provider summary
hygge catalog list anthropic                    # per-model table
hygge catalog show anthropic/claude-sonnet-4-5  # all metadata
hygge catalog refresh                           # pull a fresh snapshot
```

The same catalog drives reasoning-class detection in the OpenAI-compat
adapter: when models.dev advertises `reasoning: true` for a model, the
adapter switches to the `max_completion_tokens` / `reasoning_effort`
request shape automatically.  A hardcoded name-prefix matcher (o1-*,
o3-*, o4-*, reasoning-*) remains as a fallback for brand-new ids the
local catalog hasn't been refreshed for.

## Development

```sh
mise run test          # tests with -race
mise run lint          # golangci-lint
mise run precommit     # lint + test + build — run before every commit
```

See `SMOKE.md` for the manual ship-gate checklist.

## License

MIT — see [LICENSE](LICENSE).

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
- Cost tracking via the [models.dev](https://models.dev) catalog with a
  hard-coded fallback table for offline use.

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
- `hygge context list` / `show` / `paths` — inspect the project-context files (`AGENTS.md` / `CLAUDE.md`) contributing to the system prompt.
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

### Project context (AGENTS.md / CLAUDE.md)

Project-context files describe house rules, terminology, and
conventions the model should always have in mind. Unlike skills,
their contents are appended to the system prompt unconditionally on
every turn. Hygge treats `AGENTS.md` and `CLAUDE.md` (and
`CLAUDE.local.md`) as first-class equivalents so the same repo can
be used by either tool without duplication.

Hygge discovers context files across eight layers, in precedence
order (lowest first; all that exist are concatenated, none override
each other):

1. `~/.agents/AGENTS.md` — vendor-neutral, per-user.
2. `~/.config/hygge/AGENTS.md` — hygge-native, per-user.
3. `~/.claude/CLAUDE.md` — CLAUDE-format compat, per-user.
4. `<project-root>/.agents/AGENTS.md` — vendor-neutral, per-project.
5. `<project-root>/AGENTS.md` — conventional project root file.
6. `<project-root>/CLAUDE.md` — CLAUDE-format compat at project root.
7. `<project-root>/CLAUDE.local.md` — local CLAUDE override (ignored from VCS).
8. `<project-root>/**/{AGENTS.md,CLAUDE.md,CLAUDE.local.md}` —
   recursive descent into subdirectories.

The project-root walk-up stops at the first directory containing
`AGENTS.md`, `CLAUDE.md`, `.git`, `.agents/`, or `.hygge/`, and never
climbs into `$HOME`.

The recursive descent (layer 8) skips common dependency / build
directories — `.git`, `.agents`, `.hygge`, `node_modules`, `vendor`,
`.venv`, `__pycache__`, `dist`, `target`, `bin`, `build` — and is
bounded to **50 files** and **256 KB** total to keep the system
prompt from blowing up in a misconfigured workspace. The first file
or byte over each cap is logged via `slog.Warn`.

Use:

- `hygge context list` — tabular summary (source layer, path, bytes, lines).
- `hygge context show` — print every loaded file, in precedence order, exactly as the system prompt sees it.
- `hygge context paths` — one absolute path per line for shell pipelines.

### Provider credentials

API keys live separately from the human-edited config so the config can
be committed to a dotfiles repo without leaking secrets. They are
stored at `$XDG_STATE_HOME/hygge/auth.json` (mode `0600`,
`~/.local/state/hygge/auth.json` by default) and managed via:

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

## Development

```sh
mise run test          # tests with -race
mise run lint          # golangci-lint
mise run precommit     # lint + test + build — run before every commit
```

See `SMOKE.md` for the manual ship-gate checklist.

## License

MIT — see [LICENSE](LICENSE).

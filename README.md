# Hygge

> **Note:** Hygge is no longer under active development or in active use by me.
> It was a fun experiment, but it's hard to compete with agents with company backing, and something like [oh-my-pi](https://github.com/can1357/oh-my-pi) fits my usecases really well

Hygge (*H(Y)OO-gə*) is a terminal AI coding assistant built in Go. It provides a local-first
Bubble Tea TUI, streaming model responses, persistent SQLite sessions, tools,
plugins, MCP integrations, hooks, subagents, slash commands, and themeable chat
UI chrome.

<img width="1758" height="1052" alt="image" src="https://github.com/user-attachments/assets/ca77b3e4-1e4c-47f3-91c6-7986032b6ef7" />

## Install

Requires Go 1.26.3 or newer.

```sh
go install github.com/cfbender/hygge@latest
```

With mise:

```sh
mise install github:cfbender/hygge@latest
```

or for the go backend:
```sh
mise install go:github.com/cfbender/hygge@latest
```

For a pinned release:

```sh
go install github.com/cfbender/hygge@v0.5.0
mise install go:github.com/cfbender/hygge@v0.5.0
```

## Quick start

Launch Hygge to start the onboarding flow:

```sh
hygge
```

Onboarding creates your first **mode**. A mode is a reusable agent setup: a
name, provider/model choice, and system instructions for a kind of work. Start
with the default `General` mode, then add more focused modes when you want
different behavior without rewriting prompts every session.

To set up a starter mode layout from the CLI, authenticate at least one provider,
then run `hygge init` and pick a style:

```sh
hygge provider auth 
hygge init
```

You can also choose a style directly:

```sh
hygge init general   # one general engineering mode
hygge init amp       # smart/rush/deep modes plus specialist subagents
hygge init opencode  # build/plan modes plus general/explore/scout subagents
```

`hygge init` writes editable prompt files under
`$XDG_CONFIG_HOME/hygge/prompts/<style>/`, updates your user `config.toml`, and
writes `subagents.toml` for styles that include subagents. Manual configuration
via `hygge provider auth` or environment variables such as `ANTHROPIC_API_KEY`
also work.

Or build from source:

```sh
mise install
mise run build
./bin/hygge
```

## What Hygge does

- Streams assistant responses into a terminal chat UI.
- Persists sessions, messages, compaction markers, todos, token usage, and cost
  in SQLite.
- Resolves provider/model metadata through the Catwalk-backed catalog with an
  embedded fallback for offline startup.
- Runs model tool calls through built-in tools, plugins, and MCP servers.
- Gates side effects with permissions, project config, hooks, and yolo-mode
  boundaries.
- Supports slash commands, subagents, skills, memories, prompt attachments,
  project context files, profiles, themes, model switching, reasoning settings,
  session forking, and session resume.

## Common commands

```sh
hygge                         # launch the TUI
hygge --continue              # resume the most recent session for this cwd
hygge --new                   # force a fresh session
hygge resume [id-prefix]      # resume a session
hygge sessions list           # list sessions
hygge sessions show <id>      # inspect a session
hygge sessions delete <id>    # soft-delete a session
hygge version                 # print version and platform info
```

Configuration and discovery:

```sh
hygge config explain [key]
hygge profile list
hygge profile show [name]
hygge profile use <name>
hygge init [general|amp|opencode]
hygge provider auth [name]
hygge provider list
hygge provider remove <name>
hygge catalog list [provider]
hygge catalog show <provider>/<model>
hygge catalog refresh
hygge context list
hygge context paths
hygge context show
hygge theme show
```

Runtime extensions:

```sh
hygge skills list
hygge skills show <name>
hygge skills doctor
hygge subagents list
hygge subagents show <name>
hygge commands list
hygge commands show <name>
hygge hooks list
hygge hooks show <name>
hygge mcp list
hygge mcp tools [server]
hygge mcp ping <server>
hygge mcp doctor
hygge plugins list
hygge plugins show <name>
hygge plugins install <source>
hygge plugins update [name]
hygge plugins remove <name>
hygge plugins types install
```

## Configuration

User config lives at (either filename is accepted; `hygge.toml` takes precedence over `config.toml` when both exist):

```text
~/.config/hygge/hygge.toml   (preferred when config.toml is already used by another tool)
~/.config/hygge/config.toml  (existing name, still supported)
```

or using a custom `$XDG_CONFIG_HOME`:

```text
$XDG_CONFIG_HOME/hygge/hygge.toml
$XDG_CONFIG_HOME/hygge/config.toml
```

Project config can live in `.hygge/hygge.toml` or `.hygge/config.toml` in the
current directory or any parent project directory.  Both are loaded when
present; `hygge.toml` values win over `config.toml` values within the same
directory.

Two additional files are loaded directly from the current working directory
(not from `.hygge/`) with the highest file-based precedence:

- `hygge.toml` — project-root overrides, committed to version control.
- `hygge.local.toml` — machine-local overrides, add to `.gitignore`; wins over everything except environment variables and CLI flags.

Profiles live in:

```text
~/.config/hygge/profiles/<name>.toml
```

Select a profile with:

```sh
hygge profile use work
```

Example config:

```toml
default_profile = "work"

[session]
resume_default = "continue" # "new" | "continue" | "ask"

[model]
provider = "anthropic"
name = "claude-sonnet-4-5"
reasoning = "medium" # "off" | "low" | "medium" | "high"

[catalog]
refresh_interval = "24h"
```

`hygge config explain` shows the resolved value and source layer for every
setting.

## Project context, skills, and subagents

Hygge loads project instructions from common agent files such as `AGENTS.md`,
`AGENTS.local.md`, `CLAUDE.md`, and `CLAUDE.local.md`. Root files are included in
the system prompt; subdirectory context files are loaded lazily when tools touch
paths under those directories.

Skills are named Markdown procedures discoverable from `skills/` directories
under user or project config layers. The assistant sees each skill's name and
description, then loads the full body with the `skill` tool when needed.

Subagents are configured in `subagents.toml` and invoked by the `task` tool for
focused work. Subagent sessions are persisted, auditable, can use per-type tool
allowlists and model overrides, and can be referenced from the prompt with
`@agent:<name>` mentions.

## Tools and permissions

Built-in tools include filesystem reads/searches, edits, shell commands, skills,
todos, memories, and subagent dispatch. Read-only tools can run in parallel;
side-effecting tools run serially and pass through permission checks.

Permissions can be granted once, for the session, always, or denied. Project and
user config can tune defaults, while hooks can add policy checks before or after
tool and message events.

Add a `.aiexclude` file at the project root to hard-block Hygge's filesystem
read and write tools from matching paths. Entries use gitignore-style patterns,
for example `secrets/`, `.env.local`, or `*.pem`. These denies apply before
normal permissions and yolo-mode, so approved sessions cannot read, write, or
edit excluded files.

## MCP, hooks, and plugins

MCP servers are configured through `mcp.toml` and exposed as namespaced tools.
Hygge supports stdio, SSE, and Streamable HTTP transports.

Hooks are external commands configured in `hooks.toml`. They can observe or gate
events such as `pre_tool`, `post_tool`, `pre_message`, and `post_message`.
`pre_message` hooks may return `system_prompt_append` to add one-turn model
context without rendering or persisting that context as user text.

Plugins are Lua modules installed from local paths or GitHub repositories. They
can register tools, hooks, slash commands, and subagent types through Hygge's Lua
API. For example, [quorum](https://github.com/cfbender/quorum) uses the plugin
system to add extra planning/review subagents that Hygge can fan out to from the
same session.

When developing a Lua plugin, initialize editor support with:

```sh
hygge plugins dev init
```

For an existing plugin, run `hygge plugins types install` to add Hygge's LuaLS
type definitions and avoid editor diagnostics for the `hygge` plugin API.

## TUI basics

- Type a prompt and press `Enter` to send.
- Use `/` to open slash command completion.
- Use `@` to mention repository files or `@agent:<name>` subagents. Mentioned
  files are attached to the next prompt as context.
- Use `/attach` to attach files manually and `/attachments clear` to clear the
  pending attachment set.
- Use `/compact` to summarize older session history.
- Use `/model` and `/reason` to inspect or change runtime model settings;
  `Ctrl+T` cycles reasoning levels.
- Use `/memory`, `/remember`, and `/forget` to inspect, save, or delete durable
  facts.
- Use `/fork` to branch a session from the latest user message or a specific
  message ID.
- Press `Ctrl+E` to edit the current prompt in `$VISUAL` or `$EDITOR`.
- Use `Esc` to dismiss popovers or leave a subagent view. While a turn is busy,
  `Esc` clears queued prompts and a quick double-`Esc` interrupts the run.
- Use the sessions UI to resume, switch, or inspect prior sessions.
- Subagent transcripts render as nested, collapsible chat blocks; `Ctrl+G`
  follows into the latest subagent transcript.

## Development

```sh
mise install
mise run build        # compile ./bin/hygge
mise run test         # go test ./... -race -count=1
mise run lint         # golangci-lint run
mise run flake-update # update flake.lock via Docker-provided Nix
mise run precommit    # fix + flake-update + lint + test + build
```

Release helper:

```sh
mise run bump -- patch
mise run bump -- minor
mise run bump -- major
```

The bump task increments `cmd/hygge/cli/cli.go`, commits, creates an annotated
tag, and pushes the commit and tag. Use it only when intentionally cutting a
release.

See [docs/releasing.md](docs/releasing.md) for the full release process,
changelog generation with [git-cliff](https://git-cliff.org), and required
GitHub repository settings (squash-only merges, PR title enforcement).

## License

MIT — see [LICENSE](LICENSE).

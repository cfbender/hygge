# Hygge

Hygge (*H(Y)OO-gə*) is a terminal AI coding assistant built in Go. It provides a local-first
Bubble Tea TUI, streaming model responses, persistent SQLite sessions, tools,
plugins, MCP integrations, hooks, subagents, slash commands, and themeable chat
UI chrome.

<img width="1772" height="1046" alt="image" src="https://github.com/user-attachments/assets/3c63744c-f9a9-47ab-9e94-6a239bf3442c" />

## Install

Requires Go 1.26 or newer.

```sh
go install github.com/cfbender/hygge@latest
```

With mise:

```sh
mise install go:github.com/cfbender/hygge@latest
```

For a pinned release:

```sh
go install github.com/cfbender/hygge@v0.3.3
mise install go:github.com/cfbender/hygge@v0.3.3
```

## Quick start

Set an API key for your provider, then launch Hygge from a project directory:

```sh
export ANTHROPIC_API_KEY=...
hygge
```

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
- Supports slash commands, subagents, skills, project context files, profiles,
  themes, model switching, reasoning settings, and session resume.

## Common commands

```sh
hygge                         # launch the TUI
hygge --continue              # resume the most recent session for this cwd
hygge --new                   # force a fresh session
hygge resume [id-prefix]      # resume a session
hygge sessions list           # list sessions
hygge sessions delete <id>    # soft-delete a session
hygge version                 # print version and platform info
```

Configuration and discovery:

```sh
hygge config explain [key]
hygge profile list
hygge profile use <name>
hygge provider auth [name]
hygge provider list
hygge catalog list [provider]
hygge catalog show <provider>/<model>
hygge catalog refresh
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
hygge mcp doctor
hygge plugins list
hygge plugins install <source>
hygge plugins update [name]
hygge plugins remove <name>
```

## Configuration

User config lives at:

```text
~/.config/hygge/config.toml
```

or:

```text
$XDG_CONFIG_HOME/hygge/config.toml
```

Project config can live in `.hygge/config.toml` in the current directory or any
parent project directory.

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
reasoning = "medium" # "" | "off" | "low" | "medium" | "high"

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
focused work. Subagent sessions are persisted, auditable, and can use per-type
tool allowlists and model overrides.

## Tools and permissions

Built-in tools include filesystem reads/searches, edits, shell commands, skills,
todos, and subagent dispatch. Read-only tools can run in parallel; side-effecting
tools run serially and pass through permission checks.

Permissions can be granted once, for the session, always, or denied. Project and
user config can tune defaults, while hooks can add policy checks before or after
tool and message events.

## MCP, hooks, and plugins

MCP servers are configured through `mcp.toml` and exposed as namespaced tools.
Hygge supports stdio, SSE, and Streamable HTTP transports.

Hooks are external commands configured in `hooks.toml`. They can observe or gate
events such as `pre_tool`, `post_tool`, `pre_message`, and `post_message`.

Plugins are Lua modules installed from local paths or GitHub repositories. They
can register tools, hooks, slash commands, and subagent types through Hygge's Lua
API.

## TUI basics

- Type a prompt and press `Enter` to send.
- Use `/` to open slash command completion.
- Use `/compact` to summarize older session history.
- Use `/model` and `/reason` to inspect or change runtime model settings.
- Use the sessions UI to resume, switch, or inspect prior sessions.
- Subagent transcripts render as nested, collapsible chat blocks.

## Development

```sh
mise install
mise run build       # compile ./bin/hygge
mise run test        # go test ./... -race -count=1
mise run lint        # golangci-lint run
mise run precommit   # fix + lint + test + build
```

Release helper:

```sh
mise run bump -- patch
mise run bump -- minor
mise run bump -- major
```

The bump task increments `cmd/hygge/cli/cli.go`, commits, creates an annotated
tag, and pushes the commit and tag.

## License

MIT — see [LICENSE](LICENSE).

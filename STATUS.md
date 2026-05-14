# Hygge — deferred work / status notes

## Next: lazy AGENTS.md loading from tool-touched directories

User direction (m0176, refined in m0192): the project-root AGENTS.md /
AGENTS.local.md / CLAUDE.md / CLAUDE.local.md files are loaded at
startup. Subdirectory AGENTS.md / CLAUDE.md files are NOT loaded at
startup — that approach over-loads the system prompt with files the
model may never need. Instead, subdirectory context is surfaced lazily,
only when the agent actually touches that directory via a tool call.

### Algorithm

- Maintain a per-session `seenContextDirs map[string]struct{}` seeded
  with every directory whose AGENTS.md / CLAUDE.md was already loaded
  by `agentsmd.Load` at bootstrap (so root files are never re-injected).
- After every tool execution, collect the directory portion of every
  touched path (the `path` argument to `read`/`write`/`edit`, the cwd
  of `bash`, the search root of `grep`/`glob`, etc.).
- For each touched directory, walk UP toward the project root looking
  for `AGENTS.md`, `CLAUDE.md`, and `CLAUDE.local.md` in directories
  not yet in `seenContextDirs`. Skip directories in `LazyExcludeDirs`
  (see `internal/agentsmd/agentsmd.go`). Stop at the project root.
- Mark every visited directory as seen (whether it contained a context
  file or not) so we don't re-scan it.
- For each newly-found file, inject the content as a transient
  system-style note (or `tool_result`-shaped message addressed to the
  agent) in the next provider turn rather than mutating the running
  system prompt.
- Honour the package-level caps:
  - `agentsmd.MaxLazyContextFiles` (50) — total files injected over
    the session.
  - `agentsmd.MaxLazyContextBytes` (256 KiB) — total bytes injected
    over the session. Log a `slog.Warn` and stop injecting once a
    cap is hit.
- Re-use `agentsmd.Block` and `agentsmd.SourceProjectSubdir` for the
  injected entries so downstream code (system-prompt rendering, the
  `hygge context` CLI) can describe them uniformly.

### Touch points

- `internal/agent/loop.go` (or a new `internal/agent/context.go`):
  hook the lazy walk into the post-tool dispatch path.
- `cmd/hygge/cli/common.go`: light wiring so the agent loop knows the
  project root and the bootstrap `seenContextDirs` seed.
- Tests: per-tool-call injection, cap behaviour, exclude-dir behaviour,
  no-double-injection across turns.

Estimated ~200-300 LOC + tests.

### Why not load recursively at startup?

A recursive walk at startup loads every AGENTS.md in the tree before
the model has expressed interest in any of them, inflating the system
prompt and tying token budget to repository shape rather than the
current task. Per-tool-call loading bounds context to the directories
the agent actually visits in the current session.

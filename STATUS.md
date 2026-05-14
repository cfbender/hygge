# Hygge — deferred work / status notes

## Deferred: per-tool-call AGENTS.md auto-load

User intent (m0176): "eventually we also want the loop to auto-load AGENTS.md
in directories it is investigating/changing as well"

Currently, AGENTS.md / CLAUDE.md files are loaded ONCE at agent bootstrap by
walking the project root. When the model uses tools like `read`, `write`,
`edit`, `grep`, `glob`, or `bash` on paths outside the directories scanned at
startup (e.g. a path discovered mid-conversation, or under a freshly-cloned
submodule), any AGENTS.md sitting next to those paths is not surfaced.

Design sketch for the future:

- Add a per-session `seenContextDirs map[string]struct{}` to track which
  directories have had their AGENTS.md considered.
- After every tool execution, inspect the touched paths and walk from each
  parent up to the project root looking for AGENTS.md / CLAUDE.md files in
  directories not yet seen.
- New entries are injected as a transient system-style note in the next
  provider turn (or a `tool_result`-shaped message addressed to the agent)
  rather than mutating the running system prompt.
- Honour the same caps and exclude dirs as the bootstrap loader.

Touches: `internal/agent/loop.go`, `internal/agent/cost.go` (or a new
`internal/agent/context.go`), and `cmd/hygge/cli/common.go`. Estimated
~200-300 lines + tests.

Out of scope for the current task; user explicitly deferred to a follow-up.

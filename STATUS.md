# Hygge — deferred work / status notes

## Shipped

- **Sub-agents Stage A: `task` tool + runtime + TOML registry.**
  The `task` tool dispatches isolated missions to a sub-agent that runs
  to completion and returns a single summary message. A built-in
  `general` type ships by default; more can be declared via
  `subagents.toml` at the standard four discovery layers. Sub-agent
  runs are persisted as sessions with `kind = subagent` and a
  `parent_id` link to the dispatching session, so the conversation is
  auditable. The `task` tool is registered ONLY in the orchestrator's
  tool set; sub-agents NEVER see it (recursion guard, tested).
  Permission decisions still flow through the parent's engine, so each
  side-effecting tool the sub-agent invokes is gated individually.
  See `internal/subagent/`, `internal/tool/task.go`, the
  `0002_subagent_kind` migration, and `hygge subagents list`.

- **Lazy per-tool-call AGENTS.md / CLAUDE.md loader (v0.2).**
  Subdirectory context files are now discovered and injected on demand
  when the agent's tools touch a directory containing one. Blocks ride
  along in the NEXT provider turn's system prompt only — they are
  never persisted into session history. Bounded by
  `MaxLazyContextFiles` (50) and `MaxLazyContextBytes` (256 KiB) per
  session. See `internal/agentsmd/lazy.go` and
  `internal/agent/touched.go`.

## Follow-ups

- **Sub-agents Stage B: per-type model override.** `Type.Model` is
  parsed from `subagents.toml` and exposed via `hygge subagents show`,
  but the runtime still inherits the parent's model.
  `internal/subagent/runtime.go` already resolves model name through
  `RunInput.ModelName`; Stage B replaces the parent's value with the
  type's override when set, including a per-call provider lookup if
  the override changes provider.
- **Sub-agents Stage C: live nested transcript in the TUI.** Stage A
  already emits `bus.SubagentStarted` / `bus.SubagentCompleted` and
  the sub-agent's normal streaming events are tagged with the sub-
  session id (so the parent's TUI naturally filters them). Stage C
  subscribes to those events and renders an inline nested transcript
  while the sub-agent runs. Sub-session costs also need to roll up
  into the parent's totals at that point.
- Bash `cwd` argument: the bash tool currently has no explicit
  working-directory argument, so `touchedPaths` returns nil for it.
  When bash grows a `cwd` field, wire it into `touchedPaths` so
  `cd subdir && cmd` invocations surface subdir context.
- Walk-up logic exists in three places now (`agentsmd.findProjectRoot`,
  `cli.discoverProjectRoot`, `agentsmd.LazyTracker.walkUp`). When a
  fourth use case appears, consolidate into a shared helper.

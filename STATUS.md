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

- **Sub-agents Stage B: per-type model override.** A sub-agent type
  can now pin a specific provider+model via `model = "<provider>/<
  model-id>"` in `subagents.toml`. The runtime resolves the override
  through a `ProviderResolver` injected into `RunnerOptions`; the CLI
  bootstrap wires a resolver that reuses the parent's provider when
  the providerName matches and constructs new providers on demand
  otherwise, caching one instance per provider name and sharing the
  parent's credential precedence (`model.options.api_key` →
  `$<PROVIDER>_API_KEY` → `auth.json`). Malformed model strings are
  logged and dropped at load time; resolver errors at runtime surface
  as task-tool `IsError` results. See
  `internal/subagent/resolver.go`, `Runner.Run`, and
  `cmd/hygge/cli/common.go`'s `buildProviderResolver` /
  `buildProviderFor`.

- **Sub-agents Stage C: live nested transcript in the TUI.** The TUI
  now renders an inline collapsible block beneath every `task` tool
  call. Headers show `task[<type>] · <provider>/<model> · <state> ·
  <elapsed> · <tokens> · $<cost>`; expanding (`Ctrl+T` toggles the
  latest block) reveals the streamed sub-agent transcript with a `│`
  gutter. Multiple sub-agents in the same session render independently;
  events from sub-sessions whose root ancestor is not the foreground
  session are filtered out. `bus.SubagentStarted` gained `Model` (in
  `<provider>/<model-id>` form) and `ParentMessageID` (the parent's
  task tool_use_id) so the block header can label the sub-agent and
  the message list can anchor the block under the right row. With
  Stage C in, the sub-agent feature is complete for v0.2. See
  `internal/ui/components/subagent_block.go`, `internal/ui/app.go`'s
  `onSubagentStarted` / `onSubagentCompleted` and friends, plus
  `internal/bus/events.go`.

## Sub-agent follow-ups (v0.3 backlog)

- **Foreground-switch into a sub-session.** Today the nested block is
  read-only and the user cannot focus a sub-session as the primary
  view. A future keybind (e.g. `Ctrl+G`) could "follow" a sub-agent —
  swap the foreground id and surface the sub-session's full
  transcript in the main message list with a breadcrumb back to the
  parent.
- **Sub-session cost roll-up.** Stage C surfaces per-sub-agent cost
  inside the nested block, but the parent footer still excludes
  sub-agent dollars. When `SubagentCompleted` lands, the parent's
  `costDollars` should accumulate the sub-agent's final `CostUSD` so
  the footer reflects total spend across the dispatch tree.
- **Cursor-based message navigation.** A cursor would replace the
  "toggle the latest block" affordance with a per-block toggle keyed
  off the selected message. Required before multi-level recursion is
  practical to view.
- **Multi-level nesting.** The runtime currently caps recursion at
  depth 1 (`task` is stripped from every sub-agent's tool set). The
  TUI's `isInForegroundChain` already walks the chain so a future
  relaxation will Just Work for routing — but indentation in the
  block renderer assumes a single nesting level. Revisit once the
  recursion guard is lifted.
- **Tool-result expand on `space`.** The "press space to expand"
  hint on tool messages still has no handler; align that with the
  Stage C toggle key when the cursor lands.

- **Lazy per-tool-call AGENTS.md / CLAUDE.md loader (v0.2).**
  Subdirectory context files are now discovered and injected on demand
  when the agent's tools touch a directory containing one. Blocks ride
  along in the NEXT provider turn's system prompt only — they are
  never persisted into session history. Bounded by
  `MaxLazyContextFiles` (50) and `MaxLazyContextBytes` (256 KiB) per
  session. See `internal/agentsmd/lazy.go` and
  `internal/agent/touched.go`.

## Follow-ups

- Bash `cwd` argument: the bash tool currently has no explicit
  working-directory argument, so `touchedPaths` returns nil for it.
  When bash grows a `cwd` field, wire it into `touchedPaths` so
  `cd subdir && cmd` invocations surface subdir context.
- Walk-up logic exists in three places now (`agentsmd.findProjectRoot`,
  `cli.discoverProjectRoot`, `agentsmd.LazyTracker.walkUp`). When a
  fourth use case appears, consolidate into a shared helper.

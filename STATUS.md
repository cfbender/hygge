# Hygge — deferred work / status notes

## Shipped

- **Slash-commands framework (v0.3 T1.1).** Input that begins with `/`
  is routed through `internal/command`'s `Registry` instead of the
  send path. Built-in commands ship for `/help`, `/clear`,
  `/compact`, `/cost`, `/sessions`, `/fork`, `/model`, `/reason`,
  `/version`. Users extend the set with a `commands.toml` at the
  standard four discovery layers
  (`~/.agents`, `~/.config/hygge`, `<project>/.agents`,
  `<project>/.hygge`); each entry declares a `description`, a
  `prompt` template with `{{name}}` placeholders, and an optional
  `args` list. Commands return a closed-set `Outcome` (Message,
  Notice, ClearHistory, Compact, OpenModal, Updates) which the TUI
  dispatches — commands never mutate state directly. An inline
  command palette renders above the input on slash buffers with
  prefix-filtered matches, Up/Down navigation, Tab completion, Esc
  dismissal, and an overflow indicator past 8 rows. Also adds
  `hygge commands list [--source ...]` / `hygge commands show
  <name>` for inspection. T1.2 (session-management UI) and T2.3
  (compaction UX) are now unblocked. See `internal/command/`,
  `internal/ui/app_slash.go`, `internal/ui/components/command_palette.go`,
  and `cmd/hygge/cli/commands_cmd.go`.

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

- **Reasoning model support (v0.2).** A unified `provider.Reasoning`
  field on `provider.Request` is translated by both adapters into
  their wire-format equivalents. The Anthropic adapter populates the
  existing `thinking` block from the typed field and raises the
  default `max_tokens` to sit at least `budget + 1024` above the
  budget; the legacy `Options["thinking"]` passthrough is preserved
  for callers that need explicit shape control. The OpenAI-compat
  adapter detects reasoning-class models (`o1*`, `o3*`, `o4-*`,
  `reasoning-*`) by name prefix, switches the request body to
  `max_completion_tokens`, drops `temperature`, and sends
  `reasoning_effort` when the knob is `low/medium/high`. Reasoning
  tokens are parsed from
  `usage.completion_tokens_details.reasoning_tokens` and propagated
  through `provider.Usage`, `bus.CostUpdated`, and
  `bus.ContextUsageUpdated`. Reasoning-summary stream chunks are
  surfaced as `EventThinkingDelta` (via the `reasoningDelta` helper)
  so the existing TUI thinking renderer surfaces them with no UI
  changes. Exposed as `model.reasoning` / `model.reasoning_budget`
  in config and `--reasoning {off|low|medium|high}` on the CLI.

  Detection of reasoning-class models reads the new catalog's
  capability flag first (sourced from models.dev's `reasoning: true`
  field) and falls back to the hardcoded name-prefix matcher
  (`o1*`/`o3*`/`o4-*`/`reasoning-*`) only when the catalog has no
  entry for the id — so brand-new models are still handled correctly
  before the next `hygge catalog refresh`.

- **Models-catalog-driven model + capability metadata (v0.2).** A new
  `internal/catalog` package is the single source of truth for model
  metadata: pricing, capabilities (reasoning, tool-calling, vision,
  attachments), and context-window limits.  Sourced from models.dev
  with a disk-cached snapshot at
  `$XDG_STATE_HOME/hygge/catalog.json` and an embedded
  `snapshot.json` so hygge keeps working fully offline.  Background
  refresh fires on startup when the disk snapshot is older than 7
  days and never blocks startup.

  Downstream wiring:
  - `internal/cost.Catalog` is now a thin wrapper around
    `catalog.Catalog`; `cost.ErrModelNotPriced` is preserved as the
    not-found sentinel so the agent loop and subagent runner did not
    have to change.
  - Each provider package's `Models()` derives its list from the
    catalog when one is wired (via `SetCatalog`), with a small
    hardcoded fallback for tests and for the (defensive) case where
    even the embedded snapshot is unavailable.
  - `internal/provider/openaicompat`'s reasoning-model detection
    consults the catalog capability metadata first and falls back to
    the legacy `o*`-prefix matcher only when the catalog has no
    entry — handling brand-new ids without a refresh.

  CLI surface: `hygge catalog list [<provider>]` /
  `hygge catalog show <provider>/<model>` / `hygge catalog refresh`.

## Follow-ups

- **Catalog: openrouter alias resolution.** Openrouter ids are
  namespaced as `<vendor>/<model>` (e.g. `openai/o3-mini`).  Today
  the openaicompat reasoning lookup falls back to a bare-id lookup
  against the `openai` provider when the namespaced id misses under
  `openrouter`; a fuller solution would resolve aliases by walking
  the catalog's `openrouter` entry and matching its embedded
  upstream id.  Deferred until a real-world miss is reported.
- **Catalog: custom-endpoint catalogs.** Self-hosted / proxy
  gateways (Azure OpenAI, LiteLLM, etc.) don't appear on
  models.dev.  Users currently rely on the provider-level
  hardcoded defaults for those endpoints.  A `[catalog.custom]`
  table in `config.toml` that injects extra entries would close
  the gap.
- **Catalog: periodic auto-refresh.** Today the background refresh
  fires on startup only.  A long-lived TUI session keeps the
  in-memory snapshot for the lifetime of the process.  Adding a
  periodic refresh (daily?) would surface upstream pricing changes
  to a running session.
- Bash `cwd` argument: the bash tool currently has no explicit
  working-directory argument, so `touchedPaths` returns nil for it.
  When bash grows a `cwd` field, wire it into `touchedPaths` so
  `cd subdir && cmd` invocations surface subdir context.
- Walk-up logic exists in three places now (`agentsmd.findProjectRoot`,
  `cli.discoverProjectRoot`, `agentsmd.LazyTracker.walkUp`). When a
  fourth use case appears, consolidate into a shared helper.

- **MCP: SSE transport (T1.3a) — shipped.** Hygge can now connect to
  hosted MCP servers (Linear, GitHub, Notion, etc.) over HTTP in
  addition to local stdio subprocesses.  Configured via
  `transport = "sse"` and `url = "..."` in `mcp.toml`; headers
  (bearer tokens, etc.) go in `[servers.<name>.headers]` with
  `$VAR` expansion.  The transport implements the MCP SSE
  handshake, correlates POST responses with JSON-RPC ids via the
  existing Client dispatcher, handles server-initiated
  notifications on the GET stream, and reconnects with exponential
  backoff (capped at 5 attempts by default) on transient drops.
  See `internal/mcp/sse.go` and `cmd/hygge/cli/common.go`.

- **MCP: Streamable HTTP (T1.3b) — next slice.** The 2026
  Streamable HTTP transport spec is intentionally out of scope
  here.  When it lands it will follow the same `Transport`
  interface, extend `validTransports` with `"streamable-http"`,
  and reuse the SSE scanner for the response body.

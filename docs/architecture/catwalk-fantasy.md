# Catwalk + Fantasy migration: target architecture

Hygge is migrating from hand-rolled provider clients and a hand-rolled agent
loop to `charm.land/catwalk` (model catalog) and `charm.land/fantasy` (agent
framework). This document is the target north star — every phase moves toward
this shape.

## Layer map

```
UI (internal/ui)
  │  tea.Msg / bus events
  ▼
Bus (internal/bus)         ← all event-fan-out, no logic
  │
  ▼
App coordinator (cmd/hygge/cli)
  │  assembles: provider, tools, hooks, session
  ▼
Agent (internal/agent)     ← session-scoped loop; will wrap fantasy.Agent
  │  fantasy.Agent.Stream()
  ▼
LLM bridge (internal/llm)  ← new; catwalk client + model resolution
  │  fantasy.LanguageModel
  ▼
Provider adapters (internal/provider/*)
  │  upstream HTTP (Anthropic, OpenAI, OpenRouter, …)
  ▼
Upstream API

Alongside (cross-cutting):
  internal/session + internal/store  — message persistence
  internal/permission                — user-consent gate
  internal/mcp                       — MCP bridge (tool source)
  internal/hook                      — pre/post-tool/message hooks
  internal/plugin                    — Lua plugin host
  internal/skill  / agentsmd         — system-prompt injection
  internal/subagent                  — sub-agent dispatch (task tool)
```

## Boundaries

| Crush concept | Hygge equivalent | File globs |
|---|---|---|
| `agent/coordinator.go` | `cmd/hygge/cli` bootstrap + future `internal/coordinator` | `cmd/hygge/cli/*.go` |
| `agent/agent.go` | `internal/agent/agent.go` + `loop.go` | `internal/agent/*.go` |
| `catwalk` client wrapper | `internal/llm/` (new, Phase 1) | `internal/llm/*.go` |
| `fantasy.LanguageModel` | `internal/provider/provider.go` `Provider` interface → replaced in Phase 2 | `internal/provider/**/*.go` |
| `fantasy.AgentTool` | `internal/tool/tool.go` `Tool` interface → adapted in Phase 3 | `internal/tool/*.go` |
| model catalog (models.dev) | `internal/catalog/` → replaced by catwalk in Phase 1 | `internal/catalog/*.go` |
| MCP bridge | `internal/mcp/` (kept; rewired as `fantasy.AgentTool` in Phase 3) | `internal/mcp/*.go` |
| plugin tools | `internal/plugin/` `PluginTool` + `pluginToolAdapter` (rewired in Phase 3) | `internal/plugin/*.go` |
| hook registry | `internal/hook/` (kept; coordinator compiles at run time) | `internal/hook/*.go` |
| session store | `internal/session/` + `internal/store/` (kept as-is) | `internal/session/*.go`, `internal/store/*.go` |
| sub-agent dispatch | `internal/subagent/runtime.go` (API preserved; impl adapted Phase 4) | `internal/subagent/*.go` |
| bus events | `internal/bus/events.go` (kept; fantasy callbacks publish to this) | `internal/bus/events.go` |

## Per-layer responsibility

### internal/coordinator (new in Phase 4, mirrors crush)

Assembles the full object graph for one session: resolves provider + model
from config, compiles tool registry (built-ins + MCP bridges + plugin
adapters), compiles hook registry, handles model-switch mid-session, and
dispatches sub-agent runs. Roughly mirrors `crush/internal/agent/coordinator.go`
(file:line crush: `internal/agent/coordinator.go:1`). Today this logic lives
spread across `cmd/hygge/cli/cli.go` and `cmd/hygge/cli/run.go`.

### internal/agent (sessionAgent only)

Per-turn engine. Constructs a `fantasy.Agent`, registers the streaming
callbacks for persistence and bus publishing, then calls `Stream()` once per
turn. Handles parallel/sequential tool partitioning, hook invocation, cost
accounting, and compaction. Replaces the hand-rolled `runOneTurn` +
`executeToolCalls` loop in `internal/agent/loop.go`. Roughly mirrors
`crush/internal/agent/agent.go` (file:line crush: `internal/agent/agent.go:1`).

### internal/llm (new, thin — Phase 1)

Catwalk client wrapper plus model-resolution helpers. Loads the embedded
catwalk snapshot (`charm.land/catwalk/pkg/embedded`) on startup, then does a
live ETag-gated refresh in the background (mirroring how crush wires
`internal/llm/catwalk.go`). Exposes a `Resolve(providerName, modelID string)
→ catwalk.Model` helper and a `NewLanguageModel(catwalk.Model, apiKey string)
→ fantasy.LanguageModel` factory. Replaces `internal/catalog/` in Phase 1.
File: `internal/llm/client.go` (to be created in Phase 1).

### internal/tool/* (preserved location, refactored content — Phase 3)

Each built-in tool (`read`, `write`, `edit`, `bash`, `grep`, `glob`, `task`,
`skill`) becomes a `fantasy.AgentTool` via `fantasy.NewAgentTool[T]` or
`fantasy.NewParallelAgentTool[T]`. MCP tools are wrapped via a bridge adapter
in `internal/mcp/`. Plugin tools injected via `PluginTool` + `pluginToolAdapter`
in `internal/plugin/` are rewired to produce a `fantasy.AgentTool` instead of
the current `tool.Tool`. The `internal/tool/registry.go` `Registry` type is
replaced by a `[]fantasy.AgentTool` slice at the coordinator layer.

### Persistence: internal/session + internal/store

Kept as-is throughout the migration. `preparePrompt()` (currently in
`internal/agent/prompt.go`) builds `[]fantasy.Message` from the store starting
at the latest compaction marker, translating our `session.Part` types to
`fantasy.Content` values. The store schema may be freely rewritten (no
migration required per user decision); the `session.Store` interface is the
stable boundary.

### Bus events

The existing bus event surface is fully preserved. Fantasy's streaming
callbacks (equivalent to the 16 callback slots on `fantasy.Agent`) publish the
existing events — `AssistantTextDelta`, `AssistantThinkingDelta`,
`ToolCallRequested`, `ToolCallCompleted`, `MessageAppended`, `CostUpdated`,
`ContextUsageUpdated`, `TurnStarted`, `TurnCompleted` — at slightly different
lifecycle points than today's `runOneTurn`. Phase 3 will require careful
ordering verification to confirm the UI receives events in the expected
sequence (especially: `ToolCallRequested` before the first `ToolCallProgress`).

## Migration phases

| Phase | Scope | PR count | Verification gate |
|---|---|---|---|
| **0** (this) | Add catwalk + fantasy deps; document target architecture | 1 | `mise run precommit` green; no production imports |
| **1** | Replace `internal/catalog/` with catwalk (`internal/llm/client.go`); embedded snapshot + ETag refresh | 1–2 | `go test ./internal/llm/...`; catalog tests green; `go build ./...` |
| **2** | Replace `internal/provider/*` with `fantasy.LanguageModel` adapters; keep `Provider` interface as shim | 2–3 | All provider tests green; agent integration test with fake provider passes |
| **3** | Convert `internal/tool/*` to `fantasy.AgentTool`; rewire MCP + plugin adapters; remove `tool.Registry` | 2–4 | All tool tests green; plugin round-trip test passes; `mise run precommit` |
| **4** | Replace `internal/agent/loop.go` with `fantasy.Agent`; extract `internal/coordinator`; adapt `internal/subagent/runtime.go` | 3–5 | Full end-to-end smoke test (`SMOKE.md`); all existing tests green |
| **5** | UI + slash commands: theme/model-select/API-key dialogs; slash command system mirrors target design | 2–4 | Manual UI verification; all tests green |

## Open decisions captured

1. **Database migration**: no migration needed — store schema may be freely
   rewritten. `internal/store/` is the only consumer of the SQLite schema.
2. **Tool registry location**: `internal/tool/*` path preserved across all
   phases; only the implementation is rewired.
3. **Catwalk loading strategy**: embedded snapshot on startup + live ETag
   fetch in background, mirroring crush's approach in `internal/llm/catwalk.go`.
4. **Subagent runtime API**: `internal/subagent/runtime.go` public API is
   preserved (`Runner`, `RunInput`, `Result`); the implementation is adapted
   under the hood in Phase 4 to dispatch `fantasy.Agent` instead of the
   hand-rolled agent loop.
5. **Plain `fetch` tool**: implement in Phase 3 alongside the other tools;
   `agentic_fetch` (with following/JS rendering) is deferred to a later phase.
6. **Plugin tool injection**: `PluginTool` + `pluginToolAdapter` in Phase 3
   is rewired to produce `fantasy.AgentTool` — the Lua registration API is
   unchanged.

## File:line references from crush worth keeping handy

The following crush source locations are the primary port targets. Keep these
open while implementing each phase.

**Phase 1 — catwalk/LLM layer**
- `crush/internal/llm/catwalk.go` — ETag-gated refresh + embedded snapshot load
- `crush/internal/llm/client.go` — `NewLanguageModel` factory

**Phase 2 — provider adapters**
- `crush/internal/llm/anthropic.go` — Anthropic `fantasy.LanguageModel` impl
- `crush/internal/llm/openai.go` — OpenAI `fantasy.LanguageModel` impl
- `crush/internal/llm/openrouter.go` — OpenRouter adapter

**Phase 3 — tools**
- `crush/internal/tui/tool.go` — tool-to-`fantasy.AgentTool` wiring
- `crush/internal/app/tools.go` — tool registry assembly at coordinator layer

**Phase 4 — agent loop + coordinator**
- `crush/internal/agent/agent.go` — `fantasy.Agent` construction + 16 callbacks
- `crush/internal/agent/coordinator.go` — object-graph assembly, model switch
- `crush/internal/agent/session.go` — `preparePrompt` → `[]fantasy.Message`

**Fantasy API surface (for all phases)**
- `charm.land/fantasy@v0.23.2/agent.go` — `Agent` type, `Stream()` method
- `charm.land/fantasy@v0.23.2/tool.go` — `AgentTool`, `NewAgentTool[T]`
- `charm.land/fantasy@v0.23.2/model.go` — `LanguageModel` interface
- `charm.land/catwalk@v0.40.0/pkg/catwalk/provider.go` — `Provider` struct
- `charm.land/catwalk@v0.40.0/pkg/catwalk/client.go` — `Client` type

# Hygge Agent Guide

Hygge is a Go terminal AI coding assistant with a Bubble Tea v2 TUI,
Catwalk-backed model catalog, Fantasy-backed LLM runtime, SQLite session
store, plugin-provided tools, MCP, hooks, subagents, and a Hygge-specific
bubble/sidebar UI.

## Non-negotiables

- Keep unrelated product/reference names out of code, comments, docs, tests,
  commit messages, and fixtures. Dependency/import names such as
  `charm.land/fantasy` and `charm.land/catwalk` are allowed.
- Preserve Hygge's identity: bubble chat UI, sidebar, theme atoms, plugin
  extensibility, subagents, slash commands, and local-first session storage.
- Prefer small, focused slices. Do not refactor adjacent systems unless the
  task requires it.
- Do not push. Commit only when explicitly asked or when continuing an
  already-approved autonomous implementation flow.

## Implementation workflow

- Use `mise run precommit` as the single full verification command. It runs
  lint, race tests, and build.
- Known intermittent SQLite/concurrency flakes may appear in UI/CLI tests; if
  one occurs, rerun the exact failing test with `-race -count=3` and report
  the evidence.
- Format Go with the configured tooling; `mise run precommit` must be clean
  before claiming success.
- Use hermetic tests: `t.TempDir()`, injected home/config/env/time, no real
  user home, no live network, no real remote API calls.
- Avoid `as any`-style escape hatches in spirit: use real Go types and narrow
  interfaces.

## Architecture map

- `cmd/hygge/cli/` wires config, catalog, provider/model resolution, store,
  tools, MCP, notifications, and TUI startup.
- `internal/agent/` owns the runtime/session-agent turn loop, Fantasy stream
  integration, queueing, compaction summaries, title seams, and tool adapters.
- `internal/catalog/` loads Catwalk provider/model metadata with disk cache and
  embedded fallback.
- `internal/llm/` resolves Catwalk/config provider data into Fantasy providers
  and language models.
- `internal/store/` is SQLite persistence for sessions, messages, markers,
  totals, and todos.
- `internal/session/` defines persisted domain types and store-facing
  interfaces.
- `internal/tool/` contains built-in tools plus plugin/MCP adapter seams.
- `internal/ui/` is the Bubble Tea v2 app, overlays, hydration, sidebar, queue
  and todo pills, attachments, model/API-key/theme dialogs, and chat bubbles.
- `internal/ui/components/` contains reusable TUI components; keep visual
  changes here when possible.
- `internal/ui/theme/` owns theme atoms and parsing. Add atoms deliberately and
  update all theme fixtures/tests.
- `internal/plugin/`, `internal/mcp/`, `internal/hook/`, `internal/permission/`,
  and `internal/subagent/` are integration boundaries; keep their contracts
  stable unless the task is specifically about them.

## UI rules

- Bubble Tea/Lip Gloss/Bubbles use v2 import paths: `charm.land/.../v2`.
- In Lip Gloss v2, `lipgloss.Color(...)` is a function returning
  `image/color.Color`; fields that hold colors should use `color.Color`.
- Top-level Tea models return `tea.View`; tests often need `.Content`.
- Prefer overlay-stack dialogs for modal UI. Slash completion popovers are not
  overlays.
- Update input focus when opening/closing overlays or permission prompts.
- Respect terminal theme; avoid background fills that reduce contrast.

## Config and persistence

- Config is TOML via `pelletier/go-toml/v2`; env vars use double-underscore
  separators.
- Use the narrow config writer seams for model, API key, and theme persistence.
  Preserve unrelated fields; comments may be reformatted.
- IDs are ULIDs, timestamps are Unix milliseconds, costs are `float64` USD.
- Tests must not depend on an existing local database or config file.

## CLI command UX

- Hygge CLI commands should feel like part of the terminal product, not plain
  line prompts. Prefer Fang-wrapped Cobra commands with Bubble Tea/Bubbles/Lip
  Gloss v2 components for interactive flows.
- Use selectable choices for bounded decisions such as scope, transport,
  provider, model, auth method, or yes/no confirmations. Do not ask users to
  type option names when the set of valid answers is known.
- Use text input only for genuinely free-form values: names, paths, commands,
  URLs, tokens, headers, and similar user-provided strings. Hide secret input.
- Keep automation safe: every interactive choice must also have an explicit
  flag or noninteractive path. Non-TTY runs must never hang; fail with clear
  remediation when required choices are missing.
- Keep command logic UI-independent where practical. Parse flags and build
  typed request structs separately from Bubble Tea models, then pass the result
  to narrow persistence/service functions.
- Make success and error output clear and pretty: include the affected resource,
  scope, and path when useful, while preserving script-friendly behavior for
  noninteractive output.
- Test CLI commands hermetically with fake home/config/state directories and
  direct Bubble Tea model updates for interactive flows; avoid real terminals,
  real user config, and live network calls in tests.

## Provider/runtime notes

- Active turns should use Fantasy when a `fantasy.LanguageModel` is available;
  legacy provider seams exist for nil-Fantasy tests/fallbacks.
- Preserve store shape for hydration: assistant text/thinking/tool-use parts in
  assistant messages; tool results as tool messages.
- On stream errors, flush partial assistant state before returning.
- Runtime model switches affect future sends; config persistence is additive.

- `TODOS.md` — current completed work and follow-up backlog.

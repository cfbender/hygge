# Hygge — Polish-Phase Follow-Ups

Items deferred during the v0.3 → v0.4 polish phase. Order is rough priority: top of each section is higher-leverage. None are blocking; most are small, well-scoped slices.

## For carpenter / future UI work

- **Read `docs/agents/ui-v2-gotchas.md` before editing anything under `internal/ui/`.** It documents the bubbletea / lipgloss / bubbles v2 traps the agent has hit repeatedly (especially: `lipgloss.Color` is a function, not a type — fields hold `color.Color` from `image/color`).

## High-impact UX

- [ ] **First-class agent profiles + `AgentType` plumbing**
  Today `AgentType` is hardcoded to `"General"` in the footer and assistant bubble header. Wire it from a real agent-profile concept on the active session. This also unlocks per-agent accent colors (the `AccentColor` seam is already plumbed end-to-end through the bubble primitive).

- [x] **Sessions modal per-row cost: split own vs rolled-up**
  T2.1 added a recursive CTE for cost roll-up. The sessions modal currently shows the rolled-up total; users often want to see their own session's cost vs cost-including-subagents. Add a column or toggle.

- [ ] **Foreground-switch into a completed subagent should reload history from store**
  Today `refreshMessagesForForeground` is a stub when switching INTO a completed subagent — the buffer is whatever the live state captured. Should call the hydration path against the child session id.

- [ ] **Proper scrollable thinking viewport in assistant bubbles**
  Phase 4 added max-line truncation with `… +N more lines (thinking)`. A real per-bubble viewport with key-driven expand/collapse would let users actually read long thinking on resume.

- [x] **`ModalHelp` close handler + render path**
  Phase 6C added a typed overlay stack and a Hygge-styled help overlay with Esc/q close handling.

- [ ] Subagent view doesn't show initial message on first render, but does later on resume and re-view

- [ ] Permissions per session does not work

## Architecture

- [x] **Unify modal state**
  Phase 6C introduced a typed overlay stack for help, sessions, compaction confirmation, and a compatibility permission overlay. `activeModal` remains only as a compatibility mirror for current tests/callers.

## Plugin host

- [ ] **`hygge plugins install` / `plugins remove` should rewrite `config.toml`**
  Today install/remove fetches/deletes but doesn't update the config so the plugin actually loads on next launch.

- [ ] **`hygge plugins update` hot-swap**
  Currently requires restart. Needs `UnregisterAll(pluginName)` across the tool / command / hook / slash registries with owner-tagging on registrations (already plumbed; just no API yet).

## Storage / data model

- [ ] **Migration: add `parent_tool_use_id TEXT` column to sessions**
  Subagent hydration currently parses `[toolUseID]` out of the session slug via `extractToolUseIDFromSlug`. Brittle if `buildSlug` format ever changes. A proper column makes the link exact.

- [ ] **SQLite-concurrency test hardening under `-race`**
  Three known flakes pass in isolation but fail intermittently under parallel `-race` load:
  `TestConcurrent_ReadsAndOneWriter` (`internal/store`)
  `TestOpenSessionsModalOnStart_SelectSessionSwitches` (`internal/ui`)
  `TestEnsureSessionLazilyCreates` (`internal/ui`)
  Probably need to drop `t.Parallel()` or add a small retry/backoff in test setup.

- [ ] **`uiMessage` `CreatedAt`-based sorting for compaction markers**
  Phase 2 added a `Timestamp` field, but the multi-compaction ordering path still falls back to "append at end" when `BeforeMessageID` isn't found. Use `Timestamp` for chronological insertion.

## Sidebar follow-ups

- [ ] **Modified files: respect `.gitignore` and binary skips**
  `git diff --numstat` reports binaries as `-` (treated as 0/0); fine. But staged-only / ignored files might still surface if a tool writes them. Verify against `.gitignore`. Also: if running outside a git repo, the section silently empties — consider a fallback like raw mtime tracking.

- [x] **Sidebar session-title caching** *(commit `b684fbb+1`)*
  Cached on `App.sessionTitle`; populated in `Init`, `ensureSession`, `applySwitchSession`, and `bus.MessageAppended`. `View()` no longer calls `Store.GetSession`.

- [ ] **Breadcrumb `Store.GetSession` call in `breadcrumbSegments()`**
  `breadcrumbSegments()` still calls `Store.GetSession` synchronously per-segment on the render goroutine (same pattern as the old `sidebarSessionTitle`). Visible only at depth > 1 (sub-agent foreground). Cache it the same way — a `breadcrumbLabels map[string]string` field updated on SubagentStarted / SubagentCompleted events. Deferred to a follow-up slice.

## Animation / polish

- [x] **Boot-phase progress bar** — `supportsProgressBar()` in `run.go` emits OSC 9;4;3 (indeterminate) to stderr before bootstrap and resets via defer. Visible in Ghostty/iTerm2/WezTerm/Kitty/Windows Terminal/Rio. *(this slice)*

- [x] **Working placeholder rotation in textarea** — `Input.SetBusy(bool)` switches between `WorkingPlaceholder` and `ReadyPlaceholder`. `App.Update` draws a random entry from `workingPlaceholders` on `sendStarted` and restores on `sendCompleted`. *(this slice)*

- [ ] **Status-bar busy-spinner pre-rendered frames** *(low priority — status-bar location changed since the spinner audit; verify it still exists in its old form, or close out)*

## Testing / hermeticity

- [ ] **Extract `GitRunner` interface on `PackageManager` for test injection**
  Today `internal/plugin/pkgmgr.go` defensively neuters git credential helpers via env vars and `gitCommand` wrapping. The new `internal/state/git_numstat.go` also shells out to git. Extracting a shared `GitRunner` interface lets both inject a fake — no real `git` invocation, no keychain prompts.

## External / upstream

- [ ] **Glamour transitive `lipgloss` v1 dep**
  `github.com/charmbracelet/glamour` still pulls in `github.com/charmbracelet/lipgloss` v1 indirectly. Will linger until glamour ships a `lipgloss/v2`-compatible release. Track upstream.

## Known issues — follow-up slices

- [ ] **OSC probe leak** — The TUI emits OSC probes that are not fully reclaimed on exit. Needs investigation into bubbletea v2 internals (alt-screen teardown sequence). Separate slice.

- [x] **OSC response leak → garbage in textarea** — bubbletea v2.0.6's OSC parser doesn't fully consume the terminal color-query response; inner content (`11;rgb:...`) leaks as `tea.KeyPressMsg.Text`. Fixed with a `tea.WithFilter` hook (`dropOSCResponses`) in `cmd/hygge/cli/osc_filter.go`. Secondary defence on top of `WithColorProfile(TrueColor)`. Remove filter if upstream fixes parsing. See `docs/agents/ui-v2-gotchas.md`. *(commit: see fix(ui) decouple send pipeline)*

- [x] **UI hang between Enter and modal (agent-loop goroutine)** — `startSend` now launches the `ensureSession` + `Agent.Send` call in a goroutine and injects `sendCompleted` back into the event loop via `app.program.Send` (bubbletea v2 `*tea.Program.Send`, `tea.go:1183`). The bubbletea event loop remains responsive during agent turns. *(commit: see fix(ui) decouple send pipeline)*

---


- ✅ Modified Files in sidebar — real data with git numstat (commit `deae712`)
- ✅ Right-side sidebar replaces header bar (commit `f4c7dd0`)
- ✅ `Input.Focused` toggle on modal open/close (commit `55a0f2d`)
- ✅ `RoleThinking` dead-branch cleanup (commit `55a0f2d`)
- ✅ Delete unused `StatusBar` component (commit `55a0f2d`)
- ✅ Sessions modal: show own cost + rolled-up in parens (subagents)
- ✅ **Subagent dispatch `invalid_request` (OpenRouter 400)** — fork-chain CTE leaked parent transcript into subagent sessions because the recursive step had no guard on `fork_message_id IS NOT NULL`; a dangling `tool_use` with no matching output caused the provider to reject the request. Fixed by adding `AND a.fork_message_id IS NOT NULL` to the CTE's recursive arm in `internal/store/messages.go`. `PropagateTotals` recursive CTE is intentionally left alone — it correctly rolls subagent costs up to ancestor sessions.
- ✅ **OSC response filter** — `dropOSCResponses` filter via `tea.WithFilter` drops leaked terminal color-query responses before they reach the textarea. (`cmd/hygge/cli/osc_filter.go`)
- ✅ **Decouple `startSend`** — `Agent.Send` now runs in a goroutine; `sendCompleted` arrives via `program.Send` (`tea.go:1183`). UI event loop is responsive during agent turns.
- ✅ **Boot progress bar + working placeholder + env/ctx options** — `supportsProgressBar()` emits OSC 9;4;3 around bootstrap; `Input.SetBusy` rotates placeholder on send; `tea.WithEnvironment` added to `NewProgram` call.
- ✅ **Inline tool-row status text** — `ToolStatus` enum on `UIMessage`; `handleBusEvent` transitions `Pending → AwaitingPermission → Running → Completed/Error/Cancelled` via `ToolCallRequested`, `PermissionAsked`, `PermissionReplied`, and `ToolCallCompleted`. `renderToolGroup` renders "Requesting permission…" / "Waiting for tool response…" / "error" / "cancelled" inline. Hydrated rows stamped Completed/Error/Unknown. Subagent (`task`) rows unaffected. No new bus events.
- ✅ **Per-session prompt queue (Wave 1)** — `Agent.Send` enqueues when session is busy; dispatches next queued send automatically on completion. `QueueCount`, `QueuedPrompts`, `ClearQueue`, `IsSessionBusy` methods added. `bus.QueueChanged` and `bus.TurnCompleted` events added. UI placeholder shows `(N queued)` suffix. Two-step Esc: first Esc clears queue, second Esc cancels active run. `internal/agent/queue_test.go` covers 7 scenarios with `-race`. Sending while busy now allowed (message is queued instead of blocked).
- ✅ **Desktop notifications (Wave 1)** — `internal/notify` package with `NoopBackend` and `NativeBackend` (via `github.com/gen2brain/beeep`). `config.NotificationsConfig` with `enabled`, `permission_ask`, `turn_complete` (defaults: true/true/false). UI `maybeNotify` helper gates on focus state (`tea.FocusMsg`/`tea.BlurMsg`), config, and kind. Permission asks and turn completions trigger notifications when terminal is unfocused. Notification tests in `internal/notify/notify_test.go` and `internal/ui/app_test.go`.
- ⏳ **Deferred: 750ms min-spinner-dwell pattern** — Crush has a 750ms minimum-spinner-dwell pattern for API key verification dialogs. Apply when verification dialogs are added (API keys, model probing, etc.).
---

_Last updated after the prompt-queue + desktop-notifications slice (wave 1 of crush adoption)._

## Catwalk + Fantasy migration

Target architecture documented in `docs/architecture/catwalk-fantasy.md`.
Migration plan artifact: `7yLWjW`.

- [x] **Phase 0 — Preparation** — `charm.land/catwalk v0.40.0` and `charm.land/fantasy v0.23.2` added as direct deps. `internal/llm/probe_test.go` confirms packages compile. Architecture doc written. *(this commit)*
- [x] **Phase 1 — LLM layer** — Replace `internal/catalog/` with catwalk client wrapper in `internal/llm/`; embedded snapshot + ETag refresh. *(this commit)*
- [x] **Phase 2 — Provider adapters** — Added `internal/llm` fantasy provider/model construction bridge with metadata mapping (`ResolveProviderModel`) and hermetic coverage. Existing `internal/provider.Provider` concrete stream adapters remain in place for now; Phase 3 will swap the turn loop to fantasy stream primitives and retire legacy adapter internals incrementally.
- [x] **Phase 3 — Agent stream loop + tools** — Active CLI turns construct a `fantasy.LanguageModel`, wrap registered Hygge tools as `fantasy.AgentTool`, and run through `fantasy.Agent.Stream` while preserving store/bus/tool hook contracts. Subagent internals still use the existing agent seam when invoked by the `task` tool; Phase 4 can extract the coordinator and remove the legacy provider fallback.
- [x] **Phase 4 — Coordinator cleanup** — Added explicit `agent.Runtime` and `agent.SessionAgent` split for turn execution/tool assembly, moved Fantasy tool adaptation under the runtime, and wired subagents through the same Fantasy model resolver where bootstrap can provide one. Legacy provider streaming remains isolated as the nil-Fantasy/test fallback for Phase 5/6 cleanup.
- [x] **Phase 5 — Internal Fantasy agents** — Compaction summaries now use a no-tool Fantasy-backed internal agent when a Fantasy model is configured, preserving existing marker/store/UI contracts and leaving summary usage excluded from session cost totals as before. Hygge has no active model-generated title/slug path today; `agent.Runtime.GenerateTitle` is the narrow no-tool seam for a future session-title slice.
- [ ] **Phase 6 — UI** — Theme/model-select/API-key dialogs; slash command system update.
  - [x] **Phase 6A — UX foundation** — Queue status pills near the input/footer and input-event filtering for OSC leaks plus 15ms mouse wheel/motion spam throttling.
  - [x] **Phase 6B — Slash-command completion popover** — Fuzzy inline completions with descriptions, keyboard navigation, and Hygge styling.
  - [x] **Phase 6C — Dialog overlay stack** — Typed overlay stack routes topmost keys first, centralizes input-focus state, renders help, and wraps the existing permission queue as a compatibility overlay while preserving queue semantics.
  - [ ] **Phase 6D+ follow-ups** — API-key/model/theme dialogs on the overlay stack; richer command dialogs; todo persistence/agent todo tracking; attachments; optional backdrop rendering over preserved app content instead of full-screen modal replacement.

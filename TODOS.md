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

- [ ] **`ModalHelp` close handler + render path**
  Wired up the open path during the input-focus cleanup, but the help overlay has no render path in `View()` yet and no close handler. Implement when the help dialog itself lands.

## Architecture

- [ ] **Unify modal state**
  Modal state today is split across `activeModal string` (single-slot: `""`/`"sessions"`/`"help"`/`"compact-confirm"`) and `pendingPerms []PermissionRequest` (independent queue that can coexist with any active modal). The two don't compose: if a `PermissionAsked` fires while compaction is open, both look "true". `handleKey` happens to route to perms first, so it works — but the invariant is implicit, not enforced. Refactor to a single modal stack with typed entries.

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

- [ ] **Status-bar busy-spinner pre-rendered frames** *(low priority — status-bar location changed since the spinner audit; verify it still exists in its old form, or close out)*

## Testing / hermeticity

- [ ] **Extract `GitRunner` interface on `PackageManager` for test injection**
  Today `internal/plugin/pkgmgr.go` defensively neuters git credential helpers via env vars and `gitCommand` wrapping. The new `internal/state/git_numstat.go` also shells out to git. Extracting a shared `GitRunner` interface lets both inject a fake — no real `git` invocation, no keychain prompts.

## External / upstream

- [ ] **Glamour transitive `lipgloss` v1 dep**
  `github.com/charmbracelet/glamour` still pulls in `github.com/charmbracelet/lipgloss` v1 indirectly. Will linger until glamour ships a `lipgloss/v2`-compatible release. Track upstream.

## Known issues — follow-up slices

- [ ] **OSC probe leak** — The TUI emits OSC probes that are not fully reclaimed on exit. Needs investigation into bubbletea v2 internals (alt-screen teardown sequence). Separate slice.

- [ ] **UI hang between Enter and modal (agent-loop goroutine)** — The agent loop currently runs synchronously relative to the bubbletea event loop. Heavy inference turns can hang the UI between when the user presses Enter and when a modal appears. Fix: run the agent loop in its own goroutine with a command-channel bridge. Architectural change; separate slice.

---


- ✅ Modified Files in sidebar — real data with git numstat (commit `deae712`)
- ✅ Right-side sidebar replaces header bar (commit `f4c7dd0`)
- ✅ `Input.Focused` toggle on modal open/close (commit `55a0f2d`)
- ✅ `RoleThinking` dead-branch cleanup (commit `55a0f2d`)
- ✅ Delete unused `StatusBar` component (commit `55a0f2d`)
- ✅ Sessions modal: show own cost + rolled-up in parens (subagents)
- ✅ **Subagent dispatch `invalid_request` (OpenRouter 400)** — fork-chain CTE leaked parent transcript into subagent sessions because the recursive step had no guard on `fork_message_id IS NOT NULL`; a dangling `tool_use` with no matching output caused the provider to reject the request. Fixed by adding `AND a.fork_message_id IS NOT NULL` to the CTE's recursive arm in `internal/store/messages.go`. `PropagateTotals` recursive CTE is intentionally left alone — it correctly rolls subagent costs up to ancestor sessions.

---

_Last updated after the subagent CTE fix (commit pending)._

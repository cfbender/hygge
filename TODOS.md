# Hygge — Polish-Phase Follow-Ups

Items deferred during the v0.3 → v0.4 polish phase. Order is rough priority: top of each section is higher-leverage. None are blocking; most are small, well-scoped slices.

## High-impact UX

- [ ] **First-class agent profiles + `AgentType` plumbing**
  Today `AgentType` is hardcoded to `"General"` in the footer and assistant bubble header. Wire it from a real agent-profile concept on the active session. This also unlocks per-agent accent colors (the `AccentColor` seam is already plumbed end-to-end through the bubble primitive).

- [ ] **Sessions modal per-row cost: split own vs rolled-up**
  T2.1 added a recursive CTE for cost roll-up. The sessions modal currently shows the rolled-up total; users often want to see their own session's cost vs cost-including-subagents. Add a column or toggle.

- [ ] **Foreground-switch into a completed subagent should reload history from store**
  Today `refreshMessagesForForeground` is a stub when switching INTO a completed subagent — the buffer is whatever the live state captured. Should call the hydration path against the child session id.

- [ ] **Proper scrollable thinking viewport in assistant bubbles**
  Phase 4 added max-line truncation with `… +N more lines (thinking)`. A real per-bubble viewport with key-driven expand/collapse would let users actually read long thinking on resume.

- [ ] **`Input.Focused` toggled on modal open / close**
  The bordered input from Phase 4 has a focus-aware border color, but the App never sets `input.Focused = false` when a modal opens. Border always renders accent-colored at runtime. Cosmetic; one or two call sites in `app.go`.

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

## Animation / polish

- [ ] **Status-bar `spinner.Model` pre-rendered frames**
  The status-bar busy spinner re-creates lipgloss styles per tick. Same pre-rendered cached frames pattern as `components/anim` would eliminate the per-frame allocations.

- [ ] **`RoleThinking` dead-branch cleanup**
  Phase 2 collapsed thinking into the assistant `UIMessage`. The `RoleThinking` enum value and its `renderOne` case still exist but are never emitted. Safe to delete.

- [ ] **Delete unused `StatusBar` component**
  Phase 1 replaced it with `HeaderBar` but left the file in the codebase. Remove it.

## Testing / hermeticity

- [ ] **Extract `GitRunner` interface on `PackageManager` for test injection**
  Today `internal/plugin/pkgmgr.go` defensively neuters git credential helpers via env vars and `gitCommand` wrapping. Extracting a `GitRunner` interface lets tests inject a fake — no real `git` invocation, no keychain prompts.

## External / upstream

- [ ] **Glamour transitive `lipgloss` v1 dep**
  `github.com/charmbracelet/glamour` still pulls in `github.com/charmbracelet/lipgloss` v1 indirectly. Will linger until glamour ships a `lipgloss/v2`-compatible release. Track upstream.

---

_Last updated as part of UI redesign Phase 4 (commit `958ee6d`)._

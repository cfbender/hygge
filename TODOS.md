# Hygge — Polish-Phase Follow-Ups

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

## Testing / hermeticity

- [ ] **Extract `GitRunner` interface on `PackageManager` for test injection**
  Today `internal/plugin/pkgmgr.go` defensively neuters git credential helpers via env vars and `gitCommand` wrapping. The new `internal/state/git_numstat.go` also shells out to git. Extracting a shared `GitRunner` interface lets both inject a fake — no real `git` invocation, no keychain prompts.

## UI / interaction polish

- [ ] **`@` mentions for files and subagents**
  Support mention-style insertion/selection for repository files and available subagents.

- [ ] **Multi-line paste visibility**
  Pasting multi-line content currently hides it from view; keep pasted content visible or provide a clear collapsed preview.

- [x] **Input box width styling**
  Fix prompt input styling so it does not stretch overly wide.

- [ ] **Slash commands as modal popup**
  Replace the current slash-command UI with a modal popup flow similar to Crush.

- [ ] **Model selector only shows configured providers**
  Limit selectable models to providers configured by the user.

- [ ] **Diff view component**
  Add a diff view for displaying diffs and edit-tool changes; reference Crush's diff view behavior.

- [ ] **Expandable bash tool output**
  Bash tool blocks should show truncated output with click/keyboard affordance to expand.

- [ ] **Refine system prompt**
  Update the system prompt to reflect Hygge's current capabilities.

- [ ] **`Ctrl+E` external prompt editor**
  Add a shortcut to edit the current prompt in an external editor.

- [ ] **Yolo mode**
  Add a mode for reduced confirmations / more autonomous execution.

- [ ] **Hygge smoking chimney animation**
  Add a smoking-chimney animation for Hygge branding/delight.

- [ ] **Queued messages sticky at bottom**
  Keep queued messages sticky at the bottom and do not send them until the main thread has a break.

- [ ] Text alignment and bubble fill
  Text should take up the width of the bubble before wrapping.

# Hygge — Polish-Phase Follow-Ups

## Plugin host

- [ ] **`hygge plugins install` / `plugins remove` should rewrite `config.toml`**
  Today install/remove fetches/deletes but doesn't update the config so the plugin actually loads on next launch.

- [ ] **`hygge plugins update` hot-swap**
  Currently requires restart. Needs `UnregisterAll(pluginName)` across the tool / command / hook / slash registries with owner-tagging on registrations (already plumbed; just no API yet).

## Storage / data model

- [ ] **Migration: add `parent_tool_use_id TEXT` column to sessions**
  Subagent hydration currently parses `[toolUseID]` out of the session slug via `extractToolUseIDFromSlug`. Brittle if `buildSlug` format ever changes. A proper column makes the link exact.

- [ ] **`uiMessage` `CreatedAt`-based sorting for compaction markers**
  Phase 2 added a `Timestamp` field, but the multi-compaction ordering path still falls back to "append at end" when `BeforeMessageID` isn't found. Use `Timestamp` for chronological insertion.

## Testing / hermeticity

- [ ] **Extract `GitRunner` interface on `PackageManager` for test injection**
  Today `internal/plugin/pkgmgr.go` defensively neuters git credential helpers via env vars and `gitCommand` wrapping. The new `internal/state/git_numstat.go` also shells out to git. Extracting a shared `GitRunner` interface lets both inject a fake — no real `git` invocation, no keychain prompts.

## Code

- Refactor and split out internal/ui/app.go

## UI / interaction polish

- [ ] match input border color to mode color

- [ ] compaction change from notice above input to compaction block with crunching animation while working

- [ ] Splash screen with animated ASCII art logo and centered prompt box, with tips below (ie. ctrl + e to open prompt in external editor, ctrl + t to cycle model reasoning level, tab to switch mode)

- [ ] Configure default profile in user config

- [ ] Show more info from model around tool usage. Right now, the chat only shows a list of tool usage, where in something like opencode, the agent explains what it's doing before calling the edit or write tools.

- [ ] Layout shift on glamourization after message finishes streaming

- [ ] Header redesign - bolder, background color maybe?, more padding (3 rows)

- At this point, tag as v0.3.0 and push the tag as a release on github

- [ ] **Refine system prompt**
  Update the system prompt to reflect Hygge's current capabilities.

- [ ] **Hygge smoking chimney animation**
  Add a smoking-chimney animation for Hygge branding/delight.

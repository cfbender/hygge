# Hygge — deferred work / status notes

## Shipped

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

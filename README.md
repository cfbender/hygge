# hygge

A terminal-based AI coding assistant.

## Status

v0.1-dev. The core loop is working:

- Anthropic provider with streaming responses and prompt caching.
- Six builtin tools: `read`, `write`, `edit`, `bash`, `grep`, `glob`.
- Permission engine with secrets denylist, scope-based allowances
  (`once` / `session` / `always`), and a `.hygge/config.toml` walk-up.
- SQLite-backed session store with resume.
- TOML config with profile inheritance and `state.json` for runtime data.
- Cost tracking via the [models.dev](https://models.dev) catalog with a
  hard-coded fallback table for offline use.

## Quick start

Requires [mise](https://mise.jdx.dev) for the Go toolchain pin.

```sh
mise install                    # installs Go 1.26 and golangci-lint 2
mise run build                  # compiles to ./bin/hygge
export ANTHROPIC_API_KEY=...    # required to talk to the model
./bin/hygge                     # launches the TUI in the current directory
```

## Commands

- `hygge` — launch the TUI for a new session in the current directory.
- `hygge resume [id-prefix]` — re-open the most recent matching session.
- `hygge sessions list` — list recent sessions; `--include-deleted` to show soft-deleted rows.
- `hygge sessions delete <id-prefix>` — soft-delete a session.
- `hygge profile list` / `hygge profile use <name>` — manage config profiles.
- `hygge config explain [key]` — print the effective config with provenance.
- `hygge theme list` / `hygge theme show <name>` — inspect available themes.
- `hygge version` — print version, Go version, OS/arch.

## Configuration

User config lives at `~/.config/hygge/config.toml` (or
`$XDG_CONFIG_HOME/hygge/config.toml`).

Profiles live in `~/.config/hygge/profiles/<name>.toml` and are activated
with `hygge profile use <name>`. A profile can `extends = "other"` to
inherit from another profile.

A `.hygge/config.toml` file in the current directory (or any parent) is
picked up automatically as the highest-priority layer. Use this for
per-project model or permission overrides.

`hygge config explain` shows the resolved config along with the source
of every value: builtin default, user config, profile, or project file.

## Development

```sh
mise run test          # tests with -race
mise run lint          # golangci-lint
mise run precommit     # lint + test + build — run before every commit
```

See `SMOKE.md` for the manual ship-gate checklist.

## License

MIT — see [LICENSE](LICENSE).

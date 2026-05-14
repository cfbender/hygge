# hygge v0.1 ship-gate checklist

Manual smoke pass run against a real Anthropic API key before tagging v0.1.
Every box must be checked before shipping.

Build first:

```sh
mise install
mise run build
```

Set `ANTHROPIC_API_KEY` in your environment for the items that need it.

## Boot and CLI surface

- [ ] **Empty HOME boots cleanly.**
      `HOME=$(mktemp -d) ./bin/hygge --help` exits 0 and prints the usage block.

- [ ] **`hygge version`.**
      `./bin/hygge version` prints a version string, Go version, OS/arch line.

- [ ] **`hygge config explain` with no user config.**
      `HOME=$(mktemp -d) ./bin/hygge config explain` shows the builtin defaults
      with `(default)` provenance on every row.

- [ ] **Profile use.**
      ```
      mkdir -p ~/.config/hygge/profiles
      cat > ~/.config/hygge/profiles/work.toml <<'EOF'
      [model]
      provider = "anthropic"
      name = "claude-sonnet-4.5"
      EOF
      ./bin/hygge profile use work
      ./bin/hygge profile list
      ```
      Last command marks `work` with a leading `*`.

- [ ] **`.hygge/config.toml` walk-up.**
      ```
      tmp=$(mktemp -d)
      mkdir -p "$tmp/.hygge"
      cat > "$tmp/.hygge/config.toml" <<'EOF'
      [permission]
      shell = "deny"
      EOF
      (cd "$tmp" && ../bin/hygge config explain permission.shell)
      ```
      Output shows `deny` with provenance pointing at the project file.

- [ ] **Skills load from `.agents/skills`.**
      ```
      tmp=$(mktemp -d)
      mkdir -p "$tmp/.agents/skills"
      cat > "$tmp/.agents/skills/demo.md" <<'EOF'
      ---
      name: demo
      description: Demo skill for the smoke test
      when_to_use: Manual smoke verification only
      ---
      Body of the demo skill.
      EOF
      (cd "$tmp" && ../bin/hygge skills list)
      ```
      Output lists `demo` with source `project/.agents`.

- [ ] **AGENTS.md is picked up.**
      ```
      tmp=$(mktemp -d)
      mkdir -p "$tmp/.git"
      cat > "$tmp/AGENTS.md" <<'EOF'
      # Project rules
      Be conservative with destructive shell commands.
      EOF
      (cd "$tmp" && ../bin/hygge context show)
      ```
      Output shows `## Project context` followed by the file's body.

## TUI session

- [ ] **TUI launches.**
      `ANTHROPIC_API_KEY=... ./bin/hygge` opens the alt-screen UI. The status
      bar shows the profile name, model id, and abbreviated cwd. The footer
      shows `$0.0000` and `ctx 0%`.

- [ ] **Simple message round-trip.**
      Type `hello`, press Enter. Assistant text streams in. Footer cost ticks
      up after the response. Quit with `q` and confirm a session row was
      written: `./bin/hygge sessions list` shows the row.

- [ ] **Tool call with permission prompt.**
      Ask `what files are in this directory?` The model requests `glob` or
      `bash`. For `bash`, the permission modal appears. Press `y`. The tool
      runs, output is shown, the assistant continues.

- [ ] **Permission denied gracefully.**
      Ask the agent to read `~/.aws/credentials`. The secrets denylist
      blocks it without a modal; the assistant receives the error result
      and responds with a refusal explanation. No crash.

- [ ] **Edit inside PWD.**
      Ask the agent to create `hello.txt` in the current directory. The
      permission modal shows `file.write` for the path. Approve. The file
      appears on disk with the expected content.

- [ ] **Ctrl+C cancels mid-stream.**
      Submit a long question (e.g. "explain the entire Linux boot process").
      Hit Ctrl+C while the response is streaming. Input re-enables. No
      assistant message is committed for the cancelled turn — confirm with
      `./bin/hygge sessions list` and resume to inspect the transcript.

- [ ] **Quit + resume.**
      Press `q` in the TUI. Run `./bin/hygge resume`. The TUI reopens with
      the prior session's messages visible in the transcript pane.

## Session subcommands

- [ ] **`hygge sessions list`.**
      Recent sessions appear with short ids, project dirs, and timestamps.

- [ ] **`hygge sessions delete <prefix> --no-confirm`.**
      Soft-deletes the matched session. A subsequent `hygge sessions list`
      omits it; `--include-deleted` shows it with a `(deleted)` marker.

## Offline / cost catalog

- [ ] **Cost catalog refresh handles offline.**
      Disable network access and launch `./bin/hygge`. Send one message —
      the cost line still renders using fallback prices. No fatal error.

## Test suite

- [ ] **Race detector clean.**
      `mise run test` (which runs `-race`) prints no `DATA RACE` warnings
      and exits 0.

## Known gaps for v0.1

These are deliberately deferred. Do not block v0.1 on them.

- LSP integration (dropped from v0.1 scope).
- MCP client.
- Subagents.
- OpenAI / OpenRouter / additional providers.
- Plugins (WASM or subprocess).
- Live theme reload and additional builtin themes.
- Compaction UI.
- Per-message expand for tool results.
- AssistantThinkingDelta rendering.
- History cycling on Up arrow.
- Windows shell support in the `bash` tool.

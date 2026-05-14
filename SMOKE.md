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

- [ ] **Sub-agents: built-in `general` is always available.**
      `./bin/hygge subagents list` prints a row with name `general`,
      source `builtin`, and a non-empty description. `./bin/hygge
      subagents show general` prints the full system prompt body.

- [ ] **Sub-agents: TOML additions are picked up.**
      ```
      tmp=$(mktemp -d)
      mkdir -p "$tmp/.agents"
      cat > "$tmp/.agents/subagents.toml" <<'EOF'
      [subagents.searcher]
      description = "Find files matching a pattern"
      prompt = "You search the repo. Return one final message."
      tools = ["read", "grep", "glob"]
      EOF
      (cd "$tmp" && ../bin/hygge subagents list)
      ```
      Output lists both `general` (builtin) and `searcher` (source
      `project`).

- [ ] **`task` tool dispatches a sub-agent and persists a sub-session
      with a live nested TUI block.**
      With `ANTHROPIC_API_KEY` set, launch hygge in a sample repo and
      ask: "use the task tool with subagent_type=general, description
      'find LICENSE', prompt 'find any file named LICENSE in this
      repo'". While the sub-agent is working, watch the TUI:
      - A new collapsed block appears under the `task` tool call line,
        with header `▸ task[general] · anthropic/<model> · running ·
        <elapsed> · <tokens> · $<cost>` and the description quoted on
        the next line.
      - Press `Ctrl+T` to expand the most recent block. Streamed
        assistant text, tool calls (e.g. `grep`, `read`), and tool
        results appear with a `│` gutter as the sub-agent works.
      - Press `Ctrl+T` again to collapse.
      - When the sub-agent finishes, the chevron flips to `▾` (or stays
        `▸` if collapsed), the state changes to `done`, and the cost /
        token totals freeze at the final values reported by the
        `SubagentCompleted` event.

      After the run returns:
      ```
      ./bin/hygge sessions list
      ```
      The list shows a primary session and a sub-session whose
      `parent_id` points at it. The sub-session's row is tagged with
      kind `subagent` (visible via `sqlite3 ~/.local/state/hygge/
      sessions.db 'select id, parent_id, kind from sessions'`).

- [ ] **Sub-agent model override switches provider.**
      Define a sub-agent type that pins a different provider than the
      one in your active hygge config, then dispatch it.
      ```
      tmp=$(mktemp -d)
      mkdir -p "$tmp/.agents"
      cat > "$tmp/.agents/subagents.toml" <<'EOF'
      [subagents.haiku]
      description = "Cheap & quick recon."
      prompt = "You are a recon sub-agent. Return one final message."
      tools = ["read", "grep", "glob"]
      model = "anthropic/claude-haiku-4-5"
      EOF
      (cd "$tmp" && ../bin/hygge subagents list)
      ```
      `subagents list` shows the new `haiku` type with a `MODEL`
      column reading `anthropic/claude-haiku-4-5`, and `subagents
      show haiku` echoes the same string on its `model:` line. With
      both the parent provider's key AND `ANTHROPIC_API_KEY` set,
      launch hygge in `$tmp` and ask: "use the task tool with
      subagent_type=haiku, description 'sanity', prompt 'list files
      under .'". After it returns:
      ```
      sqlite3 ~/.local/state/hygge/sessions.db \
        'select id, parent_id, model_provider, model_name from sessions order by created_at desc limit 5'
      ```
      The newest sub-session row shows `model_provider = anthropic`
      and `model_name = claude-haiku-4-5`, regardless of which
      provider the parent session used. A type with a malformed
      model (e.g. `model = "anthropic-claude"`) loads with a
      warning in the logs and falls back to the parent's model
      silently at run time.

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

- [ ] **CLAUDE.md at root is loaded.**
      ```
      tmp=$(mktemp -d)
      mkdir -p "$tmp/.git"
      cat > "$tmp/CLAUDE.md" <<'EOF'
      # Project rules (claude-compat)
      Prefer small commits.
      EOF
      (cd "$tmp" && ../bin/hygge context show)
      ```
      Output shows `## Project context` and includes the
      `<!-- source: project/root: CLAUDE.md -->` comment marker.

- [ ] **`hygge context list` summarises every source.**
      With a mix of `~/.agents/AGENTS.md`, `<root>/AGENTS.md`, and
      `<root>/CLAUDE.md` planted, run `./bin/hygge context list`
      and confirm the table contains one row per file with SOURCE
      columns drawn from `user/.agents` and `project/root`. The
      `project/root` rows use project-relative PATH values
      (`AGENTS.md`, `CLAUDE.md`), not absolute paths.

- [ ] **Lazy subdir AGENTS.md is injected on next turn.**
      With a project layout like
      ```
      tmp=$(mktemp -d)
      git init -q "$tmp"
      mkdir -p "$tmp/pkg"
      cat > "$tmp/pkg/AGENTS.md" <<'EOF'
      # pkg-local rules
      All identifiers in this package must be ALL_CAPS.
      EOF
      cat > "$tmp/pkg/code.go" <<'EOF'
      package pkg
      EOF
      ```
      run the TUI with debug logging enabled
      (`HYGGE_LOG=debug ANTHROPIC_API_KEY=... ./bin/hygge` from `$tmp`)
      and ask the agent to read `pkg/code.go`.  After it returns, check
      the debug log for an `agent: lazy context loaded for next turn`
      entry, then ask a follow-up about the package's identifier
      convention — the model should know about the ALL_CAPS rule
      because `pkg/AGENTS.md` rode along in the NEXT turn's system
      prompt.  `hygge context list` should still show only the
      project-root layers (the lazy block is transient, not persisted).

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

- [ ] **`hygge catalog list` shows the embedded snapshot.**
      `HOME=$(mktemp -d) XDG_STATE_HOME=$(mktemp -d) ./bin/hygge catalog list`
      prints a per-provider summary including at least `anthropic`,
      `openai`, and `openrouter` rows with non-zero model counts.

- [ ] **`hygge catalog list anthropic` shows the flagship models.**
      Run the command and confirm `claude-sonnet-4-5`,
      `claude-opus-4-5`, and `claude-haiku-4-5` all appear with a
      `reasoning` capability flag and a 200K (or 1M) context.

- [ ] **`hygge catalog show openai/o3-mini` shows `reasoning: true`.**
      The detail block must include a `reasoning: true` line.  This
      is what wires automatic detection in the openaicompat adapter.

- [ ] **`hygge catalog refresh` writes the on-disk snapshot.**
      `./bin/hygge catalog refresh` prints a `refreshed: N providers /
      M models` summary and creates
      `$XDG_STATE_HOME/hygge/catalog.json`.  Re-running `hygge catalog
      list` now reports source `disk`.

## Test suite

- [ ] **Race detector clean.**
      `mise run test` (which runs `-race`) prints no `DATA RACE` warnings
      and exits 0.

## Known gaps for v0.1

These are deliberately deferred. Do not block v0.1 on them.

- LSP integration (dropped from v0.1 scope).
- Subagents.
- OpenAI / OpenRouter / additional providers.
- Plugins (WASM or subprocess).
- Live theme reload and additional builtin themes.
- Compaction UI.
- Per-message expand for tool results.
- AssistantThinkingDelta rendering.
- History cycling on Up arrow.
- Windows shell support in the `bash` tool.

## v0.2 progress

- MCP client (stdio transport) — shipped. See `hygge mcp list` and the
  MCP section in README.md. Items below cover the manual smoke for it.

### MCP smoke

- [ ] **MCP server config loads from `.agents/mcp.toml`.**
      ```
      tmp=$(mktemp -d)
      mkdir -p "$tmp/.git" "$tmp/.agents"
      cat > "$tmp/.agents/mcp.toml" <<'EOF'
      [[servers]]
      name = "demo"
      command = "/nonexistent/mcp-binary"
      EOF
      (cd "$tmp" && ../bin/hygge mcp list)
      ```
      Output lists `demo` with source `project/.agents`. Status shows
      `failed` (no real binary) — that's expected here; the point is
      that discovery works.

- [ ] **`hygge mcp doctor` reports the file.**
      Same setup as above, then:
      ```
      (cd "$tmp" && ../bin/hygge mcp doctor)
      ```
      Output shows the `.agents/mcp.toml` path with status `ok` and
      a server count of 1.

- [ ] **MCP tool invocable (manual; requires a real MCP binary).**
      Install a real MCP server (e.g. `mcp-server-filesystem`),
      configure it in `~/.agents/mcp.toml`, then `hygge mcp ping
      filesystem` and `hygge mcp tools filesystem`. In the TUI, ask
      the agent to use one of the advertised tools and verify the
      permission prompt fires under the `mcp` category.

### SSE MCP transport smoke

- [ ] **`hygge mcp list` shows transport column for SSE servers.**
      ```
      tmp=$(mktemp -d)
      mkdir -p "$tmp/.git" "$tmp/.agents"
      cat > "$tmp/.agents/mcp.toml" <<'EOF'
      [[servers]]
      name = "linear"
      transport = "sse"
      url = "https://mcp.linear.app/sse"
      [servers.headers]
      Authorization = "Bearer test-token"
      EOF
      (cd "$tmp" && ../bin/hygge mcp list)
      ```
      Output shows `linear` with TRANSPORT column `sse` and STATUS
      `failed` (the token is invalid — that's expected here; transport
      type and config parsing are the focus).

- [ ] **`hygge mcp doctor` parses SSE config.**
      Same setup as above, then:
      ```
      (cd "$tmp" && ../bin/hygge mcp doctor)
      ```
      Output shows the `.agents/mcp.toml` path with status `ok` and
      1 server.

- [ ] **SSE server live round-trip (manual; requires a real SSE MCP key).**
      Configure a hosted SSE MCP server in `~/.config/hygge/mcp.toml`:
      ```toml
      [[servers]]
      name = "linear"
      transport = "sse"
      url = "https://mcp.linear.app/sse"
      [servers.headers]
      Authorization = "Bearer ${LINEAR_API_KEY}"
      ```
      Set `LINEAR_API_KEY` in the environment, then:
      ```
      ./bin/hygge mcp ping linear
      ./bin/hygge mcp tools linear
      ```
      `ping` should print `linear ready (...) — init Xms, ping Yms`.
      `tools` should list the server's advertised tools. If a key is
      not available, verify the unit tests in `internal/mcp/sse_test.go`
      cover the handshake path via `httptest`.

### Reasoning models smoke

- [ ] **`--reasoning` flag visible in help.**
      `./bin/hygge --help` lists `--reasoning` with the
      `off | low | medium | high` vocabulary in the usage text.

- [ ] **Anthropic extended thinking ticks the TUI thinking renderer.**
      With `ANTHROPIC_API_KEY` set, run:
      ```
      ./bin/hygge --reasoning high \
        -p "Think hard then answer: what is the sum of the first ten primes?"
      ```
      (or launch the TUI with `--reasoning high` and ask the same
      question.)  The TUI's thinking renderer streams reasoning
      content alongside the final text and the footer's token totals
      reflect the budget the model spent.

- [ ] **OpenAI o-series uses the reasoning request shape (manual).**
      With `OPENAI_API_KEY` set:
      ```
      ./bin/hygge --reasoning medium --model openai/o4-mini \
        -p "Briefly reason about 1+1 and answer."
      ```
      The request body the adapter sends (visible in
      `~/.local/state/hygge/hygge.log` at debug level) contains
      `max_completion_tokens` and `reasoning_effort`, and lacks
      `temperature` and `max_tokens`.  If no key is available, this
      step is verified by the unit tests in
      `internal/provider/openaicompat` instead.

## v0.3 progress

### Slash commands smoke

- [ ] **Built-in palette filters and completes.**
      Launch `./bin/hygge`, type `/he` in the input. The palette
      shows `/help` highlighted. Press `Tab` — the input fills with
      `/help `. Press `Enter`; the help listing appears as an
      ephemeral notice under the input and lists every built-in.

- [ ] **`/model` reflects and switches.**
      In a running TUI: type `/model` and press Enter. A notice
      reads `current model: <provider>/<model-id>`. Then type
      `/model openrouter/google-gemini-2-5-pro` and press Enter; the
      status bar updates to show the new model name.

- [ ] **`/cost` matches the footer.**
      Run a turn, then `/cost`. The notice shows the same dollar
      figure the footer renders.

- [ ] **Unknown command surfaces a hint.**
      `/foo` followed by Enter produces a notice reading
      `unknown command /foo — try /help`. The TUI does not crash;
      the input is cleared and continues to accept new text.

- [ ] **TOML prompt template loads and runs.**
      Drop a `commands.toml` at `~/.agents/commands.toml`:
      ```toml
      [commands.review]
      description = "Review code"
      prompt = "Review:\n\n{{code}}"
      args = [{ name = "code", required = true }]
      ```
      Launch `./bin/hygge`. Type `/review def foo(): pass` and
      press Enter. The agent receives the rendered template as a
      user message and produces a normal response. `hygge commands
      list` shows `/review` with source `user`.

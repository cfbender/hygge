---
name: hygge
description: One-stop reference for configuring Hygge, diagnosing issues, and writing plugins and skills.
when_to_use: Use when the user asks about Hygge configuration, permissions, MCP servers, plugins, skills, hooks, troubleshooting startup errors, or writing extensions for Hygge.
---

# Hygge — configuration, plugins, and troubleshooting

Hygge is a terminal-based AI coding assistant.  This skill covers every
major extension and configuration surface so you can help users set up,
customise, and debug their Hygge installation.

---

## 1. Configuration files

### 1.1 Global (user) config

Location: `$XDG_CONFIG_HOME/hygge/config.toml`
Default path when XDG is unset: `~/.config/hygge/config.toml`

```toml
[model]
provider = "anthropic"            # required
name     = "claude-sonnet-4-5"   # required
reasoning = "off"                 # "off" | "low" | "medium" | "high"
reasoning_budget = 0              # token cap when reasoning != "off"
small_model = ""                  # optional small/title model
small_provider = ""               # defaults to model.provider

[model.options]
# Provider-specific extras injected into every request.
# api_key = "sk-…"              # overrides env var + auth store

[theme]
name = "claret"                   # built-in theme name or custom name

[catalog]
refresh_interval = "24h"          # how often to refresh the model catalog

[[modes]]
name     = "smart"
provider = "anthropic"
model    = "claude-sonnet-4-5"
prompt   = "file:prompts/smart.md"   # path relative to ~/.config/hygge/

[plugins]
sources = []   # list of "local:/path/to/dir" or registry references

[permissions]
# See §4 below.
```

### 1.2 Project config

Project config is discovered by a walk-up from the current directory to the
first `.git` boundary.  Merge order (later overrides earlier):

1. Global config (`~/.config/hygge/config.toml`)
2. Project config (`.hygge/config.toml` at the project root)

There is no separate `.agents/config.toml` support — only `.hygge/config.toml`
is read for project-level settings.

### 1.3 Environment variable overrides

| Variable | Effect |
|---|---|
| `XDG_CONFIG_HOME` | Overrides `~/.config` |
| `XDG_STATE_HOME` | Overrides `~/.local/state` (sessions DB, auth store) |
| `ANTHROPIC_API_KEY` | API key for Anthropic (beats auth store) |
| `OPENAI_API_KEY` | API key for OpenAI |
| `OPENROUTER_API_KEY` | API key for OpenRouter |
| `MISTRAL_API_KEY` | API key for Mistral |
| `GROQ_API_KEY` | API key for Groq |
| `DEEPSEEK_API_KEY` | API key for DeepSeek |
| `GOOGLE_API_KEY` | API key for Gemini |
| `XAI_API_KEY` | API key for xAI |

---

## 2. Credentials and authentication

Hygge resolves credentials in this order (first non-empty wins):

1. `model.options.api_key` in `config.toml`
2. The provider's canonical env var (e.g. `ANTHROPIC_API_KEY`)
3. The per-machine auth store at `$XDG_STATE_HOME/hygge/auth.json`

Manage the auth store with:

```bash
hygge provider auth          # interactive auth flow
hygge provider auth --reset  # clear stored credential for a provider
```

---

## 3. Permissions

### 3.1 Permission categories

Every tool call belongs to a category; the permission engine prompts the user
when a category is not in the allow-list.

```toml
[permissions]
# Deny a category entirely (never prompt, always block).
deny  = ["shell", "network"]

# Pre-approve a category (never prompt).
allow = ["read", "write"]
```

Common categories: `read`, `write`, `shell`, `network`, `memory`, `task`.

### 3.2 Yolo mode

Pass `--yolo` to a command to bypass configurable permission prompts while
keeping hard-coded secret protections in place.

---

## 4. MCP servers

MCP (Model Context Protocol) servers extend Hygge's tool set via local
stdio processes, SSE endpoints, or HTTP Streamable endpoints.

### 4.1 Config file locations (merged in priority order)

| Priority | File |
|---|---|
| Highest | `<project>/.hygge/mcp.toml` |
| | `<project>/.agents/mcp.toml` |
| | `~/.config/hygge/mcp.toml` |
| Lowest | `~/.agents/mcp.toml` |

### 4.2 Stdio server example

```toml
[[servers]]
name      = "filesystem"
transport = "stdio"
command   = "npx"
args      = ["-y", "@modelcontextprotocol/server-filesystem", "/tmp"]
enabled   = true
```

### 4.3 SSE / HTTP server example

```toml
[[servers]]
name      = "remote-search"
transport = "sse"           # or "http" for Streamable HTTP
url       = "https://search.example.com/sse"
enabled   = true

[servers.headers]
Authorization = "Bearer ${SEARCH_API_KEY}"
```

### 4.4 Diagnostics

```bash
hygge mcp list        # show every configured server and its status
hygge mcp list --all  # include disabled servers
```

---

## 5. Plugins

Plugins extend Hygge with Lua scripts that can register custom tools,
hooks, slash commands, and subagent types.

### 5.1 Auto-load locations

Plugins in these directories are loaded automatically on startup:

- `~/.config/hygge/plugins/<name>/`   — user-global plugins
- `<project>/.hygge/plugins/<name>/`  — project-local plugins

Each plugin directory must contain a `plugin.toml` (metadata) or a
`plugin.lua` (entry point) to be recognised.

### 5.2 Installing a plugin

```bash
hygge plugins install local:/path/to/plugin-dir
hygge plugins install https://registry.example.com/plugin-name
```

Or add to `config.toml`:

```toml
[plugins]
sources = [
  "local:/home/alice/my-plugin",
]
```

### 5.3 Minimal plugin structure

```text
my-plugin/
├── plugin.toml          # metadata
└── plugin.lua           # entry point (required)
```

`plugin.toml`:
```toml
name    = "my-plugin"
version = "0.1.0"
```

`plugin.lua` entry-point skeleton:
```lua
-- Register a custom tool.
hygge.tools.register({
  name        = "my-tool",
  description = "Does something useful.",
  input_schema = { type = "object", properties = {} },
  handler = function(args)
    return { content = "result" }
  end,
})
```

### 5.4 Diagnostics

```bash
hygge plugins list               # show loaded plugins and their status
hygge plugins list --all         # include plugins that failed to load
```

---

## 6. Skills

Skills are markdown files with YAML frontmatter that inject specialised
instructions into the model when invoked via the `skill` tool.

### 6.1 Skill discovery layers (lowest to highest priority)

| Priority | Location |
|---|---|
| Lowest | Built-in skills embedded in the Hygge binary |
| | `~/.claude/skills/` |
| | `~/.agents/skills/` |
| | `~/.config/hygge/skills/` |
| | `<project>/.claude/skills/` (walk-up) |
| | `<project>/.agents/skills/` (walk-up) |
| Highest | `<project>/.hygge/skills/` (walk-up) |

Later layers override earlier ones for the same skill name.

### 6.2 Skill file format

Flat layout (`<name>.md`):
```markdown
---
name: my-skill
description: One-line summary shown in the system prompt.
when_to_use: When to invoke this skill (optional).
---

# Skill body

Full instructions the model receives when the skill is loaded.
```

Directory layout (`.agents`-standard, allows auxiliary files):
```text
<skills-dir>/my-skill/
├── SKILL.md          # frontmatter + body
└── helper.sh         # auxiliary script referenced from SKILL.md
```

### 6.3 Diagnostics

```bash
hygge skills list             # list every loaded skill with its source
hygge skills show <name>      # print the full body of one skill
hygge skills doctor           # report files that failed to parse
```

---

## 7. Hooks

Hooks run shell commands or Lua scripts at named lifecycle points.

### 7.1 Config file locations

Loaded from the same four-layer walk as skills:
`~/.config/hygge/hooks.toml`, `<project>/.hygge/hooks.toml`, etc.

### 7.2 Hook definition example

```toml
[[hooks]]
event   = "pre_tool_call"   # lifecycle event name
command = "echo 'about to call a tool'"
enabled = true

[[hooks]]
event  = "post_session"
script = "file:hooks/audit.lua"
```

Common events: `pre_tool_call`, `post_tool_call`, `pre_session`, `post_session`.

### 7.3 Diagnostics

```bash
hygge hooks list    # list configured hooks and their status
```

---

## 8. Subagents

Subagents are isolated agent instances dispatched by the orchestrator.

### 8.1 Config file locations

`<project>/.hygge/subagents.toml`, `~/.config/hygge/subagents.toml`.

### 8.2 Subagent type definition

```toml
[[types]]
name        = "reviewer"
description = "Code review specialist"
prompt      = "file:prompts/reviewer.md"
tools       = ["read", "search"]
provider    = "anthropic"
model       = "claude-haiku-3-5"
```

---

## 9. Troubleshooting

### 9.1 Bootstrap diagnostics

```bash
hygge --log-level debug ...     # enable verbose debug logging
hygge config explain            # show effective config and its provenance
hygge provider list             # list known providers
hygge provider auth             # check / fix credential status
```

Log files are written to `$XDG_STATE_HOME/hygge/logs/` by default.

### 9.2 Skill not loading

1. Run `hygge skills doctor` — it reports every parse error in the skill directories.
2. Check that the filename stem matches the frontmatter `name`.
3. Ensure the file opens with `---\n` (no BOM, no leading blank lines).
4. Verify the `name` matches `^[a-z][a-z0-9-]{0,63}$`.

### 9.3 MCP server not connecting

1. Run `hygge mcp list` and check the `Error` column.
2. Verify the `command` / `url` is correct and reachable.
3. Test stdio servers independently outside Hygge:
   ```bash
   echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' \
     | npx -y @modelcontextprotocol/server-filesystem /tmp
   ```

### 9.4 Plugin not loading

1. Run `hygge plugins list --all` and check the `Error` column.
2. Confirm the plugin directory contains `plugin.toml` or `plugin.lua`.
3. Run `hygge plugins doctor` for parse-level diagnostics (if available).
4. Check Lua syntax: `lua -c plugin.lua`.

### 9.5 Permission errors

- Unexpected denials: check `[permissions].deny` in `config.toml`.
- Unexpected prompts: add the category to `[permissions].allow`.
- Bypass all prompts for a one-off run: `hygge --yolo`.

### 9.6 Resetting to defaults

```bash
# Remove all user config (keeps auth store intact):
rm "${XDG_CONFIG_HOME:-$HOME/.config}/hygge/config.toml"

# Clear the credential store:
hygge provider auth --reset

# Remove the sessions database (history lost):
rm "${XDG_STATE_HOME:-$HOME/.local/state}/hygge/sessions.db"
```

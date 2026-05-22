---
name: test-hygge
description: Run Hygge inside tmux to verify rendering and exercise interactive flows from an agent. Use when you need to confirm a UI change actually works on screen — splash, modals, key bindings, layout — not just that tests pass.
---

# Test Hygge inside tmux

Hygge is an interactive TUI. The bash tool can't drive it directly — it takes
over the terminal. Wrap it in a detached `tmux` session and you can `send-keys`,
`capture-pane`, and assert on the rendered output.

## When to use this

- A UI change should be sanity-checked against the running app (`go test` only
  proves the code compiles + unit tests pass — it can't tell you the splash
  renders the wordmark in the right place).
- A reported bug only reproduces interactively (e.g. a key binding, a focus
  change, a modal opening order).
- You're verifying themed colors landed correctly — text capture preserves ANSI
  when run with `-e`.

If the change is purely a logic edit covered by unit tests, skip this. Don't
run the TUI when you don't need to.

## Quickstart

```bash
# 1. Build into /tmp so you don't pollute the working tree.
go build -o /tmp/hygge ./cmd/hygge

# 2. Wipe any prior session, start a new detached one at a known size.
tmux kill-session -t hygge 2>/dev/null
tmux new-session -d -s hygge -x 180 -y 50 '/tmp/hygge'

# 3. Wait for the ready marker (the splash wordmark is reliable).
timeout 10 bash -c 'until tmux capture-pane -t hygge -p | grep -q "hygge"; do sleep 0.2; done'

# 4. Capture and inspect.
tmux capture-pane -t hygge -p

# 5. Always tear down.
tmux kill-session -t hygge
```

That's the whole pattern. Everything below is variations on it.

## Choose your HOME carefully

Hygge's behavior at launch depends on what config it finds in `$HOME`:

| HOME setting | What you get |
|---|---|
| Real `$HOME` (just run `/tmp/hygge`) | Skips onboarding, loads your real config, lands on the splash. **Use this for visual checks.** |
| Fresh tmpdir (`HOME=$(mktemp -d) /tmp/hygge`) | Opens the onboarding wizard. Use this when you're testing the onboarding flow, otherwise it's a distraction. |
| A pre-seeded fixture dir | Set up specific config/sessions/themes before launch. Use for repro-able state. |

For UI rendering checks (splash, layout, theming), use the real `$HOME` —
it's the closest to what a user sees.

## Sizing the terminal

The splash, sidebar layout, and chrome all branch on terminal size. Pick a
known-good size and stick with it:

- `-x 180 -y 50` — comfortable full layout with sidebar.
- `-x 100 -y 30` — narrower; sidebar collapses, splash banner shrinks.
- `-x 80 -y 24` — minimum sane size; useful for catching cramped-layout
  regressions.

Don't let tmux inherit your real terminal size — make it explicit so behavior is
reproducible across runs.

## Capturing the screen

```bash
# Plain text (ANSI stripped) — best for grep/text assertions.
tmux capture-pane -t hygge -p

# With ANSI escapes — needed to verify color output.
tmux capture-pane -t hygge -p -e

# Just a row range (1-indexed from top).
tmux capture-pane -t hygge -p -S 10 -E 25
```

`-S` is start line, `-E` is end line. Negative values are relative to the
current line (`-S -10` = last 10 lines of scrollback).

## Driving the app

```bash
# Type literal text.
tmux send-keys -t hygge 'hello world'

# Press named keys. `Enter`, `Escape`, `Tab`, `Up`, `Down`, etc.
tmux send-keys -t hygge 'Enter'
tmux send-keys -t hygge 'Escape'

# Modifier combos.
tmux send-keys -t hygge 'C-c'   # Ctrl+C
tmux send-keys -t hygge 'C-e'   # Ctrl+E (open prompt in external editor)

# Chained.
tmux send-keys -t hygge 'foo' 'Enter'
```

**Wait between an input and the next assertion.** Hygge is event-driven; sends
return instantly but the screen takes a frame to update. Either:

- Poll for the expected change: `timeout 5 bash -c 'until tmux capture-pane -t hygge -p | grep -q "expected"; do sleep 0.2; done'`.
- Or `sleep 0.5` if you only care about steady-state, not the exact moment.

Polling is more reliable than sleep — it returns as soon as the change lands
and fails loudly if it never does.

## Key reference (the ones agents need most)

| Key | What it does |
|---|---|
| `Enter` | Submit the current input / accept a modal selection |
| `Escape` | Dismiss the active modal / cancel a palette / first press of double-Esc interrupt |
| `Tab` | Cycle agent mode (when input is empty) / accept palette completion |
| `Up` / `Down` | Navigate history, palettes, modal selections |
| `C-c` | Cancel a running turn (when busy) / open quit modal |
| `C-l` | Clear the input |
| `C-t` | Cycle reasoning level (off → low → med → high) |
| `C-e` | Open the current prompt in an external editor |
| `C-g` | Follow into the latest sub-agent |
| `pgup` / `pgdown` | Scroll the message viewport |

Slash commands (`/theme`, `/clear`, etc.) work by typing them into the input
and pressing Enter. The slash palette opens automatically after `/`.

## Common smoke flows

### Splash renders + wordmark is present
```bash
tmux kill-session -t hygge 2>/dev/null
tmux new-session -d -s hygge -x 180 -y 50 '/tmp/hygge'
timeout 10 bash -c 'until tmux capture-pane -t hygge -p | grep -q "hygge"; do sleep 0.2; done'
tmux capture-pane -t hygge -p | grep -c "hygge"   # expect 2 (banner + wordmark)
tmux kill-session -t hygge
```

### Theme switch hot-reloads
```bash
tmux send-keys -t hygge '/theme' 'Enter'
timeout 5 bash -c 'until tmux capture-pane -t hygge -p | grep -q "claret"; do sleep 0.2; done'
tmux send-keys -t hygge 'Down' 'Enter'   # pick the next theme in the list
sleep 0.5
tmux capture-pane -t hygge -p   # confirm color escapes changed
```

### Verify colored output (ANSI on)
```bash
tmux capture-pane -t hygge -p -e | grep -oE '\x1b\[38;2;[0-9;]+m' | sort -u | head
# Look for the truecolor SGR sequences corresponding to your theme's
# primary/accent. Catch the case where everything is rendering grayscale
# because Colors map wasn't populated.
```

### Onboarding wizard appears in a fresh HOME
```bash
TMP=$(mktemp -d)
tmux kill-session -t hygge 2>/dev/null
tmux new-session -d -s hygge -x 180 -y 50 "HOME=$TMP /tmp/hygge"
timeout 10 bash -c 'until tmux capture-pane -t hygge -p | grep -q "Welcome to Hygge"; do sleep 0.2; done'
```

## Things to watch out for

- **Always `kill-session` on the way out.** A stray session blocks your next
  `new-session -s hygge`. The `2>/dev/null` on `kill-session` is so the
  command is safe even when there's nothing to kill.
- **Build first, then launch.** A stale binary will quietly run while your
  source edits do nothing. Burn a `/tmp/hygge` rebuild before each capture
  series.
- **`capture-pane -p` strips ANSI by default.** If you're inspecting colors,
  add `-e` or you'll be hunting a ghost.
- **The splash fog animation is real motion.** Frame N and frame N+1 differ —
  diff-based assertions on the splash will be flaky unless you snap the
  animation clock first or assert on stable text like the wordmark and the
  input prompt, not the cloud glyphs.
- **`tmux send-keys` queues instantly — assertions don't.** Always poll
  before grepping for an expected change.

## When this skill isn't the right tool

- For pure logic / non-rendering checks: `go test ./...` and unit tests are
  faster and more reliable.
- For colors specifically, the unit tests under `internal/ui/components/`
  assert against the SGR escape format and don't need a TTY. Look at
  `TestSidebar_BackgroundFill_ReassertedAfterStyledFragments` for the
  pattern.
- For verifying a specific rune appears at a specific (row, col): the
  `View()` of a model in a test is more precise than `tmux capture-pane`
  because there's no terminal width/height ambiguity.

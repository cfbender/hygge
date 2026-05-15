# bubbletea v2 / lipgloss v2 / bubbles v2 — Gotchas

Notes for carpenter and future agents working on UI in this codebase.
We migrated from v1 → v2 at commit `9296d3e`. This file collects gotchas the
agent has hit repeatedly when working with the v2 APIs.

If you're about to edit anything under `internal/ui/`, read this first.

---

## lipgloss.Color is a **function**, not a type

In **lipgloss v1**, `lipgloss.Color` was a string-backed type:
```go
var c lipgloss.Color = "#ff0000"
```

In **lipgloss v2**, it is a function that returns the `color.Color` interface
from `image/color`:
```go
import "image/color"
import "charm.land/lipgloss/v2"

var c color.Color = lipgloss.Color("#ff0000")
// or
var c color.Color = lipgloss.Color("4") // ANSI 4
```

Implications:
- A field that holds a "bubble accent color" must be typed as
  `color.Color` (from `image/color`), **not** `lipgloss.Color`.
- You can't write `lipgloss.Color` as a struct field type.
- To accept a "color" parameter or field, use `color.Color`.
- Style methods like `.Foreground()` / `.Background()` / `.BorderForeground()`
  still accept `color.Color`.

If the type checker complains about `lipgloss.Color` not being a type —
this is why. Switch the type to `color.Color`.

---

## `View()` returns `tea.View`, not `string`

The `tea.Model` interface in v2 requires:
```go
View() tea.View
```

Returning `string` does not satisfy the interface.

For most rendering, return a `tea.View` with just a content string:
```go
return tea.NewView(content)
```

For the **root** App.View() that needs declarative terminal features
(alt screen, mouse mode, cursor, window title):
```go
v := tea.NewView(content)
v.AltScreen = true
v.MouseMode = tea.MouseModeCellMotion
return v
```

Components that are NOT `tea.Model` (just internal renderers whose `View()`
returns a string for embedding into a parent) can keep `View() string` —
only top-level Models need the `tea.View` return type.

---

## Key handling: `tea.KeyPressMsg`, not `tea.KeyMsg`

In v1, `tea.KeyMsg` was a struct you'd match on directly. In v2:
- `tea.KeyMsg` is now an interface covering both presses and releases.
- For typical key handling, match on `tea.KeyPressMsg` instead.

```go
// v1
case tea.KeyMsg:
    switch msg.String() { ... }

// v2
case tea.KeyPressMsg:
    switch msg.String() { ... }
```

Field renames you'll hit:
- `msg.Type` → `msg.Code` (a rune — `tea.KeyEnter`, `'a'`, etc.)
- `msg.Runes` → `msg.Text` (now a string, not `[]rune`)
- `msg.Alt` → `msg.Mod.Contains(tea.ModAlt)`

The constants `tea.KeyCtrlC`, `tea.KeyCtrlG`, `tea.KeyEsc`, `tea.KeyEnter`,
etc. no longer exist. Match on `msg.String()` consistently:
```go
switch msg.String() {
case "ctrl+c": ...
case "ctrl+g": ...
case "esc": ...
case "enter": ...
}
```

**Spacebar gotcha**: `case " ":` → `case "space":`.

`msg.Code` is still the rune `' '` and `msg.Text` is still `" "` — but
`msg.String()` returns `"space"`.

---

## Mouse handling: split by type

In v1, `tea.MouseMsg` was a struct with `Action` / `Button` fields. In v2:
- `tea.MouseMsg` is an interface.
- Coordinates live behind a `.Mouse()` call: `msg.Mouse().X`, `msg.Mouse().Y`.
- Specific message types: `tea.MouseClickMsg`, `tea.MouseReleaseMsg`,
  `tea.MouseWheelMsg`, `tea.MouseMotionMsg`.

Button constants renamed:
- `MouseButtonLeft` → `MouseLeft`
- `MouseButtonRight` → `MouseRight`
- `MouseButtonMiddle` → `MouseMiddle`
- `MouseButtonWheelUp` → `MouseWheelUp`
- `MouseButtonWheelDown` → `MouseWheelDown`

---

## Program options moved to View fields

These program options no longer exist; they moved into the `tea.View`
returned from `View()`:

| Removed option         | Now set as                                |
| ---------------------- | ----------------------------------------- |
| `WithAltScreen()`      | `v.AltScreen = true`                      |
| `WithMouseCellMotion()`| `v.MouseMode = tea.MouseModeCellMotion`   |
| `WithMouseAllMotion()` | `v.MouseMode = tea.MouseModeAllMotion`    |
| `WithReportFocus()`    | `v.ReportFocus = true`                    |
| `WithoutBracketedPaste()` | `v.DisableBracketedPasteMode = true`   |

Similarly, imperative commands like `tea.EnterAltScreen`,
`tea.HideCursor`, `tea.SetWindowTitle(...)` are gone — set the
corresponding `tea.View` field declaratively each frame.

`tea.WithInputTTY()` and `tea.WithANSICompressor()` are obsolete — remove
them.

Renames you'll hit:
- `tea.Sequentially(...)` → `tea.Sequence(...)`
- `tea.WindowSize()` → `tea.RequestWindowSize` (returns `Msg`, not `Cmd`)

---

## bubbles/v2 textarea quirk

`bubbles/v2` textarea inserts cursor escape sequences over the first
character of the placeholder text. Tests that asserted on `"Type a message"`
should expect `"ype a message"` (or use a substring match that skips the
first character).

`FocusedStyle` and `BlurredStyle` setters have been renamed:
```go
// v1
textarea.FocusedStyle = ...
// v2
ta.SetStyles(ta.Styles().With(...))
```

---

## Import paths

- `github.com/charmbracelet/bubbletea` → `charm.land/bubbletea/v2`
- `github.com/charmbracelet/lipgloss` → `charm.land/lipgloss/v2`
- `github.com/charmbracelet/bubbles/<sub>` → `charm.land/bubbles/v2/<sub>`

`go.mod` already pulls all three at v2. Just use the new paths.

`glamour` still pulls in `github.com/charmbracelet/lipgloss` v1 as an
indirect dep — that's fine, it'll linger until glamour ships a v2-compatible
release.

---

## Common stack-trace clues this file applies

- `lipgloss.Color is not a type`
- `cannot use ... (untyped string constant) as tea.MouseButton value`
- `undefined: tea.KeyCtrlC` / `undefined: tea.WithAltScreen`
- `cannot use string as tea.View value`
- "Type a message" not found in textarea render

---

_Last updated after the sidebar slice (commit `f4c7dd0`)._

// Package anim provides a compact colored-runes animation component for use
// in terminal UIs built with bubbletea v2.
//
// The animation cycles through a pool of block-element runes and shifts a
// color gradient across the width on every tick.  Frames are pre-rendered
// into a cache so hot-path rendering never allocates lipgloss styles.
package anim

import (
	"fmt"
	"image/color"
	"math"
	"strings"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	colorful "github.com/lucasb-eyer/go-colorful"

	"github.com/cfbender/hygge/internal/ui/styles"
)

// animCounter provides monotonically-increasing unique IDs for Anim instances.
// Using a global counter avoids the stack-address reuse that could arise from
// fmt.Sprintf("anim-%p", &opts) when the compiler reuses the same stack slot
// for successive calls to New — which caused the compaction anim and a
// subagent anim to share an ID and steal each other's StepMsg.
var animCounter atomic.Uint64

// defaultFrameCount is the number of pre-rendered frames kept in the cache.
const defaultFrameCount = 30

// tickInterval is the visual animation cadence (≈16 fps).
const tickInterval = 62 * time.Millisecond

// runePool is the block-element rune pool used for the animation.
// Chosen for visual density and smooth "liquid" motion in a terminal block.
var runePool = []rune("░▒▓█▓▒░·⋆·")

// StepMsg is the tick message for a specific Anim instance identified by ID.
// Multiple Anim instances use distinct IDs so their ticks never cross-trigger.
type StepMsg struct{ ID string }

// Settings configures an Anim.
type Settings struct {
	// Width is the rendered column count. Default: 8.
	Width int
	// Theme provides gradient colors. Optional; falls back to the built-in ramp.
	Theme *styles.Styles
	// GradFrom is the theme atom for the gradient start. Default: styles.AtomAccent.
	GradFrom styles.Atom
	// GradTo is the theme atom for the gradient end. Default: styles.AtomWarn.
	GradTo styles.Atom
}

// Anim is a pre-rendered, frame-cycling colored-runes spinner.
// Create with New, start with Start, and advance with Update.
//
// Anim is not safe for concurrent use; the bubbletea model owns it.
type Anim struct {
	id     string
	width  int
	frames []string // pre-rendered, len == defaultFrameCount
	frame  int      // current frame index
}

// New creates an Anim from Settings, pre-rendering all frames.
func New(opts Settings) *Anim {
	width := opts.Width
	if width <= 0 {
		width = 8
	}

	from := opts.GradFrom
	if from == "" {
		from = styles.AtomAccent
	}
	to := opts.GradTo
	if to == "" {
		to = styles.AtomWarn
	}

	// Resolve the two gradient endpoint colors.
	c1, c2 := resolveGradientColors(opts.Theme, from, to)

	// Generate a unique instance ID so concurrent Anims don't cross-trigger.
	// Use a monotonic counter rather than &opts (stack address) because Go
	// may reuse the same stack slot across calls, causing ID collisions.
	id := fmt.Sprintf("anim-%d", animCounter.Add(1))

	a := &Anim{
		id:     id,
		width:  width,
		frames: make([]string, defaultFrameCount),
	}
	a.preRender(c1, c2)
	return a
}

// ID returns the unique identifier for this Anim instance.  Use it to
// construct a StepMsg that targets this Anim directly — useful in tests that
// need to drive the animation loop without waiting for a real tea.Tick.
func (a *Anim) ID() string {
	return a.id
}

// Start returns the initial tea.Cmd that begins the animation tick loop.
func (a *Anim) Start() tea.Cmd {
	return a.tick()
}

// Update advances the animation by one frame when the incoming message is
// our own StepMsg.  Returns the Anim and the next tick Cmd (or nil if the
// message was not ours).
func (a *Anim) Update(msg tea.Msg) (*Anim, tea.Cmd) {
	step, ok := msg.(StepMsg)
	if !ok || step.ID != a.id {
		return a, nil
	}
	a.frame = (a.frame + 1) % len(a.frames)
	return a, a.tick()
}

// Render returns the current frame string.
func (a *Anim) Render() string {
	if len(a.frames) == 0 {
		return strings.Repeat("░", a.width)
	}
	return a.frames[a.frame]
}

// tick returns a tea.Cmd that fires a StepMsg for this Anim after one interval.
func (a *Anim) tick() tea.Cmd {
	id := a.id
	return tea.Tick(tickInterval, func(time.Time) tea.Msg {
		return StepMsg{ID: id}
	})
}

// preRender builds all frames and stores them in a.frames.
// The rune at position i in each frame is chosen deterministically from the
// runePool using (frame + i) as the index, giving a "scrolling" rune wave.
// The color at position i shifts the gradient offset by (frame + i) / width
// so colors flow across the width as frames advance.
func (a *Anim) preRender(c1, c2 colorful.Color) {
	pool := runePool
	poolLen := len(pool)
	for f := range a.frames {
		var sb strings.Builder
		for i := 0; i < a.width; i++ {
			// Rune: shift by frame so the pool scrolls left across the width.
			r := pool[(f+i)%poolLen]
			// Color: wrap gradient offset so colors cycle smoothly.
			t := math.Mod(float64(f+i)/float64(a.width), 1.0)
			mixed := c1.BlendHcl(c2, t).Clamped()
			hex := colorToHex(mixed)
			style := lipgloss.NewStyle().Foreground(lipgloss.Color(hex))
			sb.WriteString(style.Render(string(r)))
		}
		a.frames[f] = sb.String()
	}
}

// resolveGradientColors extracts the two colorful.Color values for the
// gradient.  Falls back to a built-in magenta→yellow ramp when the theme
// is nil or a color cannot be resolved.
func resolveGradientColors(t *styles.Styles, from, to styles.Atom) (colorful.Color, colorful.Color) {
	fallbackFrom, _ := colorful.Hex("#c678dd") // soft magenta
	fallbackTo, _ := colorful.Hex("#e5c07b")   // warm yellow

	if t == nil {
		return fallbackFrom, fallbackTo
	}

	c1 := atomToColorful(t, from, fallbackFrom)
	c2 := atomToColorful(t, to, fallbackTo)
	return c1, c2
}

// atomToColorful converts a theme atom's lipgloss color to a colorful.Color.
// Falls back to fb on any failure.
func atomToColorful(t *styles.Styles, a styles.Atom, fb colorful.Color) colorful.Color {
	style := t.Style(a)
	fg := style.GetForeground()
	if fg == nil {
		return fb
	}
	// lipgloss colors stringify to hex, ANSI index, or empty.  We try hex
	// parsing first, then fall back to a small ANSI→hex table.
	hex := colorToString(fg)
	if c, err := colorful.Hex(hex); err == nil {
		return c
	}
	// ANSI index → rough hex approximation.
	if c, ok := ansiToColorful(hex); ok {
		return c
	}
	return fb
}

// colorToString converts a color.Color to its hex string for parsing.
func colorToString(c color.Color) string {
	if c == nil {
		return ""
	}
	r32, g32, b32, _ := c.RGBA()
	// RGBA() returns values in the range [0, 65535]. Shift right by 8 to get
	// [0, 255], then mask to a byte to satisfy the static-analysis check.
	r := uint8((r32 >> 8) & 0xff)
	g := uint8((g32 >> 8) & 0xff)
	b := uint8((b32 >> 8) & 0xff)
	return fmt.Sprintf("#%02x%02x%02x", r, g, b)
}

// colorToHex converts a colorful.Color to a hex string for lipgloss.
func colorToHex(c colorful.Color) string {
	r := uint8(c.R * 255)
	g := uint8(c.G * 255)
	b := uint8(c.B * 255)
	return fmt.Sprintf("#%02x%02x%02x", r, g, b)
}

// ansiToColorful maps a small set of ANSI terminal color indices to
// representative hex values for gradient interpolation.
// The shell theme uses indices 1-8; only those need coverage here.
func ansiToColorful(s string) (colorful.Color, bool) {
	table := map[string]string{
		"0": "#000000", "1": "#e06c75", "2": "#98c379",
		"3": "#e5c07b", "4": "#61afef", "5": "#c678dd",
		"6": "#56b6c2", "7": "#abb2bf", "8": "#5c6370",
		"9": "#be5046", "10": "#98c379", "11": "#d19a66",
		"12": "#61afef", "13": "#c678dd", "14": "#56b6c2",
		"15": "#ffffff",
	}
	hex, ok := table[s]
	if !ok {
		return colorful.Color{}, false
	}
	c, err := colorful.Hex(hex)
	return c, err == nil
}

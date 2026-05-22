package styles

import (
	"image/color"
	"sort"

	"charm.land/lipgloss/v2"
	colorful "github.com/lucasb-eyer/go-colorful"
)

//go:generate go run gen_iterm_themes.go

// terminalTheme is the imported Windows Terminal representation from
// mbadolato/iTerm2-Color-Schemes. It stores the source terminal palette and is
// converted into Hygge's semantic quickStyle palette at load time.
type terminalTheme struct {
	DisplayName string

	Background string
	Foreground string
	Selection  string

	Black  string
	Red    string
	Green  string
	Yellow string
	Blue   string
	Purple string
	Cyan   string
	White  string

	BrightBlack  string
	BrightRed    string
	BrightGreen  string
	BrightYellow string
	BrightBlue   string
	BrightPurple string
	BrightCyan   string
	BrightWhite  string
}

func builtinThemeNames() []string {
	names := make([]string, 0, len(builtinItermThemes)+1)
	names = append(names, "claret")
	for name := range builtinItermThemes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func builtinTheme(name string) (*Styles, bool) {
	if name == "" || name == "claret" {
		return DefaultTheme(), true
	}
	t, ok := builtinItermThemes[name]
	if !ok {
		return nil, false
	}
	s := quickStyle(t.quickStyleOpts())
	s.Name = name
	return &s, true
}

func (t terminalTheme) quickStyleOpts() quickStyleOpts {
	bg := themeColor(t.Background)
	fg := themeColor(t.Foreground)
	blue := themeColor(t.Blue)
	purple := themeColor(t.Purple)
	cyan := themeColor(t.Cyan)
	red := themeColor(t.Red)
	green := themeColor(t.Green)
	yellow := themeColor(t.Yellow)
	white := themeColor(t.White)
	brightBlack := themeColor(t.BrightBlack)
	brightRed := themeColor(t.BrightRed)
	brightGreen := themeColor(t.BrightGreen)
	brightYellow := themeColor(t.BrightYellow)
	brightBlue := themeColor(t.BrightBlue)
	brightPurple := themeColor(t.BrightPurple)
	brightCyan := themeColor(t.BrightCyan)
	brightWhite := themeColor(t.BrightWhite)

	fgSubtle := firstColor(brightWhite, white, fg)
	fgMoreSubtle := firstColor(brightBlack, mixHex(fg, bg, 0.55), fg)
	fgMostSubtle := firstColor(mixHex(fg, bg, 0.72), brightBlack, fg)

	bgLeastVisible := firstColor(mixHex(bg, fg, 0.08), bg)
	bgLessVisible := firstColor(mixHex(bg, fg, 0.14), bg)
	bgMostVisible := firstColor(mixHex(bg, fg, 0.22), bg)

	primary := bestAccent(bg, blue, purple, cyan, brightBlue, brightPurple, brightCyan)
	accent := bestAccent(bg, purple, cyan, blue, brightPurple, brightCyan, brightBlue)
	secondary := bestAccent(bg, yellow, brightYellow, green, cyan)

	return quickStyleOpts{
		primary:   primary,
		secondary: secondary,
		accent:    accent,

		fgBase:       fg,
		fgSubtle:     fgSubtle,
		fgMoreSubtle: fgMoreSubtle,
		fgMostSubtle: fgMostSubtle,

		onPrimary: bestTextOn(primary),

		bgBase:         bg,
		bgLeastVisible: bgLeastVisible,
		bgLessVisible:  bgLessVisible,
		bgMostVisible:  bgMostVisible,

		separator: bgMostVisible,

		destructive:       firstColor(brightRed, red),
		error:             red,
		warning:           yellow,
		warningSubtle:     firstColor(brightYellow, yellow),
		busy:              secondary,
		info:              cyan,
		infoMoreSubtle:    firstColor(brightCyan, cyan),
		infoMostSubtle:    firstColor(mixHex(cyan, bg, 0.4), cyan),
		success:           green,
		successMoreSubtle: firstColor(brightGreen, green),
		successMostSubtle: firstColor(mixHex(green, bg, 0.4), green),
	}
}

func themeColor(hex string) color.Color {
	if hex == "" {
		return nil
	}
	return lipgloss.Color(hex)
}

func firstColor(colors ...color.Color) color.Color {
	for _, c := range colors {
		if c != nil {
			return c
		}
	}
	return lipgloss.Color("#FFFFFF")
}

func bestAccent(bg color.Color, choices ...color.Color) color.Color {
	best := firstColor(choices...)
	bestContrast := 0.0
	for _, c := range choices {
		if c == nil {
			continue
		}
		contrast := contrastRatio(bg, c)
		if contrast > bestContrast {
			best = c
			bestContrast = contrast
		}
	}
	return best
}

func bestTextOn(bg color.Color) color.Color {
	black := lipgloss.Color("#000000")
	white := lipgloss.Color("#FFFFFF")
	if contrastRatio(bg, black) >= contrastRatio(bg, white) {
		return black
	}
	return white
}

func mixHex(a, b color.Color, amount float64) color.Color {
	ca, oka := makeColorful(a)
	cb, okb := makeColorful(b)
	if !oka || !okb {
		return nil
	}
	if amount < 0 {
		amount = 0
	}
	if amount > 1 {
		amount = 1
	}
	return lipgloss.Color(ca.BlendHcl(cb, amount).Hex())
}

func contrastRatio(a, b color.Color) float64 {
	ca, oka := makeColorful(a)
	cb, okb := makeColorful(b)
	if !oka || !okb {
		return 0
	}
	ar, ag, ab := ca.LinearRgb()
	br, bg, bb := cb.LinearRgb()
	la := ar*0.2126 + ag*0.7152 + ab*0.0722
	lb := br*0.2126 + bg*0.7152 + bb*0.0722
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func makeColorful(c color.Color) (colorful.Color, bool) {
	if c == nil {
		return colorful.Color{}, false
	}
	r, g, b, _ := c.RGBA()
	return colorful.Color{R: float64(r) / 0xffff, G: float64(g) / 0xffff, B: float64(b) / 0xffff}, true
}

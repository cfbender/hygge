package ui

import (
	"fmt"
	"image/color"
	"math"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/cfbender/hygge/internal/ui/styles"
)

// Fog banner parameters. Direct port of the ascii-clouds preset
// (https://caidan.dev/portfolio/ascii_clouds), with the hue substituted at
// render time from the active theme accent:
//
//	cs=12  wa=0.15  ws=0.5  ni=0  vi=1  vr=0.45
//	ba=0   ca=1     ts=1    sa=0.5
//	t1=0.25  t2=0.3  t3=0.4  t4=0.5  t5=0.65
//	sd=mp0tew
const (
	fogWaveAmplitude    = 0.15
	fogWaveSpeed        = 0.50
	fogNoiseIntensity   = 0.0
	fogVignetteAmount   = 1.0
	// Bumped from the demo's 0.45 so the visible cloud spreads further toward
	// the panel edges and gives the wordmark room to sit inside the body.
	fogVignetteRadius   = 0.55
	fogBrightnessAdjust = 0.0
	fogContrastAdjust   = 1.0
	fogTimeSpeed        = 1.0
	// Saturation is overridden at render time from the theme's accent colour
	// so the fog reads as the user's theme rather than a fixed pastel.
	fogSaturationFloor = 0.55

	fogThreshold1 = 0.25 // .
	fogThreshold2 = 0.30 // -
	fogThreshold3 = 0.40 // +
	fogThreshold4 = 0.50 // O
	fogThreshold5 = 0.65 // X

	// Seed "mp0tew" hashed with the reference's djb2-style algorithm gives
	// abs(-1069410791) % 10000 = 791. Used as a constant z-offset so the
	// pattern matches the linked demo deterministically.
	fogNoiseSeed = 791.0

	// Terminal cells are roughly 2:1 (h:w). Multiplying x by this makes the
	// noise sample frequency feel visually isotropic.
	fogCellAspect = 0.5
)

// fogGlyph maps a normalised brightness [0,1] to the ASCII glyph for the cell.
func fogGlyph(b float64) rune {
	switch {
	case b < fogThreshold1:
		return 0 // empty
	case b < fogThreshold2:
		return '.'
	case b < fogThreshold3:
		return '-'
	case b < fogThreshold4:
		return '+'
	case b < fogThreshold5:
		return 'O'
	default:
		return 'X'
	}
}

// fogCell is one rendered cell of the fog grid. r==0 means empty.
type fogCell struct {
	r          rune
	cr, cg, cb uint8
}

// renderFogBanner produces a w×h ANSI-coloured fog banner at time t, tinted
// with the theme accent's hue. If label is non-empty, it is overlaid in
// bold accent colour in the bottom-right of the grid.
//
// The fog effect mirrors the ascii_clouds "Red" preset: domain-warped FBM
// noise with a soft threshold and circular vignette, mapped to a small
// glyph ramp and coloured via HSL.
func renderFogBanner(w, h int, t float64, accent color.Color, label string) string {
	if w < 1 || h < 1 {
		return ""
	}

	// Pull H and S from the theme accent so the fog reads as the user's
	// theme. A floor keeps low-saturation themes (greys) from washing out.
	hue, accentSat, _ := rgbToHSL(splitRGB(accent))
	sat := math.Max(accentSat, fogSaturationFloor)

	cells := computeFogCells(w, h, t, hue, sat)
	if label != "" {
		// Bottom-right label in a brighter shade of the accent so it pops
		// against the dimmer cloud body.
		lr, lg, lb := hslToRGB(hue, sat, 0.72)
		overlayLabel(cells, w, h, label, lr, lg, lb)
	}
	return serializeFogCells(cells, w, h)
}

// computeFogCells fills a w×h grid with fog cells. Empty (below threshold)
// cells get r==0 and render as spaces with no styling.
func computeFogCells(w, h int, t float64, hue, sat float64) []fogCell {
	cells := make([]fogCell, w*h)

	cx := float64(w) * 0.5
	cy := float64(h) * 0.5
	// The vignette runs in visual space where rows count for several columns
	// so its bounding ellipse stretches horizontally in cell coords. Pushing
	// past the literal terminal cell ratio (~2.0) makes the cloud read as a
	// wider landscape banner rather than a near-circle.
	const rowToCol = 2.6
	halfDiag := math.Hypot(cx, cy*rowToCol)
	if halfDiag <= 0 {
		halfDiag = 1
	}

	// Scale cell coords to roughly height-normalised noise space, mirroring
	// the reference shader where `uv` is 0..1 in y and 0..aspect in x.
	invH := 1.0 / float64(h)

	// Time scaling matches the reference exactly:
	//   time += dt * timeSpeed  →  passed in as `t`, here multiplied once.
	//   warpTime = time * max(0.025, 0.04 * waveSpeed)
	//   drift    = time * (0.02 + 0.02 * waveSpeed) * (0.3, 0.2)
	//   driftR   = time * (0.016 + 0.016 * waveSpeed) * (0.25, 0.15)
	scaledT := t * fogTimeSpeed
	warpTime := scaledT * math.Max(0.025, 0.04*fogWaveSpeed)
	driftXBase := scaledT * (0.02 + 0.02*fogWaveSpeed)
	driftRXBase := scaledT * (0.016 + 0.016*fogWaveSpeed)
	driftX := driftXBase * 0.3
	driftY := driftXBase * 0.2
	driftRX := driftRXBase * 0.25
	driftRY := driftRXBase * 0.15
	warpZ := warpTime + fogNoiseSeed

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			u := (float64(x) + 0.5) * invH * fogCellAspect
			v := (float64(y) + 0.5) * invH

			// Vignette in visual-space (rows scaled to match column width).
			dx := (float64(x) + 0.5 - cx) / halfDiag
			dy := (float64(y) + 0.5 - cy) * rowToCol / halfDiag
			dist := math.Hypot(dx, dy)

			// First warp layer.
			qx := fbm3(u+driftX, v+driftY, warpZ)
			qy := fbm3(u+driftX+5.2, v+driftY+1.3, warpTime*0.9+fogNoiseSeed)

			// Second warp layer reads from the first.
			rx := fbm3(u+4*qx+1.7+driftRX, v+4*qy+9.2+driftRY, warpTime*0.8+fogNoiseSeed)
			ry := fbm3(u+4*qx+8.3+driftRX, v+4*qy+2.8+driftRY, warpTime*0.7+fogNoiseSeed)

			// Domain-warped sample at 4× frequency.
			ws := fogWaveAmplitude * 1.5
			wx := u + ws*rx + driftX
			wy := v + ws*ry + driftY
			density := fbm3(wx*4, wy*4, warpTime*0.5+fogNoiseSeed)*0.5 + 0.5

			// High-frequency grain.
			grain := snoise3(u*50+driftX*10, v*50+driftY*10, fogNoiseSeed)*0.5 + 0.5
			density += grain * fogNoiseIntensity

			// Soft threshold for billowing edges.
			visible := smoothstep(0.35, 0.70, density)

			// Vignette.
			edgeFade := 1.0 - smoothstep(fogVignetteRadius*0.5, fogVignetteRadius, dist)*fogVignetteAmount
			visible *= edgeFade

			// Brightness / contrast.
			visible = (visible + fogBrightnessAdjust) * fogContrastAdjust
			visible = clamp(visible, 0, 1)

			g := fogGlyph(visible)
			if g == 0 {
				continue
			}

			// HSL lightness curve mirrors the reference glyph shader. We
			// soften the reference's full brightness×color collapse with a
			// floor so even dim cells still register as the theme colour
			// instead of fading to grey/black.
			l := 0.5 + visible*0.3
			cr, cgC, cb := hslToRGB(hue, sat, l)
			dim := 0.45 + 0.55*visible
			r := uint8(float64(cr) * dim)
			gc := uint8(float64(cgC) * dim)
			b := uint8(float64(cb) * dim)
			cells[y*w+x] = fogCell{r: g, cr: r, cg: gc, cb: b}
		}
	}
	return cells
}

// overlayLabel stamps a literal text label into the cloud's bottom-right
// corner. The position is derived from the same vignette geometry that
// shapes the visible cloud, so the label always sits inside the body
// regardless of panel size — not in the vignetted dead zone.
func overlayLabel(cells []fogCell, w, h int, label string, lr, lg, lb uint8) {
	if h < 1 || w < 1 {
		return
	}
	runes := []rune(label)
	if len(runes) > w {
		runes = runes[len(runes)-w:]
	}

	// Recompute the vignette geometry to find where the visible cloud's
	// right edge sits. (Keep this in sync with computeFogCells.)
	const rowToCol = 2.6
	cx := float64(w) * 0.5
	cy := float64(h) * 0.5
	halfDiag := math.Hypot(cx, cy*rowToCol)
	if halfDiag <= 0 {
		halfDiag = 1
	}

	// Place the label two rows up from the bottom so the lowest row (most
	// vignetted) doesn't host text. Compute the right-edge column at that
	// row by solving the vignette ellipse for dist == vignetteRadius.
	row := h - 2
	if row < 0 {
		row = 0
	}
	dy := (float64(row) + 0.5 - cy) * rowToCol / halfDiag
	r2 := fogVignetteRadius*fogVignetteRadius - dy*dy
	if r2 < 0 {
		r2 = 0
	}
	dxEdge := math.Sqrt(r2)
	endF := cx + dxEdge*halfDiag
	end := int(endF)
	if end > w {
		end = w
	}
	start := end - len(runes)
	if start < 0 {
		start = 0
	}
	for i, r := range runes {
		idx := row*w + start + i
		if idx >= len(cells) {
			break
		}
		cells[idx] = fogCell{r: r, cr: lr, cg: lg, cb: lb}
	}
}

// serializeFogCells emits the cell grid as a string with truecolor SGR per
// non-empty run. Empty cells are written as plain spaces so the terminal
// background shows through.
func serializeFogCells(cells []fogCell, w, h int) string {
	var sb strings.Builder
	sb.Grow(w * h * 6)

	for y := 0; y < h; y++ {
		var (
			open       bool
			cr, cg, cb uint8
		)
		for x := 0; x < w; x++ {
			c := cells[y*w+x]
			if c.r == 0 {
				if open {
					sb.WriteString("\x1b[0m")
					open = false
				}
				sb.WriteByte(' ')
				continue
			}
			if !open || c.cr != cr || c.cg != cg || c.cb != cb {
				if open {
					sb.WriteString("\x1b[0m")
				}
				fmt.Fprintf(&sb, "\x1b[38;2;%d;%d;%dm", c.cr, c.cg, c.cb)
				open = true
				cr, cg, cb = c.cr, c.cg, c.cb
			}
			sb.WriteRune(c.r)
		}
		if open {
			sb.WriteString("\x1b[0m")
		}
		if y < h-1 {
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}

// splitRGB converts a color.Color to 8-bit RGB components. Returns a soft
// magenta fallback if c is nil.
func splitRGB(c color.Color) (r, g, b uint8) {
	if c == nil {
		return 0xb0, 0x6a, 0xff
	}
	r32, g32, b32, _ := c.RGBA()
	return uint8((r32 >> 8) & 0xff), uint8((g32 >> 8) & 0xff), uint8((b32 >> 8) & 0xff)
}

// resolveAccentRGB picks the color the fog should be tinted with. The new
// styles.Styles layer (Claret + user themes) is authoritative — its
// WorkingGradFromColor is the theme's brand primary. The legacy
// styles.Styles.AtomAccent is used as a fallback (it resolves to ANSI 5 in the
// shell theme, which is not what user-configured themes intend).
func resolveAccentRGB(s *styles.Styles, t *styles.Styles) color.Color {
	if s != nil && s.WorkingGradFromColor != nil {
		return s.WorkingGradFromColor
	}
	if t != nil {
		if c := t.Style(styles.AtomAccent).GetForeground(); c != nil {
			return c
		}
	}
	return lipgloss.Color("#b06aff")
}

// ---------------------------------------------------------------------------
// Color space conversion
// ---------------------------------------------------------------------------

// rgbToHSL converts 8-bit RGB to HSL with H in degrees [0, 360) and S, L in
// [0, 1].
func rgbToHSL(r, g, b uint8) (h, s, l float64) {
	rf := float64(r) / 255
	gf := float64(g) / 255
	bf := float64(b) / 255
	maxV := math.Max(rf, math.Max(gf, bf))
	minV := math.Min(rf, math.Min(gf, bf))
	l = (maxV + minV) * 0.5
	if maxV == minV {
		return 0, 0, l
	}
	d := maxV - minV
	if l > 0.5 {
		s = d / (2 - maxV - minV)
	} else {
		s = d / (maxV + minV)
	}
	switch maxV {
	case rf:
		h = (gf - bf) / d
		if gf < bf {
			h += 6
		}
	case gf:
		h = (bf-rf)/d + 2
	default:
		h = (rf-gf)/d + 4
	}
	h *= 60
	return h, s, l
}

// hslToRGB converts HSL (H in degrees, S/L in [0,1]) to 8-bit RGB. Matches
// the reference glyph shader's HSL conversion.
func hslToRGB(h, s, l float64) (r, g, b uint8) {
	c := (1 - math.Abs(2*l-1)) * s
	hp := math.Mod(math.Mod(h, 360)+360, 360) / 60
	x := c * (1 - math.Abs(math.Mod(hp, 2)-1))
	var rf, gf, bf float64
	switch {
	case hp < 1:
		rf, gf, bf = c, x, 0
	case hp < 2:
		rf, gf, bf = x, c, 0
	case hp < 3:
		rf, gf, bf = 0, c, x
	case hp < 4:
		rf, gf, bf = 0, x, c
	case hp < 5:
		rf, gf, bf = x, 0, c
	default:
		rf, gf, bf = c, 0, x
	}
	m := l - c*0.5
	return uint8(clamp((rf+m)*255, 0, 255)),
		uint8(clamp((gf+m)*255, 0, 255)),
		uint8(clamp((bf+m)*255, 0, 255))
}

// ---------------------------------------------------------------------------
// Noise primitives
// ---------------------------------------------------------------------------

// fbm3 is 4-octave fractional Brownian motion over snoise3.
func fbm3(x, y, z float64) float64 {
	value := 0.0
	amp := 0.5
	freq := 1.0
	for i := 0; i < 4; i++ {
		value += amp * snoise3(x*freq, y*freq, z*freq)
		amp *= 0.5
		freq *= 2.0
	}
	return value
}

// snoise3 is a Stefan-Gustavson-style 3D simplex noise returning values in
// roughly [-1, 1]. Public-domain numerical method.
func snoise3(x, y, z float64) float64 {
	const (
		F3 = 1.0 / 3.0
		G3 = 1.0 / 6.0
	)
	s := (x + y + z) * F3
	i := math.Floor(x + s)
	j := math.Floor(y + s)
	k := math.Floor(z + s)
	tCorr := (i + j + k) * G3
	X0 := x - (i - tCorr)
	Y0 := y - (j - tCorr)
	Z0 := z - (k - tCorr)

	var i1, j1, k1, i2, j2, k2 float64
	switch {
	case X0 >= Y0 && Y0 >= Z0:
		i1, j1, k1, i2, j2, k2 = 1, 0, 0, 1, 1, 0
	case X0 >= Y0 && X0 >= Z0:
		i1, j1, k1, i2, j2, k2 = 1, 0, 0, 1, 0, 1
	case X0 >= Y0:
		i1, j1, k1, i2, j2, k2 = 0, 0, 1, 1, 0, 1
	case Y0 >= Z0 && X0 >= Z0:
		i1, j1, k1, i2, j2, k2 = 0, 1, 0, 1, 1, 0
	case Y0 >= Z0:
		i1, j1, k1, i2, j2, k2 = 0, 1, 0, 0, 1, 1
	default:
		i1, j1, k1, i2, j2, k2 = 0, 0, 1, 0, 1, 1
	}

	X1 := X0 - i1 + G3
	Y1 := Y0 - j1 + G3
	Z1 := Z0 - k1 + G3
	X2 := X0 - i2 + 2*G3
	Y2 := Y0 - j2 + 2*G3
	Z2 := Z0 - k2 + 2*G3
	X3 := X0 - 1 + 3*G3
	Y3 := Y0 - 1 + 3*G3
	Z3 := Z0 - 1 + 3*G3

	ii := int(i) & 255
	jj := int(j) & 255
	kk := int(k) & 255

	n0 := corner3(X0, Y0, Z0, grad3Index(ii, jj, kk))
	n1 := corner3(X1, Y1, Z1, grad3Index(ii+int(i1), jj+int(j1), kk+int(k1)))
	n2 := corner3(X2, Y2, Z2, grad3Index(ii+int(i2), jj+int(j2), kk+int(k2)))
	n3 := corner3(X3, Y3, Z3, grad3Index(ii+1, jj+1, kk+1))

	return 32 * (n0 + n1 + n2 + n3)
}

func corner3(x, y, z float64, gi int) float64 {
	tt := 0.6 - x*x - y*y - z*z
	if tt < 0 {
		return 0
	}
	tt *= tt
	g := grad3[gi]
	return tt * tt * (g[0]*x + g[1]*y + g[2]*z)
}

// grad3 is the standard 12-edge gradient table used by 3D simplex noise.
var grad3 = [12][3]float64{
	{1, 1, 0}, {-1, 1, 0}, {1, -1, 0}, {-1, -1, 0},
	{1, 0, 1}, {-1, 0, 1}, {1, 0, -1}, {-1, 0, -1},
	{0, 1, 1}, {0, -1, 1}, {0, 1, -1}, {0, -1, -1},
}

// perm is Ken Perlin's reference permutation table, duplicated for wraparound.
var perm = func() [512]int {
	base := [256]int{
		151, 160, 137, 91, 90, 15, 131, 13, 201, 95, 96, 53, 194, 233, 7, 225,
		140, 36, 103, 30, 69, 142, 8, 99, 37, 240, 21, 10, 23, 190, 6, 148,
		247, 120, 234, 75, 0, 26, 197, 62, 94, 252, 219, 203, 117, 35, 11, 32,
		57, 177, 33, 88, 237, 149, 56, 87, 174, 20, 125, 136, 171, 168, 68, 175,
		74, 165, 71, 134, 139, 48, 27, 166, 77, 146, 158, 231, 83, 111, 229, 122,
		60, 211, 133, 230, 220, 105, 92, 41, 55, 46, 245, 40, 244, 102, 143, 54,
		65, 25, 63, 161, 1, 216, 80, 73, 209, 76, 132, 187, 208, 89, 18, 169,
		200, 196, 135, 130, 116, 188, 159, 86, 164, 100, 109, 198, 173, 186, 3, 64,
		52, 217, 226, 250, 124, 123, 5, 202, 38, 147, 118, 126, 255, 82, 85, 212,
		207, 206, 59, 227, 47, 16, 58, 17, 182, 189, 28, 42, 223, 183, 170, 213,
		119, 248, 152, 2, 44, 154, 163, 70, 221, 153, 101, 155, 167, 43, 172, 9,
		129, 22, 39, 253, 19, 98, 108, 110, 79, 113, 224, 232, 178, 185, 112, 104,
		218, 246, 97, 228, 251, 34, 242, 193, 238, 210, 144, 12, 191, 179, 162, 241,
		81, 51, 145, 235, 249, 14, 239, 107, 49, 192, 214, 31, 181, 199, 106, 157,
		184, 84, 204, 176, 115, 121, 50, 45, 127, 4, 150, 254, 138, 236, 205, 93,
		222, 114, 67, 29, 24, 72, 243, 141, 128, 195, 78, 66, 215, 61, 156, 180,
	}
	var p [512]int
	for i := 0; i < 256; i++ {
		p[i] = base[i]
		p[i+256] = base[i]
	}
	return p
}()

func grad3Index(i, j, k int) int {
	return perm[(i+perm[(j+perm[k&255])&255])&255] % 12
}

// ---------------------------------------------------------------------------
// Small math helpers
// ---------------------------------------------------------------------------

func smoothstep(edge0, edge1, x float64) float64 {
	if edge1 == edge0 {
		if x < edge0 {
			return 0
		}
		return 1
	}
	t := (x - edge0) / (edge1 - edge0)
	t = clamp(t, 0, 1)
	return t * t * (3 - 2*t)
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

package bubble

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/cfbender/hygge/internal/ui/theme"
)

// stripANSI is a naive ANSI escape stripper for test assertions.
// It removes ESC[...m sequences so tests can check plain text.
func stripANSI(s string) string {
	var out strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '[' {
			// skip until 'm'
			i += 2
			for i < len(s) && s[i] != 'm' {
				i++
			}
			i++ // consume 'm'
			continue
		}
		out.WriteByte(s[i])
		i++
	}
	return out.String()
}

func TestBubble_RendersWithHeader(t *testing.T) {
	t.Parallel()
	b := Bubble{
		Width:       80,
		HeaderLeft:  "General",
		HeaderRight: "Claude Opus 4.7",
		Body:        "Hello, world!",
		Theme:       theme.ShellTheme(),
	}
	out := b.View()
	plain := stripANSI(out)

	if !strings.Contains(plain, "General") {
		t.Errorf("header left missing: %q", plain)
	}
	if !strings.Contains(plain, "Claude Opus 4.7") {
		t.Errorf("header right missing: %q", plain)
	}
	if !strings.Contains(plain, "Hello, world!") {
		t.Errorf("body missing: %q", plain)
	}
	// Should have a separator row of at least 3 consecutive '─' chars that is
	// distinct from the border top/bottom (those share the same character, but
	// the separator is a full-width inner row).  We check for ≥10 consecutive
	// '─' characters which is only possible for the separator line.
	if !strings.Contains(plain, "──────────") {
		t.Errorf("expected header/body separator (10+ '─') between header and body: %q", plain)
	}
}

func TestBubble_NoHeaderOmitsSeparator(t *testing.T) {
	t.Parallel()
	b := Bubble{
		Width: 80,
		Body:  "just a body",
		Theme: theme.ShellTheme(),
	}
	out := b.View()
	plain := stripANSI(out)

	// The border uses '─' for its top and bottom lines, but a separator row
	// would be a full inner row of '─'.  Check there's no run of ≥10 '─'
	// characters on a non-border line (i.e. inside the bubble).
	// Approach: split into lines and ensure no *inner* line (not the first/last
	// border row) is made entirely of '─'.
	lines := strings.Split(plain, "\n")
	for i := 1; i < len(lines)-1; i++ {
		inner := strings.Trim(lines[i], "│ ")
		if len(inner) >= 10 && strings.Count(inner, "─") == len([]rune(inner)) {
			t.Errorf("unexpected separator row at line %d when header is empty: %q", i, lines[i])
		}
	}
	if !strings.Contains(plain, "just a body") {
		t.Errorf("body missing: %q", plain)
	}
}

func TestBubble_MaxBodyHeight_Truncates(t *testing.T) {
	t.Parallel()
	lines := make([]string, 20)
	for i := range lines {
		lines[i] = "line" + itoa(i)
	}
	b := Bubble{
		Width:         80,
		Body:          strings.Join(lines, "\n"),
		MaxBodyHeight: 5,
		Theme:         theme.ShellTheme(),
	}
	out := b.View()
	plain := stripANSI(out)

	// Lines 0–3 should appear (first 4 of 5 slots; slot 5 = indicator).
	for i := 0; i < 4; i++ {
		if !strings.Contains(plain, "line"+itoa(i)) {
			t.Errorf("expected line%d to be visible: %q", i, plain)
		}
	}
	// Lines 4+ should NOT appear (beyond cap).
	if strings.Contains(plain, "line4\n") {
		t.Errorf("expected line4 to be truncated: %q", plain)
	}
	// Indicator "more" should appear.
	if !strings.Contains(plain, "more") {
		t.Errorf("expected 'more' indicator: %q", plain)
	}
}

func TestBubble_MaxBodyHeight_NoTruncateWhenFits(t *testing.T) {
	t.Parallel()
	b := Bubble{
		Width:         80,
		Body:          "line0\nline1\nline2",
		MaxBodyHeight: 10,
		Theme:         theme.ShellTheme(),
	}
	out := b.View()
	plain := stripANSI(out)

	if strings.Contains(plain, "more") {
		t.Errorf("expected NO 'more' indicator when body fits: %q", plain)
	}
	if !strings.Contains(plain, "line2") {
		t.Errorf("expected all lines visible: %q", plain)
	}
}

func TestBubble_AlignRight_PadsLeft(t *testing.T) {
	t.Parallel()
	b := Bubble{
		Width:       80,
		BubbleWidth: 40,
		Alignment:   AlignRight,
		Body:        "right aligned",
		Theme:       theme.ShellTheme(),
	}
	out := b.View()
	// The output must start with at least one space (left padding).
	if len(out) == 0 || out[0] != ' ' {
		t.Errorf("expected leading space for right-aligned bubble, got: %q", out[:clamp(20, len(out))])
	}
	// Total width should equal b.Width.
	gotW := lipgloss.Width(out)
	if gotW != 80 {
		t.Errorf("expected total width 80, got %d", gotW)
	}
}

func TestBubble_AlignLeft_PadsRight(t *testing.T) {
	t.Parallel()
	b := Bubble{
		Width:       80,
		BubbleWidth: 40,
		Alignment:   AlignLeft,
		Body:        "left aligned",
		Theme:       theme.ShellTheme(),
	}
	out := b.View()
	gotW := lipgloss.Width(out)
	if gotW != 80 {
		t.Errorf("expected output width 80, got %d", gotW)
	}
}

func TestBubble_StyleDistinct_UsesNormalBorder(t *testing.T) {
	t.Parallel()
	b := Bubble{
		Width:    80,
		Body:     "subdued content",
		SubStyle: StyleDistinct,
		Theme:    theme.ShellTheme(),
	}
	// We can only check the output is non-empty and contains the body.
	out := b.View()
	plain := stripANSI(out)
	if !strings.Contains(plain, "subdued content") {
		t.Errorf("body missing in distinct bubble: %q", plain)
	}
}

func TestBubble_AccentColor_Seam(t *testing.T) {
	t.Parallel()
	// Verify that a custom AccentColor is accepted without panic.
	// lipgloss.Color("2") returns color.Color (green ANSI) — simulates
	// per-agent-mode color injection.
	b := Bubble{
		Width:       80,
		Body:        "colored",
		AccentColor: lipgloss.Color("2"),
		Theme:       theme.ShellTheme(),
	}
	out := b.View()
	if !strings.Contains(stripANSI(out), "colored") {
		t.Errorf("body missing with custom accent color: %q", out)
	}
}

func TestBubble_NilTheme_NocrashMonochrome(t *testing.T) {
	t.Parallel()
	b := Bubble{
		Width:       80,
		HeaderLeft:  "Agent",
		HeaderRight: "Model",
		Body:        "content",
		Theme:       nil, // no theme
	}
	out := b.View()
	plain := stripANSI(out)
	if !strings.Contains(plain, "content") {
		t.Errorf("body missing with nil theme: %q", plain)
	}
}

func TestBubble_AutoBubbleWidth(t *testing.T) {
	t.Parallel()
	// BubbleWidth == 0 → auto (~70% of Width, capped 40–100).
	b := Bubble{
		Width: 80,
		Body:  "auto width",
		Theme: theme.ShellTheme(),
	}
	out := b.View()
	// Auto for width=80 → int(80*0.70)=56 bubble width.
	// Total should equal 80.
	gotW := lipgloss.Width(out)
	if gotW != 80 {
		t.Errorf("expected output width 80, got %d", gotW)
	}
}

func TestBubble_ShowTail_RightAligned(t *testing.T) {
	t.Parallel()
	b := Bubble{
		Width:       80,
		BubbleWidth: 40,
		Alignment:   AlignRight,
		Body:        "user message",
		Theme:       theme.ShellTheme(),
		ShowTail:    true,
	}
	out := b.View()
	plain := stripANSI(out)
	if !strings.Contains(plain, "◢") {
		t.Errorf("right-aligned bubble with ShowTail must contain ◢; got:\n%s", plain)
	}
	// The ◣ (left tail) must NOT appear.
	if strings.Contains(plain, "◣") {
		t.Errorf("right-aligned bubble must not contain ◣; got:\n%s", plain)
	}
}

func TestBubble_ShowTail_LeftAligned(t *testing.T) {
	t.Parallel()
	b := Bubble{
		Width:       80,
		BubbleWidth: 40,
		Alignment:   AlignLeft,
		Body:        "assistant message",
		Theme:       theme.ShellTheme(),
		ShowTail:    true,
	}
	out := b.View()
	plain := stripANSI(out)
	if !strings.Contains(plain, "◣") {
		t.Errorf("left-aligned bubble with ShowTail must contain ◣; got:\n%s", plain)
	}
	// The ◢ (right tail) must NOT appear.
	if strings.Contains(plain, "◢") {
		t.Errorf("left-aligned bubble must not contain ◢; got:\n%s", plain)
	}
}

func TestBubble_ShowTailFalse_NoTail(t *testing.T) {
	t.Parallel()
	b := Bubble{
		Width:    80,
		Body:     "no tail",
		Theme:    theme.ShellTheme(),
		ShowTail: false,
	}
	out := b.View()
	plain := stripANSI(out)
	if strings.Contains(plain, "◢") || strings.Contains(plain, "◣") {
		t.Errorf("bubble with ShowTail=false must not contain tail glyphs; got:\n%s", plain)
	}
}

func TestBubble_BackgroundColor_Applied(t *testing.T) {
	t.Parallel()
	// Verify that a BackgroundColor is accepted and the bubble renders without
	// panic.  We can't easily assert the ANSI escape sequence in a unit test,
	// but we verify body text still appears and the output is non-empty.
	b := Bubble{
		Width:           80,
		BubbleWidth:     40,
		Body:            "bg tinted",
		Theme:           theme.ShellTheme(),
		AccentColor:     lipgloss.Color("5"),
		BackgroundColor: lipgloss.Color("53"),
	}
	out := b.View()
	plain := stripANSI(out)
	if !strings.Contains(plain, "bg tinted") {
		t.Errorf("bubble body must be visible with BackgroundColor set; got:\n%s", plain)
	}
}

func TestBubble_BackgroundColorNil_NoBackground(t *testing.T) {
	t.Parallel()
	// When BackgroundColor is nil, rendering must succeed normally.
	b := Bubble{
		Width:       80,
		BubbleWidth: 40,
		Body:        "no bg",
		Theme:       theme.ShellTheme(),
	}
	out := b.View()
	plain := stripANSI(out)
	if !strings.Contains(plain, "no bg") {
		t.Errorf("bubble body missing when BackgroundColor is nil; got:\n%s", plain)
	}
}

// clamp returns min(a, b).
func clamp(a, b int) int {
	if a < b {
		return a
	}
	return b
}

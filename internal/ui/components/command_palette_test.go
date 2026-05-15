package components

import (
	"context"
	"strings"
	"testing"

	"github.com/cfbender/hygge/internal/command"
	"github.com/cfbender/hygge/internal/provider"
	"github.com/cfbender/hygge/internal/session"
	"github.com/cfbender/hygge/internal/ui/theme"
)

// stubApp is the minimum command.App impl needed for the palette
// tests (none of the commands here actually exercise App, but the
// type constraint forces us to provide one).
type stubApp struct{}

func (stubApp) SessionID() string                                         { return "" }
func (stubApp) Model() string                                             { return "" }
func (stubApp) Reasoning() provider.Reasoning                             { return provider.Reasoning{} }
func (stubApp) Cost() float64                                             { return 0 }
func (stubApp) Sessions(context.Context, int) ([]*session.Session, error) { return nil, nil }

func newPaletteRegistry(t *testing.T) *command.Registry {
	t.Helper()
	r := command.New()
	command.RegisterBuiltins(r)
	return r
}

func TestPaletteEmptyWhenNoBuffer(t *testing.T) {
	t.Parallel()
	p := CommandPalette{Width: 60}
	if !p.Empty() {
		t.Error("expected Empty with no matches")
	}
	if p.View() != "" {
		t.Errorf("expected empty view, got %q", p.View())
	}
}

func TestPaletteNoMatchesWithQuery(t *testing.T) {
	t.Parallel()
	p := CommandPalette{
		Width:           60,
		Theme:           theme.ShellTheme(),
		Matches:         nil,
		QueryAfterSlash: "qwerty",
	}
	out := p.View()
	if !strings.Contains(out, "no commands match") {
		t.Errorf("expected no-match hint, got:\n%s", out)
	}
}

func TestPaletteRendersFilteredBuiltins(t *testing.T) {
	t.Parallel()
	r := newPaletteRegistry(t)
	matches := r.LookupPrefix("co")
	if len(matches) < 2 {
		t.Fatalf("setup: want >=2 matches for 'co', got %d", len(matches))
	}
	p := CommandPalette{
		Width:           60,
		Theme:           theme.ShellTheme(),
		Matches:         matches,
		Highlight:       0,
		QueryAfterSlash: "co",
	}
	out := p.View()
	for _, want := range []string{"/compact", "/cost"} {
		if !strings.Contains(out, want) {
			t.Errorf("palette missing %q in:\n%s", want, out)
		}
	}
}

func TestPaletteRendersFuzzyBuiltins(t *testing.T) {
	t.Parallel()
	r := newPaletteRegistry(t)
	matches := r.LookupPrefix("cpct")
	p := CommandPalette{
		Width:           60,
		Theme:           theme.ShellTheme(),
		Matches:         matches,
		Highlight:       0,
		QueryAfterSlash: "cpct",
	}
	out := p.View()
	if !strings.Contains(out, "/compact") {
		t.Errorf("palette missing fuzzy match /compact in:\n%s", out)
	}
}

func TestPaletteHighlightMarker(t *testing.T) {
	t.Parallel()
	r := newPaletteRegistry(t)
	matches := r.LookupPrefix("co")
	p := CommandPalette{
		Width:     60,
		Theme:     theme.ShellTheme(),
		Matches:   matches,
		Highlight: 1, // /cost
	}
	out := p.View()
	// The highlighted row should contain the row marker.
	lines := strings.Split(out, "\n")
	var marked []string
	for _, l := range lines {
		if strings.Contains(l, "▶") {
			marked = append(marked, l)
		}
	}
	if len(marked) != 1 {
		t.Fatalf("expected exactly 1 highlighted row, got %d in:\n%s", len(marked), out)
	}
	if !strings.Contains(marked[0], "/cost") {
		t.Errorf("highlight marker should be on /cost, got %q", marked[0])
	}
}

func TestPaletteOverflowIndicator(t *testing.T) {
	t.Parallel()
	r := newPaletteRegistry(t)
	matches := r.LookupPrefix("") // every built-in
	if len(matches) <= commandPaletteMaxRows {
		t.Skipf("need >%d matches to test overflow; got %d", commandPaletteMaxRows, len(matches))
	}
	p := CommandPalette{
		Width:   60,
		Theme:   theme.ShellTheme(),
		Matches: matches,
	}
	out := p.View()
	if !strings.Contains(out, "more") {
		t.Errorf("expected +N more indicator in:\n%s", out)
	}
}

func TestPaletteHandlesNilTheme(t *testing.T) {
	t.Parallel()
	r := newPaletteRegistry(t)
	p := CommandPalette{
		Width:   60,
		Matches: r.LookupPrefix("h"),
	}
	out := p.View()
	if !strings.Contains(out, "/help") {
		t.Errorf("nil theme should still render names, got:\n%s", out)
	}
}

func TestPaletteHighlightOutOfRangeIsClamped(t *testing.T) {
	t.Parallel()
	r := newPaletteRegistry(t)
	matches := r.LookupPrefix("co")
	p := CommandPalette{
		Width:     60,
		Theme:     theme.ShellTheme(),
		Matches:   matches,
		Highlight: 99,
	}
	out := p.View()
	// No "▶" marker should appear because the index is out of range.
	if strings.Contains(out, "▶") {
		t.Errorf("out-of-range Highlight should clamp to no marker, got:\n%s", out)
	}
}

func TestPalette_RuntimeAppContract(t *testing.T) {
	t.Parallel()
	// Compile-time sanity that stubApp satisfies command.App.  If
	// the App interface shape changes, this will fail to compile
	// and surface in this file.
	var _ command.App = stubApp{}
}

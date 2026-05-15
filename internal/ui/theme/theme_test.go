package theme

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// writeTOML writes a TOML file at path, creating parent directories as needed.
func writeTOML(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// hermeticOpts returns LoadOptions pointing into tmp, with no real HOME reads.
func hermeticOpts(tmp string) LoadOptions {
	return LoadOptions{
		ConfigHome: filepath.Join(tmp, ".config"),
		HomeDir:    tmp,
	}
}

// writeSingleTheme writes a theme file under <configHome>/hygge/themes/<name>.toml.
func writeSingleTheme(t *testing.T, opts LoadOptions, name, content string) {
	t.Helper()
	path := filepath.Join(opts.ConfigHome, "hygge", "themes", name+".toml")
	writeTOML(t, path, content)
}

// fullThemeTOML returns a minimal valid TOML theme string with the given name.
func fullThemeTOML(name string) string {
	return `name = "` + name + `"
[colors]
primary        = "#7AA2F7"
accent         = "5"
muted          = "ansi:8"
success        = "#9ECE6A"
warn           = "#E0AF68"
error          = "#F7768E"
"code.fg"      = "7"
"code.bg"      = ""
"diff.add.bg"  = "22"
"diff.del.bg"  = "52"
"statusbar.fg" = "15"
"statusbar.bg" = "8"
"modal.bg"     = ""
"modal.border" = "8"
"bubble.border"         = "5"
"bubble.border.distinct" = "8"
"bubble.header"         = "5"
"bubble.header.muted"   = "8"
"bubble.body.muted"     = "8"
"bubble.user.border"    = "4"
"bubble.agent.border"   = "1"
`
}

// ---------------------------------------------------------------------------
// 1. ShellTheme completeness
// ---------------------------------------------------------------------------

func TestShellTheme_Complete(t *testing.T) {
	sh := ShellTheme()
	for _, a := range AllAtoms() {
		if _, ok := sh.Colors[a]; !ok {
			t.Errorf("ShellTheme missing atom %q", a)
		}
	}
}

// ---------------------------------------------------------------------------
// 2. Load("shell", ...) returns shell theme without disk I/O
// ---------------------------------------------------------------------------

func TestLoad_ShellByName(t *testing.T) {
	// Use a non-existent configHome — if any disk I/O occurs the test will
	// not fail on its own, but confirms the path is taken correctly.
	opts := LoadOptions{ConfigHome: "/nonexistent-path", HomeDir: "/nonexistent"}
	th, err := Load("shell", opts)
	if err != nil {
		t.Fatalf("Load(shell): %v", err)
	}
	if th.Name != "shell" {
		t.Errorf("Name: got %q, want shell", th.Name)
	}
}

// ---------------------------------------------------------------------------
// 3. Load("", ...) returns shell theme
// ---------------------------------------------------------------------------

func TestLoad_EmptyNameReturnsShell(t *testing.T) {
	opts := LoadOptions{ConfigHome: "/nonexistent-path", HomeDir: "/nonexistent"}
	th, err := Load("", opts)
	if err != nil {
		t.Fatalf("Load(empty): %v", err)
	}
	if th.Name != "shell" {
		t.Errorf("Name: got %q, want shell", th.Name)
	}
}

// ---------------------------------------------------------------------------
// 4. Load("nonexistent", ...) returns ErrThemeNotFound
// ---------------------------------------------------------------------------

func TestLoad_NonexistentReturnsErrThemeNotFound(t *testing.T) {
	tmp := t.TempDir()
	opts := hermeticOpts(tmp)

	_, err := Load("nonexistent", opts)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrThemeNotFound) {
		t.Errorf("expected ErrThemeNotFound, got %T: %v", err, err)
	}
}

// ---------------------------------------------------------------------------
// 5. Valid TOML round-trip (valid_full.toml)
// ---------------------------------------------------------------------------

func TestLoad_ValidFullRoundTrip(t *testing.T) {
	data, err := os.ReadFile("testdata/valid_full.toml")
	if err != nil {
		t.Fatalf("read testdata/valid_full.toml: %v", err)
	}
	th, err := parseTOMLTheme(data)
	if err != nil {
		t.Fatalf("parseTOMLTheme: %v", err)
	}
	if th.Name != "midnight" {
		t.Errorf("Name: got %q, want midnight", th.Name)
	}

	// primary should be hex #7AA2F7.
	c := th.Colors[AtomPrimary]
	if c.kind != colorKindHex || c.raw != "#7AA2F7" {
		t.Errorf("primary: got kind=%v raw=%q, want hex #7AA2F7", c.kind, c.raw)
	}

	// accent should be ANSI 5 (plain digit).
	c = th.Colors[AtomAccent]
	if c.kind != colorKindANSI || c.raw != "5" {
		t.Errorf("accent: got kind=%v raw=%q, want ansi 5", c.kind, c.raw)
	}

	// muted should be ANSI 8 (ansi:N form).
	c = th.Colors[AtomMuted]
	if c.kind != colorKindANSI || c.raw != "8" {
		t.Errorf("muted: got kind=%v raw=%q, want ansi 8", c.kind, c.raw)
	}

	// code.fg should be inherit:primary.
	c = th.Colors[AtomCodeFg]
	if c.kind != colorKindInherit || c.inheritAtom != AtomPrimary {
		t.Errorf("code.fg: got kind=%v inheritAtom=%q, want inherit:primary", c.kind, c.inheritAtom)
	}

	// code.bg should be default.
	c = th.Colors[AtomCodeBg]
	if !c.IsDefault() {
		t.Errorf("code.bg: expected default, got %+v", c)
	}

	// Every atom must be present.
	for _, a := range AllAtoms() {
		if _, ok := th.Colors[a]; !ok {
			t.Errorf("atom %q missing after round-trip", a)
		}
	}
}

// ---------------------------------------------------------------------------
// 6. Color form parsing
// ---------------------------------------------------------------------------

func TestParseColor_Forms(t *testing.T) {
	cases := []struct {
		name      string
		input     string
		wantKind  colorKind
		wantRaw   string
		wantAtom  Atom
		wantError bool
	}{
		{"empty", "", colorKindDefault, "", "", false},
		{"hex6", "#7AA2F7", colorKindHex, "#7AA2F7", "", false},
		{"hex3", "#ABC", colorKindHex, "#ABC", "", false},
		{"plain digit", "5", colorKindANSI, "5", "", false},
		{"plain multi-digit", "22", colorKindANSI, "22", "", false},
		{"ansi prefix", "ansi:8", colorKindANSI, "8", "", false},
		{"ansi prefix 3-digit", "ansi:255", colorKindANSI, "255", "", false},
		{"inherit", "inherit:primary", colorKindInherit, "", AtomPrimary, false},
		{"inherit dotted", "inherit:code.fg", colorKindInherit, "", AtomCodeFg, false},
		{"bad word", "purple", 0, "", "", true},
		{"bad hex short", "#GG", 0, "", "", true},
		{"bad hex len", "#12345", 0, "", "", true},
		{"ansi bad", "ansi:notnum", 0, "", "", true},
		{"inherit empty", "inherit:", 0, "", "", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, err := parseColor(AtomPrimary, tc.input)
			if tc.wantError {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				var ice *ErrInvalidColor
				if !errors.As(err, &ice) {
					t.Errorf("expected *ErrInvalidColor, got %T: %v", err, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if c.kind != tc.wantKind {
				t.Errorf("kind: got %v, want %v", c.kind, tc.wantKind)
			}
			if tc.wantRaw != "" && c.raw != tc.wantRaw {
				t.Errorf("raw: got %q, want %q", c.raw, tc.wantRaw)
			}
			if tc.wantAtom != "" && c.inheritAtom != tc.wantAtom {
				t.Errorf("inheritAtom: got %q, want %q", c.inheritAtom, tc.wantAtom)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 7. Inherit chain: code.fg inherits primary
// ---------------------------------------------------------------------------

func TestStyle_InheritChain(t *testing.T) {
	th := &Theme{
		Name: "test",
		Colors: map[Atom]Color{
			AtomPrimary: {kind: colorKindHex, raw: "#7AA2F7"},
			AtomCodeFg:  {kind: colorKindInherit, inheritAtom: AtomPrimary},
		},
	}

	style := th.Style(AtomCodeFg)
	// The style should have a foreground set to #7AA2F7.
	// lipgloss.Style.GetForeground() returns the color set on the style.
	got := style.GetForeground()
	want := lipgloss.Color("#7AA2F7")
	if got != want {
		t.Errorf("Style(code.fg): foreground = %v, want %v", got, want)
	}
}

// ---------------------------------------------------------------------------
// 8. Inherit cycle: primary <-> accent
// ---------------------------------------------------------------------------

func TestParseTOMLTheme_InheritCycle(t *testing.T) {
	content := fullThemeTOML("cycle-test")
	// Override primary and accent to create a cycle.
	content = strings.ReplaceAll(content, `primary        = "#7AA2F7"`, `primary = "inherit:accent"`)
	content = strings.ReplaceAll(content, `accent         = "5"`, `accent = "inherit:primary"`)

	_, err := parseTOMLTheme([]byte(content))
	if err == nil {
		t.Fatal("expected ErrInheritCycle, got nil")
	}
	if !errors.Is(err, ErrInheritCycle) {
		t.Errorf("expected ErrInheritCycle, got %T: %v", err, err)
	}
}

// ---------------------------------------------------------------------------
// 9. Inherit chain too deep (>= maxInheritDepth)
// ---------------------------------------------------------------------------

func TestParseTOMLTheme_InheritTooDeep(t *testing.T) {
	// Build a chain: primary -> accent -> muted -> success -> warn -> error ->
	// code.fg -> code.bg -> diff.add.bg (9 hops, exceeds maxInheritDepth=8)
	//
	// We override these atoms to form a chain, keeping remaining atoms valid.
	content := `name = "deep"
[colors]
primary        = "inherit:accent"
accent         = "inherit:muted"
muted          = "inherit:success"
success        = "inherit:warn"
warn           = "inherit:error"
error          = "inherit:code.fg"
"code.fg"      = "inherit:code.bg"
"code.bg"      = "inherit:diff.add.bg"
"diff.add.bg"  = "inherit:diff.del.bg"
"diff.del.bg"  = "#3D2C30"
"statusbar.fg" = "15"
"statusbar.bg" = "8"
"modal.bg"     = ""
"modal.border" = "8"
"bubble.border"         = "5"
"bubble.border.distinct" = "8"
"bubble.header"         = "5"
"bubble.header.muted"   = "8"
"bubble.body.muted"     = "8"
"bubble.user.border"    = "4"
"bubble.agent.border"   = "1"
`
	_, err := parseTOMLTheme([]byte(content))
	if err == nil {
		t.Fatal("expected error for deep inherit chain, got nil")
	}
	if !errors.Is(err, ErrInheritCycle) {
		t.Errorf("expected ErrInheritCycle, got %T: %v", err, err)
	}
}

// ---------------------------------------------------------------------------
// 10. Missing atoms → ErrIncompleteTheme
// ---------------------------------------------------------------------------

func TestParseTOMLTheme_MissingAtoms(t *testing.T) {
	data, err := os.ReadFile("testdata/valid_minimal.toml")
	if err != nil {
		t.Fatalf("read testdata: %v", err)
	}
	// Remove the primary line.
	content := strings.ReplaceAll(string(data), "primary       = \"4\"\n", "")

	_, err = parseTOMLTheme([]byte(content))
	if err == nil {
		t.Fatal("expected ErrIncompleteTheme, got nil")
	}

	var ie *ErrIncompleteTheme
	if !errors.As(err, &ie) {
		t.Fatalf("expected *ErrIncompleteTheme, got %T: %v", err, err)
	}
	// The missing atom list should contain "primary".
	found := false
	for _, a := range ie.Missing {
		if a == AtomPrimary {
			found = true
		}
	}
	if !found {
		t.Errorf("ErrIncompleteTheme.Missing does not contain AtomPrimary: %v", ie.Missing)
	}
	// Error message should mention the missing atom.
	if !strings.Contains(err.Error(), "primary") {
		t.Errorf("error message should mention 'primary': %q", err.Error())
	}
}

// ---------------------------------------------------------------------------
// 11. Unknown atoms → ErrUnknownAtom
// ---------------------------------------------------------------------------

func TestParseTOMLTheme_UnknownAtom(t *testing.T) {
	data, err := os.ReadFile("testdata/bad_unknown_atom.toml")
	if err != nil {
		t.Fatalf("read testdata: %v", err)
	}

	_, err = parseTOMLTheme(data)
	if err == nil {
		t.Fatal("expected ErrUnknownAtom, got nil")
	}

	var ua *ErrUnknownAtom
	if !errors.As(err, &ua) {
		t.Fatalf("expected *ErrUnknownAtom, got %T: %v", err, err)
	}
	found := false
	for _, a := range ua.Atoms {
		if a == "highlight" {
			found = true
		}
	}
	if !found {
		t.Errorf("ErrUnknownAtom.Atoms should contain 'highlight': %v", ua.Atoms)
	}
	if !strings.Contains(err.Error(), "highlight") {
		t.Errorf("error message should mention 'highlight': %q", err.Error())
	}
}

// ---------------------------------------------------------------------------
// 12. Bad color value → ErrInvalidColor with atom name and bad value
// ---------------------------------------------------------------------------

func TestParseTOMLTheme_BadColor(t *testing.T) {
	data, err := os.ReadFile("testdata/bad_color.toml")
	if err != nil {
		t.Fatalf("read testdata: %v", err)
	}

	_, err = parseTOMLTheme(data)
	if err == nil {
		t.Fatal("expected ErrInvalidColor, got nil")
	}

	var ice *ErrInvalidColor
	if !errors.As(err, &ice) {
		t.Fatalf("expected *ErrInvalidColor, got %T: %v", err, err)
	}
	if ice.Atom != AtomPrimary {
		t.Errorf("ErrInvalidColor.Atom: got %q, want %q", ice.Atom, AtomPrimary)
	}
	if ice.Value != "purple" {
		t.Errorf("ErrInvalidColor.Value: got %q, want 'purple'", ice.Value)
	}
	if !strings.Contains(err.Error(), "purple") {
		t.Errorf("error should mention value 'purple': %q", err.Error())
	}
	if !strings.Contains(err.Error(), "primary") {
		t.Errorf("error should mention atom 'primary': %q", err.Error())
	}
}

// ---------------------------------------------------------------------------
// 13. AllAtoms() is stable with length 14
// ---------------------------------------------------------------------------

func TestAllAtoms_Stable(t *testing.T) {
	atoms := AllAtoms()
	if len(atoms) != 21 {
		t.Errorf("AllAtoms(): len = %d, want 21", len(atoms))
	}

	// Exact order must match the const list.
	expected := []Atom{
		AtomPrimary, AtomAccent, AtomMuted, AtomSuccess, AtomWarn, AtomError,
		AtomCodeBg, AtomCodeFg,
		AtomDiffAddBg, AtomDiffDelBg,
		AtomStatusBarBg, AtomStatusBarFg,
		AtomModalBg, AtomModalBorder,
		AtomBubbleBorder, AtomBubbleBorderDistinct,
		AtomBubbleHeader, AtomBubbleHeaderMuted, AtomBubbleBodyMuted,
		AtomBubbleUserBorder, AtomBubbleAgentBorder,
	}
	for i, want := range expected {
		if atoms[i] != want {
			t.Errorf("AllAtoms()[%d]: got %q, want %q", i, atoms[i], want)
		}
	}

	// Second call must return the same order.
	atoms2 := AllAtoms()
	for i := range atoms {
		if atoms[i] != atoms2[i] {
			t.Errorf("AllAtoms() not stable at index %d: %q vs %q", i, atoms[i], atoms2[i])
		}
	}

	// Mutating the returned slice must not affect the next call.
	atoms[0] = "mutated"
	atoms3 := AllAtoms()
	if atoms3[0] != AtomPrimary {
		t.Error("AllAtoms() returned a shared slice that was mutated")
	}
}

// ---------------------------------------------------------------------------
// 14. FormatTheme(ShellTheme()) matches golden file
// ---------------------------------------------------------------------------

func TestFormatTheme_ShellGolden(t *testing.T) {
	goldenPath := "testdata/shell_format.golden"
	goldenData, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden file %s: %v", goldenPath, err)
	}

	got := FormatTheme(ShellTheme())
	want := string(goldenData)

	if got != want {
		t.Errorf("FormatTheme(ShellTheme()) mismatch.\nGot:\n%s\nWant:\n%s", got, want)
	}
}

// ---------------------------------------------------------------------------
// 15. Style(a) for empty-color atom returns blank style
// ---------------------------------------------------------------------------

func TestStyle_EmptyColorAtom(t *testing.T) {
	sh := ShellTheme()

	// code.bg and modal.bg are empty/default in the shell theme.
	// lipgloss.Style.GetForeground/GetBackground return lipgloss.NoColor{} when unset.
	for _, a := range []Atom{AtomCodeBg, AtomModalBg} {
		style := sh.Style(a)
		// Neither Foreground nor Background should be set.
		if fg := style.GetForeground(); fg != (lipgloss.NoColor{}) {
			t.Errorf("Style(%q).GetForeground(): got %T(%v), want NoColor{}", a, fg, fg)
		}
		if bg := style.GetBackground(); bg != (lipgloss.NoColor{}) {
			t.Errorf("Style(%q).GetBackground(): got %T(%v), want NoColor{}", a, bg, bg)
		}
	}
}

// ---------------------------------------------------------------------------
// Additional: Load from disk (user theme)
// ---------------------------------------------------------------------------

func TestLoad_UserThemeFromDisk(t *testing.T) {
	tmp := t.TempDir()
	opts := hermeticOpts(tmp)
	writeSingleTheme(t, opts, "midnight", fullThemeTOML("midnight"))

	th, err := Load("midnight", opts)
	if err != nil {
		t.Fatalf("Load(midnight): %v", err)
	}
	if th.Name != "midnight" {
		t.Errorf("Name: got %q, want midnight", th.Name)
	}
}

// ---------------------------------------------------------------------------
// Additional: BlockStyle combines fg and bg
// ---------------------------------------------------------------------------

func TestBlockStyle_CombinesFgAndBg(t *testing.T) {
	sh := ShellTheme()
	style := sh.BlockStyle(AtomCodeFg, AtomCodeBg)
	// code.fg is ansi:7 → should have Foreground set.
	if fg := style.GetForeground(); fg != lipgloss.Color("7") {
		t.Errorf("BlockStyle(code.fg, code.bg).GetForeground(): got %T(%v), want lipgloss.Color(7)", fg, fg)
	}
	// code.bg is default → Background should not be set (NoColor{}).
	if bg := style.GetBackground(); bg != (lipgloss.NoColor{}) {
		t.Errorf("BlockStyle(code.fg, code.bg).GetBackground(): got %T(%v), want NoColor{}", bg, bg)
	}
}

// ---------------------------------------------------------------------------
// Additional: Style sets Background for .bg atoms
// ---------------------------------------------------------------------------

func TestStyle_BackgroundAtom(t *testing.T) {
	sh := ShellTheme()
	// statusbar.bg is ansi:8 — must be set as Background.
	style := sh.Style(AtomStatusBarBg)
	if bg := style.GetBackground(); bg != lipgloss.Color("8") {
		t.Errorf("Style(statusbar.bg).GetBackground(): got %T(%v), want lipgloss.Color(8)", bg, bg)
	}
	if fg := style.GetForeground(); fg != (lipgloss.NoColor{}) {
		t.Errorf("Style(statusbar.bg).GetForeground(): got %T(%v), want NoColor{}", fg, fg)
	}
}

// ---------------------------------------------------------------------------
// Additional: Color.String() forms
// ---------------------------------------------------------------------------

func TestColorString(t *testing.T) {
	cases := []struct {
		c    Color
		want string
	}{
		{Color{kind: colorKindDefault}, "(default)"},
		{Color{kind: colorKindANSI, raw: "4"}, "ansi:4"},
		{Color{kind: colorKindHex, raw: "#7AA2F7"}, "#7AA2F7"},
		{Color{kind: colorKindInherit, inheritAtom: AtomPrimary}, "inherit:primary"},
	}
	for _, tc := range cases {
		if got := tc.c.String(); got != tc.want {
			t.Errorf("Color.String(): got %q, want %q", got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// Additional: valid_minimal.toml loads without error
// ---------------------------------------------------------------------------

func TestLoad_ValidMinimal(t *testing.T) {
	data, err := os.ReadFile("testdata/valid_minimal.toml")
	if err != nil {
		t.Fatalf("read testdata/valid_minimal.toml: %v", err)
	}
	th, err := parseTOMLTheme(data)
	if err != nil {
		t.Fatalf("parseTOMLTheme(valid_minimal): %v", err)
	}
	if len(th.Colors) != len(AllAtoms()) {
		t.Errorf("Colors len: got %d, want %d", len(th.Colors), len(AllAtoms()))
	}
}

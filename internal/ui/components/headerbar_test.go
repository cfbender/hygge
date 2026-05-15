package components

import (
	"strings"
	"testing"

	"github.com/cfbender/hygge/internal/ui/theme"
)

func TestHeaderBar_BasicLayout(t *testing.T) {
	t.Parallel()
	h := HeaderBar{
		Width:       120,
		AppName:     "Hygge",
		Version:     "v0.4",
		Profile:     "default",
		ProjectPath: "/Users/cfb/code/github/hygge",
		GitBranch:   "main",
		CtxPercent:  0.10,
		CostUSD:     0.0042,
		Theme:       theme.ShellTheme(),
		NerdFonts:   false,
		HomeDir:     "/Users/cfb",
	}
	out := h.View()
	for _, want := range []string{"Hygge", "v0.4", "profile: default", "~/code/github/hygge", ":main", "10% ctx", "$0.0042"} {
		if !strings.Contains(out, want) {
			t.Errorf("header missing %q in:\n%s", want, out)
		}
	}
}

func TestHeaderBar_NerdFonts_GitGlyph(t *testing.T) {
	t.Parallel()
	h := HeaderBar{
		Width:     80,
		AppName:   "Hygge",
		Version:   "v0.4",
		Profile:   "default",
		GitBranch: "main",
		Theme:     theme.ShellTheme(),
		NerdFonts: true,
	}
	out := h.View()
	if !strings.Contains(out, nerdFontBranch) {
		t.Errorf("nerd font glyph missing (NerdFonts=true): %q", out)
	}
	if !strings.Contains(out, "main") {
		t.Errorf("branch name missing: %q", out)
	}
}

func TestHeaderBar_NoNerdFonts_ColonBranch(t *testing.T) {
	t.Parallel()
	h := HeaderBar{
		Width:     80,
		AppName:   "Hygge",
		Version:   "v0.4",
		Profile:   "default",
		GitBranch: "main",
		Theme:     theme.ShellTheme(),
		NerdFonts: false,
	}
	out := h.View()
	if strings.Contains(out, nerdFontBranch) {
		t.Errorf("nerd font glyph should NOT appear when NerdFonts=false: %q", out)
	}
	if !strings.Contains(out, ":main") {
		t.Errorf("expected ':main' when NerdFonts=false: %q", out)
	}
}

func TestHeaderBar_NoGitBranch_NoBranchSuffix(t *testing.T) {
	t.Parallel()
	h := HeaderBar{
		Width:     80,
		AppName:   "Hygge",
		Version:   "v0.4",
		Profile:   "default",
		GitBranch: "",
		NerdFonts: true,
		Theme:     theme.ShellTheme(),
	}
	out := h.View()
	if strings.Contains(out, nerdFontBranch) {
		t.Errorf("no branch glyph when GitBranch is empty: %q", out)
	}
}

func TestHeaderBar_ZeroCtxAndCost_Hidden(t *testing.T) {
	t.Parallel()
	h := HeaderBar{
		Width:      80,
		AppName:    "Hygge",
		Version:    "v0.4",
		Profile:    "default",
		CtxPercent: 0,
		CostUSD:    0,
		Theme:      theme.ShellTheme(),
	}
	out := h.View()
	if strings.Contains(out, "ctx") {
		t.Errorf("'ctx' should be hidden when CtxPercent=0: %q", out)
	}
	if strings.Contains(out, "$") {
		t.Errorf("'$' should be hidden when CostUSD=0: %q", out)
	}
}

func TestHeaderBar_TildeCollapse(t *testing.T) {
	t.Parallel()
	h := HeaderBar{
		Width:       80,
		AppName:     "Hygge",
		Version:     "v0.4",
		Profile:     "default",
		ProjectPath: "/Users/bob/myproject",
		HomeDir:     "/Users/bob",
		Theme:       theme.ShellTheme(),
	}
	out := h.View()
	if !strings.Contains(out, "~/myproject") {
		t.Errorf("expected tilde-collapsed path: %q", out)
	}
}

func TestHeaderBar_NilTheme_NoCrash(t *testing.T) {
	t.Parallel()
	h := HeaderBar{
		Width:   80,
		AppName: "Hygge",
		Version: "v0.4",
		Profile: "default",
		Theme:   nil,
	}
	out := h.View()
	if !strings.Contains(out, "Hygge") {
		t.Errorf("app name missing with nil theme: %q", out)
	}
}

func TestHeaderBar_CostFormat(t *testing.T) {
	t.Parallel()
	h := HeaderBar{
		Width:   120,
		AppName: "Hygge",
		Version: "v0.4",
		Profile: "default",
		CostUSD: 0.0042,
		Theme:   theme.ShellTheme(),
	}
	out := h.View()
	if !strings.Contains(out, "$0.0042") {
		t.Errorf("expected $0.0042, got: %q", out)
	}
}

func TestHeaderBar_SingleLine(t *testing.T) {
	t.Parallel()
	h := HeaderBar{
		Width:   80,
		AppName: "Hygge",
		Version: "v0.4",
		Profile: "default",
		Theme:   theme.ShellTheme(),
	}
	out := h.View()
	if strings.Contains(out, "\n") {
		t.Errorf("header should be single line, got newline: %q", out)
	}
}

package cli

import (
	"testing"

	"github.com/charmbracelet/colorprofile"
)

func TestTUIColorProfileUsesANSI256ForDefaultMacTerminal(t *testing.T) {
	got := tuiColorProfile([]string{
		"TERM_PROGRAM=Apple_Terminal",
		"TERM=xterm-256color",
	})
	if got != colorprofile.ANSI256 {
		t.Fatalf("profile = %v, want ANSI256", got)
	}
}

func TestTUIColorProfileAllowsTrueColorOptInForMacTerminal(t *testing.T) {
	got := tuiColorProfile([]string{
		"TERM_PROGRAM=Apple_Terminal",
		"TERM=xterm-256color",
		"COLORTERM=truecolor",
	})
	if got != colorprofile.TrueColor {
		t.Fatalf("profile = %v, want TrueColor", got)
	}
}

func TestTUIColorProfileKeepsTrueColorForModernTerminals(t *testing.T) {
	got := tuiColorProfile([]string{
		"TERM_PROGRAM=WezTerm",
		"TERM=xterm-256color",
	})
	if got != colorprofile.TrueColor {
		t.Fatalf("profile = %v, want TrueColor", got)
	}
}

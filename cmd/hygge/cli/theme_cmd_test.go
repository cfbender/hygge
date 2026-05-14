package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/cfbender/hygge/internal/ui/theme"
)

func TestThemeShow(t *testing.T) {
	hermeticHome(t)

	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"theme", "show"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := out.String()
	for _, atom := range theme.AllAtoms() {
		if !strings.Contains(got, string(atom)) {
			t.Errorf("output missing atom %q in:\n%s", atom, got)
		}
	}
	if !strings.Contains(got, "preview:") {
		t.Errorf("output missing preview section:\n%s", got)
	}
}

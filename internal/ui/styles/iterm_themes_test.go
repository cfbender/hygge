package styles

import (
	"slices"
	"testing"
)

func TestKnownNamesIncludesGeneratedItermThemes(t *testing.T) {
	t.Parallel()
	names := KnownNames(LoadOptions{ConfigHome: t.TempDir(), HomeDir: t.TempDir()})
	if len(names) < 500 {
		t.Fatalf("KnownNames returned %d themes, want imported iTerm catalog", len(names))
	}
	if !slices.Contains(names, "claret") {
		t.Fatalf("KnownNames missing claret")
	}
	if !slices.Contains(names, "dracula") {
		t.Fatalf("KnownNames missing dracula")
	}
}

func TestLoadGeneratedItermTheme(t *testing.T) {
	t.Parallel()
	th, err := Load("dracula", LoadOptions{ConfigHome: t.TempDir(), HomeDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Load(dracula) error = %v", err)
	}
	if th.Name != "dracula" {
		t.Fatalf("theme name = %q", th.Name)
	}
	if th.Style(AtomPrimary).GetForeground() == nil {
		t.Fatalf("primary color was not populated")
	}
	if th.Style(AtomCodeBg).GetBackground() == nil {
		t.Fatalf("code background was not populated")
	}
}

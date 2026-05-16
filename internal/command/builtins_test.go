package command

import "testing"

// TestBuiltinSetIsLocked pins the names hygge ships as built-ins.
// Any addition or removal must be a deliberate change to this list.
func TestBuiltinSetIsLocked(t *testing.T) {
	t.Parallel()
	want := []string{
		"apikey",
		"attach",
		"attachments",
		"clear",
		"compact",
		"cost",
		"fork",
		"help",
		"model",
		"new",
		"reason",
		"sessions",
		"theme",
		"version",
		"yolo",
	}
	r := New()
	RegisterBuiltins(r)
	got := names(r.List())
	if !equalStrings(got, want) {
		t.Errorf("built-in set drift:\n got  %v\n want %v", got, want)
	}
}

// TestBuiltinDescriptionsNonEmpty ensures every built-in has a
// description (used by /help and the command palette).
func TestBuiltinDescriptionsNonEmpty(t *testing.T) {
	t.Parallel()
	for _, c := range builtinCommands() {
		if c.Description() == "" {
			t.Errorf("%s missing description", c.Name())
		}
		if c.Source() != "builtin" {
			t.Errorf("%s source = %q, want builtin", c.Name(), c.Source())
		}
	}
}

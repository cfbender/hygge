package config

import (
	"errors"
	"strings"
	"testing"
)

// TestErrorTypes exercises the Error() methods on all typed errors.
func TestErrorTypes(t *testing.T) {
	t.Run("ParseError", func(t *testing.T) {
		inner := errors.New("line 1")
		pe := &ParseError{File: "foo.toml", Err: inner}
		msg := pe.Error()
		if !strings.Contains(msg, "foo.toml") {
			t.Errorf("ParseError.Error() missing file: %q", msg)
		}
		if !errors.Is(pe, inner) {
			// errors.Is unwraps; our Unwrap returns Err.
			t.Error("ParseError.Unwrap should return inner error")
		}
	})

	t.Run("MergeTypeError", func(t *testing.T) {
		me := &MergeTypeError{
			Key: "model.name", LowFile: "a.toml", HighFile: "b.toml",
			LowType: "string", HighType: "int64",
		}
		msg := me.Error()
		if !strings.Contains(msg, "model.name") {
			t.Errorf("MergeTypeError.Error() missing key: %q", msg)
		}
		if !strings.Contains(msg, "a.toml") || !strings.Contains(msg, "b.toml") {
			t.Errorf("MergeTypeError.Error() missing files: %q", msg)
		}
	})

	t.Run("UnknownKeyError with file", func(t *testing.T) {
		e := &UnknownKeyError{Key: "foo", File: "bar.toml"}
		msg := e.Error()
		if !strings.Contains(msg, "foo") || !strings.Contains(msg, "bar.toml") {
			t.Errorf("UnknownKeyError.Error(): %q", msg)
		}
	})

	t.Run("UnknownKeyError without file", func(t *testing.T) {
		e := &UnknownKeyError{Key: "foo"}
		msg := e.Error()
		if !strings.Contains(msg, "foo") {
			t.Errorf("UnknownKeyError.Error(): %q", msg)
		}
	})

	t.Run("InvalidValueError", func(t *testing.T) {
		e := &InvalidValueError{Key: "permission.shell", Value: "maybe", Msg: "bad value"}
		msg := e.Error()
		if !strings.Contains(msg, "permission.shell") {
			t.Errorf("InvalidValueError.Error(): %q", msg)
		}
		if !strings.Contains(msg, "maybe") {
			t.Errorf("InvalidValueError.Error() missing value: %q", msg)
		}
	})
}

// TestCoerceEnvValue exercises the type-coercion helper.
func TestCoerceEnvValue(t *testing.T) {
	cases := []struct {
		in      string
		wantTyp string
	}{
		{"true", "bool"},
		{"false", "bool"},
		{"42", "int64"},
		{"3.14", "float64"},
		{"hello", "string"},
	}
	for _, tc := range cases {
		got := coerceEnvValue(tc.in)
		switch tc.wantTyp {
		case "bool":
			if _, ok := got.(bool); !ok {
				t.Errorf("coerce(%q): expected bool, got %T", tc.in, got)
			}
		case "int64":
			if _, ok := got.(int64); !ok {
				t.Errorf("coerce(%q): expected int64, got %T", tc.in, got)
			}
		case "float64":
			if _, ok := got.(float64); !ok {
				t.Errorf("coerce(%q): expected float64, got %T", tc.in, got)
			}
		case "string":
			if _, ok := got.(string); !ok {
				t.Errorf("coerce(%q): expected string, got %T", tc.in, got)
			}
		}
	}
}

// TestBuildEnvMapFromKeys exercises the hermetic env-map builder.
//
// Env-var path segments are separated by "__" (double underscore).
// Single underscores within a segment are preserved as part of the key name.
//
//	HYGGE_model__provider=openai         → model.provider = "openai"
//	HYGGE_permission__file_write=allow   → permission.file_write = "allow"
//	HYGGE_model__options__thinking_budget=8000 → model.options.thinking_budget = 8000
func TestBuildEnvMapFromKeys(t *testing.T) {
	lookup := makeEnvLookup(map[string]string{
		"HYGGE_model__provider":        "openai",
		"HYGGE_permission__shell":      "deny",
		"HYGGE_model__options__budget": "8000",
	})
	keys := []string{
		"HYGGE_model__provider",
		"HYGGE_permission__shell",
		"HYGGE_model__options__budget",
	}

	m := buildEnvMapFromKeys(keys, lookup)

	model, ok := m["model"].(map[string]any)
	if !ok {
		t.Fatalf("expected model map, got %T", m["model"])
	}
	if model["provider"] != "openai" {
		t.Errorf("model.provider: got %v, want openai", model["provider"])
	}

	perm, ok := m["permission"].(map[string]any)
	if !ok {
		t.Fatalf("expected permission map, got %T", m["permission"])
	}
	if perm["shell"] != "deny" {
		t.Errorf("permission.shell: got %v, want deny", perm["shell"])
	}

	opts, ok := model["options"].(map[string]any)
	if !ok {
		t.Fatalf("expected model.options map, got %T", model["options"])
	}
	if opts["budget"] != int64(8000) {
		t.Errorf("model.options.budget: got %v (%T), want 8000 int64",
			opts["budget"], opts["budget"])
	}
}

// TestBuildEnvMapFromKeys_UnderscoreInSegment verifies that single underscores
// within a segment are preserved as part of the config key name.
//
//	HYGGE_permission__file_write=allow → permission.file_write = "allow"
func TestBuildEnvMapFromKeys_UnderscoreInSegment(t *testing.T) {
	lookup := makeEnvLookup(map[string]string{
		"HYGGE_permission__file_write": "allow",
	})
	m := buildEnvMapFromKeys([]string{"HYGGE_permission__file_write"}, lookup)

	perm, ok := m["permission"].(map[string]any)
	if !ok {
		t.Fatalf("expected permission map, got %T", m["permission"])
	}
	if perm["file_write"] != "allow" {
		t.Errorf("permission.file_write: got %v, want allow", perm["file_write"])
	}
	// Confirm single-underscore split does NOT occur: "file" key must not exist.
	if _, exists := perm["file"]; exists {
		t.Error("permission.file should not exist — single underscore must not split segments")
	}
}

// TestBuildEnvMapFromKeys_DeepUnderscoreKey verifies deeply nested keys with
// underscores in the leaf segment name.
//
//	HYGGE_model__options__thinking_budget=8000 → model.options.thinking_budget = 8000
func TestBuildEnvMapFromKeys_DeepUnderscoreKey(t *testing.T) {
	lookup := makeEnvLookup(map[string]string{
		"HYGGE_model__options__thinking_budget": "8000",
	})
	m := buildEnvMapFromKeys([]string{"HYGGE_model__options__thinking_budget"}, lookup)

	model, ok := m["model"].(map[string]any)
	if !ok {
		t.Fatalf("expected model map, got %T", m["model"])
	}
	opts, ok := model["options"].(map[string]any)
	if !ok {
		t.Fatalf("expected model.options map, got %T", model["options"])
	}
	if opts["thinking_budget"] != int64(8000) {
		t.Errorf("model.options.thinking_budget: got %v (%T), want 8000 int64",
			opts["thinking_budget"], opts["thinking_budget"])
	}
}

// TestBuildEnvMapFromKeys_MalformedEmptySegment verifies that env vars with
// empty segments (produced by triple+ underscores after HYGGE_) are silently
// skipped.  The resulting map must not contain any key from the bad var.
func TestBuildEnvMapFromKeys_MalformedEmptySegment(t *testing.T) {
	// HYGGE___foo has "HYGGE_" stripped → "_foo", split on "__" → ["", "foo"]
	// The empty first segment must cause the var to be skipped.
	lookup := makeEnvLookup(map[string]string{
		"HYGGE___foo": "bad",
	})
	m := buildEnvMapFromKeys([]string{"HYGGE___foo"}, lookup)
	if len(m) != 0 {
		t.Errorf("expected empty map for malformed env var, got %v", m)
	}
}

// TestResolveValue exercises the Explain helper's value resolution for all
// known keys.
func TestResolveValue(t *testing.T) {
	optMap := map[string]any{"thinking_budget": int64(8000)}
	cfg := &Config{
		Model: ModelConfig{
			Provider: "anthropic",
			Name:     "claude-sonnet-4-5",
			Options:  optMap,
		},
		Permission: PermissionConfig{
			FileReadOutsidePwd: PermAsk,
			FileWrite:          PermAllow,
			Shell:              PermDeny,
			Network:            PermDeny,
		},
		Theme: ThemeConfig{Name: "shell"},
	}

	// Scalar-returning cases use == comparison.
	scalarCases := []struct {
		key  string
		want any
	}{
		{"model.provider", "anthropic"},
		{"model.name", "claude-sonnet-4-5"},
		{"model.options.thinking_budget", int64(8000)},
		{"permission.file_read_outside_pwd", PermAsk},
		{"permission.file_write", PermAllow},
		{"permission.shell", PermDeny},
		{"permission.network", PermDeny},
		{"theme.name", "shell"},
	}

	for _, tc := range scalarCases {
		got, err := resolveValue(cfg, tc.key)
		if err != nil {
			t.Errorf("resolveValue(%q): unexpected error: %v", tc.key, err)
			continue
		}
		if got != tc.want {
			t.Errorf("resolveValue(%q): got %v, want %v", tc.key, got, tc.want)
		}
	}

	// model.options returns the map itself — check type only.
	got, err := resolveValue(cfg, "model.options")
	if err != nil {
		t.Fatalf("resolveValue(model.options): %v", err)
	}
	if _, ok := got.(map[string]any); !ok {
		t.Errorf("resolveValue(model.options): expected map[string]any, got %T", got)
	}
}

func TestResolveValue_UnknownKey(t *testing.T) {
	_, err := resolveValue(&Config{}, "nonexistent.key")
	if err == nil {
		t.Fatal("expected error for unknown key")
	}
}

func TestResolveValue_IncompleteKey(t *testing.T) {
	cases := []string{"model", "permission", "model.options.nonexistent"}
	for _, key := range cases {
		_, err := resolveValue(&Config{Model: ModelConfig{Options: map[string]any{}}}, key)
		if err == nil {
			t.Errorf("expected error for key %q, got nil", key)
		}
	}
}

// TestFormatValue exercises formatValue for all branches.
func TestFormatValue(t *testing.T) {
	cases := []struct {
		in      any
		wantSub string
	}{
		{"hello", `"hello"`},
		{PermAsk, `"ask"`},
		{true, "true"},
		{int64(42), "42"},
		{float64(3.14), "3.14"},
		{map[string]any{"k": "v"}, "{...}"},
		{Source{File: "x.toml"}, "(set here)"},
		{nil, "<nil>"},
	}

	for _, tc := range cases {
		got := formatValue(tc.in)
		if !strings.Contains(got, tc.wantSub) {
			t.Errorf("formatValue(%T %v): got %q, want substring %q", tc.in, tc.in, got, tc.wantSub)
		}
	}
}

// TestXdgDirs exercises XDG path computation.
func TestXdgDirs(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		opts := LoadOptions{
			HomeDir:   "/home/user",
			EnvLookup: makeEnvLookup(nil),
		}
		if got := xdgConfigDir(opts); got != "/home/user/.config" {
			t.Errorf("xdgConfigDir: got %q", got)
		}
		if got := xdgStateDir(opts); got != "/home/user/.local/state" {
			t.Errorf("xdgStateDir: got %q", got)
		}
	})

	t.Run("XDG env overrides", func(t *testing.T) {
		opts := LoadOptions{
			HomeDir: "/home/user",
			EnvLookup: makeEnvLookup(map[string]string{
				"XDG_CONFIG_HOME": "/custom/config",
				"XDG_STATE_HOME":  "/custom/state",
			}),
		}
		if got := xdgConfigDir(opts); got != "/custom/config" {
			t.Errorf("xdgConfigDir: got %q", got)
		}
		if got := xdgStateDir(opts); got != "/custom/state" {
			t.Errorf("xdgStateDir: got %q", got)
		}
	})
}

// TestDottedToNested exercises the flag-map conversion helper.
func TestDottedToNested(t *testing.T) {
	flat := map[string]any{
		"model.name":       "gpt-4o",
		"theme.name":       "dark",
		"permission.shell": "deny",
	}
	nested := dottedToNested(flat)

	model, ok := nested["model"].(map[string]any)
	if !ok {
		t.Fatalf("model not a map: %T", nested["model"])
	}
	if model["name"] != "gpt-4o" {
		t.Errorf("model.name: got %v", model["name"])
	}

	perm, ok := nested["permission"].(map[string]any)
	if !ok {
		t.Fatalf("permission not a map: %T", nested["permission"])
	}
	if perm["shell"] != "deny" {
		t.Errorf("permission.shell: got %v", perm["shell"])
	}
}

// TestParseTOMLBytes_BadSyntax verifies that a syntax error returns an error
// with position information.
func TestParseTOMLBytes_BadSyntax(t *testing.T) {
	bad := []byte(`[model
provider = "x"
`)
	_, err := parseTOMLBytes(bad)
	if err == nil {
		t.Fatal("expected parse error")
	}
	// go-toml/v2 DecodeError should contain position info in the string.
	msg := err.Error()
	if !strings.Contains(msg, ":") {
		t.Errorf("parse error should contain line/col info: %q", msg)
	}
}

// TestDeepMerge_NumericCrossType verifies that int64 and float64 are
// treated as compatible (both numeric).
func TestDeepMerge_NumericCrossType(t *testing.T) {
	dst := map[string]any{"budget": int64(8000)}
	src := map[string]any{"budget": float64(9000.0)}
	prov := make(Provenance)
	prov["budget"] = []Source{{File: "low.toml"}}

	if err := deepMergeInto(dst, src, prov, Source{File: "high.toml"}); err != nil {
		t.Fatalf("numeric cross-type should not error: %v", err)
	}
	if dst["budget"] != float64(9000.0) {
		t.Errorf("budget: got %v", dst["budget"])
	}
}

// TestSourceFileOf exercises provenance lookup helper.
func TestSourceFileOf(t *testing.T) {
	prov := Provenance{
		"key": {{File: "a.toml"}, {File: "b.toml"}},
	}
	if got := sourceFileOf(prov, "key"); got != "b.toml" {
		t.Errorf("sourceFileOf: got %q, want b.toml", got)
	}
	if got := sourceFileOf(prov, "missing"); got != "<unknown>" {
		t.Errorf("sourceFileOf missing: got %q, want <unknown>", got)
	}
}

// TestAllHaveMergeKey exercises edge cases of allHaveMergeKey.
func TestAllHaveMergeKey(t *testing.T) {
	if !allHaveMergeKey([]any{}, "id") {
		t.Error("empty slice should be vacuously true")
	}
	withID := []any{
		map[string]any{"id": "a"},
		map[string]any{"id": "b"},
	}
	if !allHaveMergeKey(withID, "id") {
		t.Error("all elements have id, should return true")
	}
	withName := []any{
		map[string]any{"name": "smart"},
		map[string]any{"name": "rush"},
	}
	if !allHaveMergeKey(withName, "name") {
		t.Error("all elements have name, should return true")
	}
	withoutID := []any{
		map[string]any{"id": "a"},
		map[string]any{"name": "no-id"},
	}
	if allHaveMergeKey(withoutID, "id") {
		t.Error("one element missing id, should return false")
	}
	notMap := []any{"scalar"}
	if allHaveMergeKey(notMap, "id") {
		t.Error("scalar element, should return false")
	}
}

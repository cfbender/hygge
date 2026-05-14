package config

import (
	"errors"
	"testing"
)

// TestDeepMerge_ScalarsOverride verifies that higher-precedence scalars
// override lower-precedence ones.
func TestDeepMerge_ScalarsOverride(t *testing.T) {
	dst := map[string]any{"key": "low"}
	src := map[string]any{"key": "high"}
	prov := make(Provenance)

	srcLow := Source{File: "low.toml"}
	prov["key"] = []Source{srcLow}

	srcHigh := Source{File: "high.toml"}
	if err := deepMergeInto(dst, src, prov, srcHigh); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := dst["key"]; got != "high" {
		t.Errorf("got %q, want %q", got, "high")
	}
	sources := prov["key"]
	if len(sources) != 2 || sources[1].File != "high.toml" {
		t.Errorf("provenance not updated correctly: %v", sources)
	}
}

// TestDeepMerge_MapsRecursive verifies that maps are merged by key, not replaced.
func TestDeepMerge_MapsRecursive(t *testing.T) {
	dst := map[string]any{
		"model": map[string]any{
			"provider": "anthropic",
			"name":     "old-name",
		},
	}
	src := map[string]any{
		"model": map[string]any{
			"name": "new-name",
		},
	}
	prov := make(Provenance)
	if err := deepMergeInto(dst, src, prov, Source{File: "high.toml"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	model := dst["model"].(map[string]any)
	if got := model["provider"]; got != "anthropic" {
		t.Errorf("provider should survive merge, got %q", got)
	}
	if got := model["name"]; got != "new-name" {
		t.Errorf("name should be overridden, got %q", got)
	}
}

// TestDeepMerge_ArraysReplacedWholesale verifies that scalar arrays are
// replaced wholesale (not concatenated).
func TestDeepMerge_ArraysReplacedWholesale(t *testing.T) {
	dst := map[string]any{"tags": []any{"a", "b", "c"}}
	src := map[string]any{"tags": []any{"x", "y"}}
	prov := make(Provenance)

	if err := deepMergeInto(dst, src, prov, Source{File: "high.toml"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tags := dst["tags"].([]any)
	if len(tags) != 2 || tags[0] != "x" || tags[1] != "y" {
		t.Errorf("expected [x y], got %v", tags)
	}
}

// TestDeepMerge_ArraysOfTablesByID verifies that arrays of tables with "id"
// fields are merged by id.
func TestDeepMerge_ArraysOfTablesByID(t *testing.T) {
	dst := map[string]any{
		"servers": []any{
			map[string]any{"id": "a", "cmd": "old-a"},
			map[string]any{"id": "b", "cmd": "cmd-b"},
		},
	}
	src := map[string]any{
		"servers": []any{
			map[string]any{"id": "a", "cmd": "new-a"},
			map[string]any{"id": "c", "cmd": "cmd-c"},
		},
	}
	prov := make(Provenance)

	if err := deepMergeInto(dst, src, prov, Source{File: "high.toml"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	servers := dst["servers"].([]any)
	if len(servers) != 3 {
		t.Fatalf("expected 3 servers, got %d: %v", len(servers), servers)
	}

	byID := map[string]map[string]any{}
	for _, v := range servers {
		m := v.(map[string]any)
		byID[m["id"].(string)] = m
	}

	if got := byID["a"]["cmd"]; got != "new-a" {
		t.Errorf("server a: expected cmd=new-a, got %v", got)
	}
	if got := byID["b"]["cmd"]; got != "cmd-b" {
		t.Errorf("server b: expected cmd=cmd-b, got %v", got)
	}
	if _, ok := byID["c"]; !ok {
		t.Error("server c should be present")
	}
}

// TestDeepMerge_UnsetSentinelClearsKey verifies the __hygge_unset__ sentinel.
func TestDeepMerge_UnsetSentinelClearsKey(t *testing.T) {
	dst := map[string]any{"key": "value", "other": "keep"}
	src := map[string]any{"key": unsetSentinel}
	prov := make(Provenance)
	prov["key"] = []Source{{File: "low.toml"}}

	if err := deepMergeInto(dst, src, prov, Source{File: "high.toml"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, ok := dst["key"]; ok {
		t.Error("key should have been removed by unset sentinel")
	}
	if dst["other"] != "keep" {
		t.Error("other key should be unaffected")
	}
}

// TestDeepMerge_TypeMismatchError verifies that a type mismatch returns an error.
func TestDeepMerge_TypeMismatchError(t *testing.T) {
	dst := map[string]any{"count": int64(5)}
	src := map[string]any{"count": "not-a-number"}
	prov := make(Provenance)
	prov["count"] = []Source{{File: "low.toml"}}

	err := deepMergeInto(dst, src, prov, Source{File: "high.toml"})
	if err == nil {
		t.Fatal("expected error for type mismatch, got nil")
	}

	var te *MergeTypeError
	if !errors.As(err, &te) {
		t.Fatalf("expected MergeTypeError, got %T: %v", err, err)
	}
	if te.Key != "count" {
		t.Errorf("expected key=count, got %q", te.Key)
	}
	if te.LowFile != "low.toml" || te.HighFile != "high.toml" {
		t.Errorf("expected low=low.toml high=high.toml, got low=%q high=%q", te.LowFile, te.HighFile)
	}
}

// TestDeepMerge_NewKeyFromHigher verifies keys only in the higher layer are added.
func TestDeepMerge_NewKeyFromHigher(t *testing.T) {
	dst := map[string]any{"existing": "yes"}
	src := map[string]any{"new": "value"}
	prov := make(Provenance)

	if err := deepMergeInto(dst, src, prov, Source{File: "high.toml"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if dst["new"] != "value" {
		t.Errorf("new key not set, dst=%v", dst)
	}
	if dst["existing"] != "yes" {
		t.Errorf("existing key removed, dst=%v", dst)
	}
}

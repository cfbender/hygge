package plugin_test

import (
	"context"
	"testing"

	"github.com/cfbender/hygge/internal/hook"
	"github.com/cfbender/hygge/internal/plugin"
)

// TestRegistry_install verifies basic Install and Get round-trip.
func TestRegistry_install(t *testing.T) {
	reg, _, _, _, _ := buildTestRegistry(t)

	dir := testdataDir(t)
	src := "local:" + dir + "/hello"

	if err := reg.Install(context.Background(), src); err != nil {
		t.Fatalf("Install: %v", err)
	}

	p, ok := reg.Get("hello")
	if !ok {
		t.Fatal("Get('hello'): not found")
	}
	if p.Name() != "hello" {
		t.Errorf("Name() = %q, want %q", p.Name(), "hello")
	}
	if p.Source() != src {
		t.Errorf("Source() = %q, want %q", p.Source(), src)
	}

	all := reg.List()
	if len(all) != 1 {
		t.Errorf("List() len = %d, want 1", len(all))
	}
}

// TestRegistry_duplicateInstall verifies that installing the same plugin twice
// is silently skipped.
func TestRegistry_duplicateInstall(t *testing.T) {
	reg, _, _, _, _ := buildTestRegistry(t)

	dir := testdataDir(t)
	src := "local:" + dir + "/hello"

	if err := reg.Install(context.Background(), src); err != nil {
		t.Fatalf("first Install: %v", err)
	}
	// Second install should be a no-op (same plugin name).
	if err := reg.Install(context.Background(), src); err != nil {
		t.Logf("second Install returned (expected): %v", err)
	}

	// Should still have only one loaded.
	if n := len(reg.List()); n != 1 {
		t.Errorf("List() len = %d after duplicate install, want 1", n)
	}
}

// TestRegistry_remove verifies that removing a plugin closes it.
func TestRegistry_remove(t *testing.T) {
	reg, toolReg, hookReg, cmdReg, subReg := buildTestRegistry(t)

	dir := testdataDir(t)
	src := "local:" + dir + "/hello"

	if err := reg.Install(context.Background(), src); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if _, ok := toolReg.Get("hello_world"); !ok {
		t.Fatal("hello_world not registered before Remove")
	}
	if err := reg.Remove(context.Background(), "hello"); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	if _, ok := reg.Get("hello"); ok {
		t.Error("plugin 'hello' still present after Remove")
	}
	if _, ok := toolReg.Get("hello_world"); ok {
		t.Error("tool 'hello_world' still registered after Remove")
	}
	if len(hookReg.For(hook.EventPreTool)) != 0 {
		t.Error("pre_tool hook still registered after Remove")
	}
	if _, ok := cmdReg.Get("greet"); ok {
		t.Error("command 'greet' still registered after Remove")
	}
	if _, ok := subReg.Get("pluginagent"); ok {
		t.Error("subagent 'pluginagent' still registered after Remove")
	}
}

// TestRegistry_loadAll verifies that LoadAll skips failures.
func TestRegistry_loadAll(t *testing.T) {
	reg, _, _, _, _ := buildTestRegistry(t)

	base := testdataDir(t)
	sources := []string{
		"local:" + base + "/hello",
		"local:" + base + "/nonexistent", // should fail silently
		"local:" + base + "/notify",
	}

	reg.LoadAll(context.Background(), sources)

	all := reg.List()
	if len(all) < 2 {
		t.Errorf("LoadAll: expected at least 2 plugins loaded, got %d", len(all))
	}
	if _, ok := reg.Get("hello"); !ok {
		t.Error("hello plugin not loaded")
	}
	if _, ok := reg.Get("notify"); !ok {
		t.Error("notify plugin not loaded")
	}
}

// TestRegistry_ownedRegistrations verifies the ownership tracking.
func TestRegistry_ownedRegistrations(t *testing.T) {
	reg, _, _, _, _ := buildTestRegistry(t)

	dir := testdataDir(t)
	if err := reg.Install(context.Background(), "local:"+dir+"/hello"); err != nil {
		t.Fatalf("Install: %v", err)
	}

	owned := reg.OwnedRegistrations()
	if len(owned) == 0 {
		t.Error("OwnedRegistrations: expected non-empty list")
	}

	// Check that the hello plugin's tool is tracked.
	var foundTool bool
	for _, o := range owned {
		if o.Kind == "tool" && o.Name == "hello_world" {
			foundTool = true
			break
		}
	}
	if !foundTool {
		t.Error("OwnedRegistrations: hello_world tool not tracked")
	}
}

// TestRegistry_sourceRoundtrip verifies Source() returns the original URI.
func TestRegistry_sourceRoundtrip(t *testing.T) {
	reg, _, _, _, _ := buildTestRegistry(t)

	dir := testdataDir(t)
	srcURI := "local:" + dir + "/hello"

	if err := reg.Install(context.Background(), srcURI); err != nil {
		t.Fatalf("Install: %v", err)
	}

	src, ok := reg.Source("hello")
	if !ok {
		t.Fatal("Source('hello'): not found")
	}
	if src.Raw != srcURI {
		t.Errorf("Source.Raw = %q, want %q", src.Raw, srcURI)
	}
	if src.Kind != plugin.SourceLocal {
		t.Errorf("Source.Kind = %q, want local", src.Kind)
	}
}

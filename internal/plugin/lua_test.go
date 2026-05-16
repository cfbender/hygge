package plugin_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/cfbender/hygge/internal/command"
	"github.com/cfbender/hygge/internal/hook"
	"github.com/cfbender/hygge/internal/permission"
	"github.com/cfbender/hygge/internal/plugin"
	"github.com/cfbender/hygge/internal/subagent"
	"github.com/cfbender/hygge/internal/tool"
)

// testdataDir returns the absolute path to internal/plugin/testdata/plugins.
func testdataDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(file), "testdata", "plugins")
}

// buildTestRegistry creates a Registry wired with in-memory registries.
// It does NOT load any plugins; callers call Install or LoadAll.
func buildTestRegistry(t *testing.T) (*plugin.Registry, *tool.Registry, *hook.Registry, *command.Registry, *subagent.Registry) {
	t.Helper()
	toolReg := tool.NewRegistry()
	hookReg := hook.New()
	cmdReg := command.New()
	subReg, err := subagent.Load(subagent.LoadOptions{})
	if err != nil {
		t.Fatalf("subagent.Load: %v", err)
	}

	// Build a minimal bus+permission engine.  Tests don't need real permission asks;
	// we pass nil Permission to the registry so the tool adapter skips the ask.
	reg, err := plugin.NewRegistry(plugin.RegistryOptions{
		CacheDir:   t.TempDir(),
		Tools:      toolReg,
		Hooks:      hookReg,
		Commands:   cmdReg,
		Subagents:  subReg,
		Permission: buildTestPermission(t),
	})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	return reg, toolReg, hookReg, cmdReg, subReg
}

// buildTestPermission builds a minimal permission engine that always allows.
func buildTestPermission(t *testing.T) *permission.Engine {
	t.Helper()
	// We cannot build a real permission engine without a bus.  For tests
	// we accept a nil permission engine and let the adapter skip the ask.
	return nil
}

// TestLuaLoader_registerTool verifies that a Lua plugin can register a tool
// that invokes correctly.
func TestLuaLoader_registerTool(t *testing.T) {
	reg, toolReg, _, _, _ := buildTestRegistry(t)

	dir := filepath.Join(testdataDir(t), "hello")
	if err := reg.Install(context.Background(), "local:"+dir); err != nil {
		t.Fatalf("Install: %v", err)
	}

	// The tool should be registered.
	toolFn, ok := toolReg.Get("hello_world")
	if !ok {
		t.Fatal("tool 'hello_world' not registered")
	}

	// Execute it.
	input := json.RawMessage(`{"name":"Hygge"}`)
	result, err := toolFn.Execute(context.Background(), input, tool.ExecContext{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected IsError: content=%q", result.Content)
	}
	if result.Content != "Hello, Hygge!" {
		t.Errorf("Content = %q, want %q", result.Content, "Hello, Hygge!")
	}
}

// TestLuaLoader_registerHook verifies that a Lua plugin can register a hook
// that denies a tool call.
func TestLuaLoader_registerHook(t *testing.T) {
	reg, _, hookReg, _, _ := buildTestRegistry(t)

	dir := filepath.Join(testdataDir(t), "hello")
	if err := reg.Install(context.Background(), "local:"+dir); err != nil {
		t.Fatalf("Install: %v", err)
	}

	// The hook should be registered.
	hooks := hookReg.For(hook.EventPreTool)
	if len(hooks) == 0 {
		t.Fatal("no pre_tool hooks registered")
	}

	// Run the hook with a blocked tool name.
	in := hook.Input{
		Event:    hook.EventPreTool,
		ToolName: "blocked_tool",
		HookName: "test",
	}
	_, dec, denier, reason, _ := hookReg.RunPre(context.Background(), hook.EventPreTool, in)
	if dec != hook.DecisionDeny {
		t.Errorf("expected Deny decision, got %v", dec)
	}
	if denier == "" {
		t.Error("expected non-empty denier")
	}
	if reason == "" {
		t.Error("expected non-empty reason")
	}

	// Allow a non-blocked tool.
	in.ToolName = "some_tool"
	_, dec2, _, _, _ := hookReg.RunPre(context.Background(), hook.EventPreTool, in)
	if dec2 != hook.DecisionAllow {
		t.Errorf("expected Allow decision for non-blocked tool, got %v", dec2)
	}
}

// TestLuaLoader_registerCommand verifies that a command registered by a plugin
// is callable.
func TestLuaLoader_registerCommand(t *testing.T) {
	reg, _, _, cmdReg, _ := buildTestRegistry(t)

	dir := filepath.Join(testdataDir(t), "hello")
	if err := reg.Install(context.Background(), "local:"+dir); err != nil {
		t.Fatalf("Install: %v", err)
	}

	cmd, ok := cmdReg.Get("greet")
	if !ok {
		t.Fatal("command 'greet' not registered")
	}

	outcome, err := cmd.Execute(context.Background(), nil, "test-input")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if outcome.Message == "" {
		t.Error("expected non-empty outcome.Message")
	}
}

// TestLuaLoader_registerSubagent verifies that a subagent registered by a
// plugin appears in the subagent registry.
func TestLuaLoader_registerSubagent(t *testing.T) {
	reg, _, _, _, subReg := buildTestRegistry(t)

	dir := filepath.Join(testdataDir(t), "hello")
	if err := reg.Install(context.Background(), "local:"+dir); err != nil {
		t.Fatalf("Install: %v", err)
	}

	sa, ok := subReg.Get("pluginagent")
	if !ok {
		t.Fatal("subagent 'pluginagent' not registered")
	}
	if sa.Description == "" {
		t.Error("expected non-empty Description")
	}
}

// TestLuaLoader_panicHandler verifies that a Lua error in execute fails-open.
func TestLuaLoader_panicHandler(t *testing.T) {
	reg, toolReg, _, _, _ := buildTestRegistry(t)

	dir := filepath.Join(testdataDir(t), "error")
	if err := reg.Install(context.Background(), "local:"+dir); err != nil {
		t.Fatalf("Install: %v", err)
	}

	toolFn, ok := toolReg.Get("error_tool")
	if !ok {
		t.Fatal("tool 'error_tool' not registered")
	}

	// Execute should return an IsError result, not panic.
	result, err := toolFn.Execute(context.Background(), json.RawMessage(`{}`), tool.ExecContext{})
	if err != nil {
		t.Fatalf("Execute returned unexpected error: %v", err)
	}
	if !result.IsError {
		t.Errorf("expected IsError=true, content=%q", result.Content)
	}
}

// TestLuaLoader_concurrentCalls verifies that concurrent tool calls via the
// same plugin are serialised safely (no data races).
func TestLuaLoader_concurrentCalls(t *testing.T) {
	reg, toolReg, _, _, _ := buildTestRegistry(t)

	dir := filepath.Join(testdataDir(t), "hello")
	if err := reg.Install(context.Background(), "local:"+dir); err != nil {
		t.Fatalf("Install: %v", err)
	}

	toolFn, ok := toolReg.Get("hello_world")
	if !ok {
		t.Fatal("tool 'hello_world' not registered")
	}

	const goroutines = 20
	var wg sync.WaitGroup
	errors := make(chan error, goroutines)

	for i := range goroutines {
		wg.Add(1)
		go func(_ int) {
			defer wg.Done()
			input := json.RawMessage(`{"name":"concurrent"}`)
			result, err := toolFn.Execute(context.Background(), input, tool.ExecContext{})
			if err != nil {
				errors <- err
				return
			}
			if result.IsError {
				errors <- nil
			}
		}(i)
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Errorf("concurrent Execute error: %v", err)
		}
	}
}

// TestLuaLoader_invalidRegistration verifies that an invalid Lua plugin
// (missing required fields) fails at Load time.
func TestLuaLoader_invalidRegistration(t *testing.T) {
	reg, _, _, _, _ := buildTestRegistry(t)

	dir := filepath.Join(testdataDir(t), "invalid")
	err := reg.Install(context.Background(), "local:"+dir)
	if err == nil {
		t.Fatal("expected error for invalid registration (missing name)")
	}
}

// TestRegistry_multiplePlugins verifies that loading multiple plugins doesn't
// interfere and that a failure in one doesn't block others.
func TestRegistry_multiplePlugins(t *testing.T) {
	reg, toolReg, _, _, _ := buildTestRegistry(t)
	base := testdataDir(t)

	// hello plugin: valid
	if err := reg.Install(context.Background(), "local:"+filepath.Join(base, "hello")); err != nil {
		t.Fatalf("Install hello: %v", err)
	}

	// invalid plugin: should fail but not crash
	err := reg.Install(context.Background(), "local:"+filepath.Join(base, "invalid"))
	if err == nil {
		t.Log("invalid plugin silently skipped (LoadAll behaviour; Install surfaces error)")
	}

	// hello tool should still work after the invalid install attempt.
	_, ok := toolReg.Get("hello_world")
	if !ok {
		t.Error("hello_world tool missing after failed install of another plugin")
	}
}

// TestRegistry_notifyPlugin verifies the notify test fixture loads without error.
func TestRegistry_notifyPlugin(t *testing.T) {
	reg, _, _, _, _ := buildTestRegistry(t)

	dir := filepath.Join(testdataDir(t), "notify")
	// Should not error even though it only calls notify/log.
	if err := reg.Install(context.Background(), "local:"+dir); err != nil {
		t.Fatalf("Install notify: %v", err)
	}
}
